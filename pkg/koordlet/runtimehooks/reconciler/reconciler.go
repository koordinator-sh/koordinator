/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reconciler

import (
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type ReconcilerLevel string

const (
	KubeQOSLevel   ReconcilerLevel = "kubeqos"
	PodLevel       ReconcilerLevel = "pod"
	ContainerLevel ReconcilerLevel = "container"
	SandboxLevel   ReconcilerLevel = "sandbox"
	AllPodsLevel   ReconcilerLevel = "allpods"
)

var globalCgroupReconcilers = struct {
	all []*cgroupReconciler

	kubeQOSLevel   map[string]*cgroupReconciler
	podLevel       map[string]*cgroupReconciler
	containerLevel map[string]*cgroupReconciler

	sandboxContainerLevel map[string]*cgroupReconciler
	allPodsLevel          map[string]*cgroupReconciler
}{
	kubeQOSLevel:   map[string]*cgroupReconciler{},
	podLevel:       map[string]*cgroupReconciler{},
	containerLevel: map[string]*cgroupReconciler{},

	sandboxContainerLevel: map[string]*cgroupReconciler{},
	allPodsLevel:          map[string]*cgroupReconciler{},
}

type cgroupReconciler struct {
	cgroupFile  system.Resource
	description map[string]string
	level       ReconcilerLevel
	filter      Filter
	fn          map[string]reconcileFunc
	fn4AllPods  map[string]reconcileFunc4AllPods
}

// Filter & Conditions:
// 1. a condition for one cgroup file should have no more than one filter/index func
// 2. different indexes of one cgroup file can have different reconcile functions
// 3. indexes for one cgroup should be enumerable
type Filter interface {
	Name() string
	Filter(podMeta *statesinformer.PodMeta) string
}

type noneFilter struct{}

const (
	NoneFilterCondition = ""
	NoneFilterName      = "none"
)

func (d *noneFilter) Name() string {
	return NoneFilterName
}

func (d *noneFilter) Filter(podMeta *statesinformer.PodMeta) string {
	return NoneFilterCondition
}

var singletonNoneFilter *noneFilter

// NoneFilter returns a Filter which skip filtering anything (into the same condition)
func NoneFilter() Filter {
	if singletonNoneFilter == nil {
		singletonNoneFilter = &noneFilter{}
	}
	return singletonNoneFilter
}

type podQOSFilter struct{}

const (
	PodQOSFilterName = "podQOS"
	HostNetWork      = "hostNetwork"
)

func (p *podQOSFilter) Name() string {
	return PodQOSFilterName
}

func (p *podQOSFilter) Filter(podMeta *statesinformer.PodMeta) string {
	qosClass := apiext.GetPodQoSClassRaw(podMeta.Pod)

	// consider as LSR if pod is qos=None and has cpuset
	if qosClass == apiext.QoSNone && podMeta.Pod != nil && podMeta.Pod.Annotations != nil {
		cpuset, _ := util.GetCPUSetFromPod(podMeta.Pod.Annotations)
		if len(cpuset) > 0 {
			return string(apiext.QoSLSR)
		}
	}

	return string(qosClass)
}

type podHostNetworkFilter struct{}

func (p *podHostNetworkFilter) Name() string {
	return HostNetWork
}

func (p *podHostNetworkFilter) Filter(podMeta *statesinformer.PodMeta) string {
	return strconv.FormatBool(podMeta.Pod.Spec.HostNetwork)
}

var singletonPodQOSFilter *podQOSFilter

// PodQOSFilter returns a Filter which filters pod qos class
func PodQOSFilter() Filter {
	if singletonPodQOSFilter == nil {
		singletonPodQOSFilter = &podQOSFilter{}
	}
	return singletonPodQOSFilter
}

var singletonPodHostNetworkFilter *podHostNetworkFilter

// PodHostNetworkFilter returns a Filter which filters pod hostnetwork is true
func PodHostNetworkFilter() *podHostNetworkFilter {
	if singletonPodQOSFilter == nil {
		singletonPodHostNetworkFilter = &podHostNetworkFilter{}
	}
	return singletonPodHostNetworkFilter
}

type podAnnotationResctrlFilter struct{}

const (
	podAnnotationResctrlFilterName = "resctrl"
)

func (p *podAnnotationResctrlFilter) Name() string {
	return podAnnotationResctrlFilterName
}

func (p *podAnnotationResctrlFilter) Filter(podMeta *statesinformer.PodMeta) string {
	if _, ok := podMeta.Pod.Annotations[apiext.AnnotationResctrl]; ok {
		return podAnnotationResctrlFilterName
	}

	return ""
}

var singletonPodAnnotationResctrlFilter *podAnnotationResctrlFilter

// PodQOSFilter returns a Filter which filters pod qos class
func PodAnnotationResctrlFilter() *podAnnotationResctrlFilter {
	if singletonPodQOSFilter == nil {
		singletonPodQOSFilter = &podQOSFilter{}
	}
	return singletonPodAnnotationResctrlFilter
}

type reconcileFunc func(protocol.HooksProtocol) error
type reconcileFunc4AllPods func([]protocol.HooksProtocol) error

func RegisterCgroupReconciler4AllPods(level ReconcilerLevel, cgroupFile system.Resource, description string,
	fn reconcileFunc4AllPods, filter Filter, conditions ...string) {
	if len(conditions) <= 0 { // default condition
		conditions = []string{NoneFilterCondition}
	}

	for _, r := range globalCgroupReconcilers.all {
		if level != r.level || cgroupFile.ResourceType() != r.cgroupFile.ResourceType() {
			continue
		}

		for _, condition := range conditions {
			// if reconciler exist
			if r.filter.Name() != filter.Name() {
				klog.Fatalf("%v of level %v is already registered with filter %v by %v, cannot change to %v by %v",
					cgroupFile.ResourceType(), level, r.filter.Name(), r.description[condition], filter.Name(), description)
			}

			if _, ok := r.fn[condition]; ok {
				klog.Fatalf("%v of level %v is already registered with condition %v by %v, cannot change by %v",
					cgroupFile.ResourceType(), level, condition, r.description[condition], description)
			}

			r.fn4AllPods[condition] = fn
			r.description[condition] = description
		}
		klog.V(1).Infof("register reconcile function %v finished, info: level=%v, resourceType=%v, add conditions=%v",
			description, level, cgroupFile.ResourceType(), conditions)
		return
	}

	// if reconciler not exist
	r := &cgroupReconciler{
		cgroupFile:  cgroupFile,
		description: map[string]string{},
		level:       level,
		fn:          map[string]reconcileFunc{},
		fn4AllPods:  map[string]reconcileFunc4AllPods{},
	}

	globalCgroupReconcilers.all = append(globalCgroupReconcilers.all, r)
	switch level {
	case AllPodsLevel:
		r.filter = filter
		for _, condition := range conditions {
			r.fn4AllPods[condition] = fn
		}
		globalCgroupReconcilers.allPodsLevel[string(r.cgroupFile.ResourceType())] = r
	default:
		klog.Fatalf("cgroup level %v is not supported", level)
	}
	klog.V(1).Infof("register reconcile function %v finished, info: level=%v, resourceType=%v, filter=%v, conditions=%v",
		description, level, cgroupFile.ResourceType(), filter.Name(), conditions)
}

// RegisterCgroupReconciler registers a cgroup reconciler according to the cgroup file, reconcile function and filter
// conditions. A cgroup file of one level can have multiple reconcile functions with different filtered conditions.
//
//	e.g. pod-level cfs_quota can be registered both by cpuset hook and batchresource hook. While cpuset hook reconciles
//	cfs_quota for LSE and LSR pods, batchresource reconciles pods of BE QoS.
//
// TODO: support priority+qos filter.
func RegisterCgroupReconciler(level ReconcilerLevel, cgroupFile system.Resource, description string,
	fn reconcileFunc, filter Filter, conditions ...string) {
	if len(conditions) <= 0 { // default condition
		conditions = []string{NoneFilterCondition}
	}

	for _, r := range globalCgroupReconcilers.all {
		if level != r.level || cgroupFile.ResourceType() != r.cgroupFile.ResourceType() {
			continue
		}

		for _, condition := range conditions {
			// if reconciler exist
			if r.filter.Name() != filter.Name() {
				klog.Fatalf("%v of level %v is already registered with filter %v by %v, cannot change to %v by %v",
					cgroupFile.ResourceType(), level, r.filter.Name(), r.description[condition], filter.Name(), description)
			}

			if _, ok := r.fn[condition]; ok {
				klog.Fatalf("%v of level %v is already registered with condition %v by %v, cannot change by %v",
					cgroupFile.ResourceType(), level, condition, r.description[condition], description)
			}

			r.fn[condition] = fn
			r.description[condition] = description
		}
		klog.V(1).Infof("register reconcile function %v finished, info: level=%v, resourceType=%v, add conditions=%v",
			description, level, cgroupFile.ResourceType(), conditions)
		return
	}

	// if reconciler not exist
	r := &cgroupReconciler{
		cgroupFile:  cgroupFile,
		description: map[string]string{},
		level:       level,
		fn:          map[string]reconcileFunc{},
		fn4AllPods:  map[string]reconcileFunc4AllPods{},
	}

	globalCgroupReconcilers.all = append(globalCgroupReconcilers.all, r)
	switch level {
	case KubeQOSLevel:
		r.filter = NoneFilter()
		r.fn[NoneFilterCondition] = fn
		r.description[NoneFilterCondition] = description
		globalCgroupReconcilers.kubeQOSLevel[string(r.cgroupFile.ResourceType())] = r
	case PodLevel:
		r.filter = filter
		for _, condition := range conditions {
			r.fn[condition] = fn
			r.description[condition] = description
		}
		globalCgroupReconcilers.podLevel[string(r.cgroupFile.ResourceType())] = r
	case ContainerLevel:
		r.filter = filter
		for _, condition := range conditions {
			r.fn[condition] = fn
			r.description[condition] = description
		}
		globalCgroupReconcilers.containerLevel[string(r.cgroupFile.ResourceType())] = r
	case SandboxLevel:
		r.filter = filter
		for _, condition := range conditions {
			r.fn[condition] = fn
			r.description[condition] = description
		}
		globalCgroupReconcilers.sandboxContainerLevel[string(r.cgroupFile.ResourceType())] = r
	default:
		klog.Fatalf("cgroup level %v is not supported", level)
	}
	klog.V(1).Infof("register reconcile function %v finished, info: level=%v, resourceType=%v, filter=%v, conditions=%v",
		description, level, cgroupFile.ResourceType(), filter.Name(), conditions)
}

type Reconciler interface {
	Run(stopCh <-chan struct{}) error
}

type Context struct {
	StatesInformer    statesinformer.StatesInformer
	Executor          resourceexecutor.ResourceUpdateExecutor
	ReconcileInterval time.Duration
	EventRecorder     record.EventRecorder
}

func NewReconciler(ctx Context) Reconciler {
	r := &reconciler{
		podUpdated:        make(chan struct{}, 1),
		executor:          ctx.Executor,
		reconcileInterval: ctx.ReconcileInterval,
		eventRecorder:     ctx.EventRecorder,
	}
	// TODO register individual pod event
	ctx.StatesInformer.RegisterCallbacks(statesinformer.RegisterTypeAllPods, "runtime-hooks-reconciler",
		"Reconcile cgroup files if pod updated", r.podRefreshCallback)
	return r
}

type reconciler struct {
	podsMutex         sync.RWMutex
	podsMeta          []*statesinformer.PodMeta
	podUpdated        chan struct{}
	executor          resourceexecutor.ResourceUpdateExecutor
	reconcileInterval time.Duration
	eventRecorder     record.EventRecorder
}

func (c *reconciler) Run(stopCh <-chan struct{}) error {
	go c.reconcilePodCgroup(stopCh)
	go c.reconcileKubeQOSCgroup(stopCh)
	klog.V(1).Infof("start runtime hook reconciler successfully")
	return nil
}

func (c *reconciler) podRefreshCallback(t statesinformer.RegisterType, o interface{}, target *statesinformer.CallbackTarget) {
	if target == nil {
		klog.Warningf("callback target is nil")
		return
	}
	c.podsMutex.Lock()
	defer c.podsMutex.Unlock()
	c.podsMeta = target.Pods
	if len(c.podUpdated) == 0 {
		c.podUpdated <- struct{}{}
	}
}

func (c *reconciler) getPodsMeta() []*statesinformer.PodMeta {
	c.podsMutex.RLock()
	defer c.podsMutex.RUnlock()
	result := make([]*statesinformer.PodMeta, len(c.podsMeta))
	copy(result, c.podsMeta)
	return result
}

func (c *reconciler) reconcileKubeQOSCgroup(stopCh <-chan struct{}) {
	// TODO refactor kubeqos reconciler, inotify watch corresponding cgroup file and update only when receive modified event
	timer := time.NewTimer(c.reconcileInterval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			doKubeQOSCgroup(c.executor)
			timer.Reset(c.reconcileInterval)
		case <-stopCh:
			klog.V(1).Infof("stop reconcile kube qos cgroup")
		}
	}
}

func doKubeQOSCgroup(e resourceexecutor.ResourceUpdateExecutor) {
	for _, kubeQOS := range []corev1.PodQOSClass{
		corev1.PodQOSGuaranteed, corev1.PodQOSBurstable, corev1.PodQOSBestEffort} {
		for resourceType, r := range globalCgroupReconcilers.kubeQOSLevel {
			kubeQOSCtx := protocol.HooksProtocolBuilder.KubeQOS(kubeQOS)
			reconcileFn, ok := r.fn[NoneFilterCondition]
			if !ok { // all kube qos reconcilers should register in this condition
				klog.Warningf("calling reconcile function %v failed, error condition %s not registered",
					r.description[NoneFilterCondition], NoneFilterCondition)
				continue
			}
			start := time.Now()
			if err := reconcileFn(kubeQOSCtx); err != nil {
				metrics.RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(string(KubeQOSLevel), resourceType, err, metrics.SinceInSeconds(start))
				klog.Warningf("calling reconcile function %v for kube qos %v failed, error %v",
					r.description[NoneFilterCondition], kubeQOS, err)
			} else {
				kubeQOSCtx.ReconcilerDone(e)
				metrics.RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(string(KubeQOSLevel), resourceType, nil, metrics.SinceInSeconds(start))
				klog.V(5).Infof("calling reconcile function %v for kube qos %v finish",
					r.description[NoneFilterCondition], kubeQOS)
			}
		}
	}
}

func (c *reconciler) reconcilePodCgroup(stopCh <-chan struct{}) {
	// TODO refactor pod reconciler, inotify watch corresponding cgroup file and update only when receive modified event
	// new watcher will be added with new pod created, and deleted with pod destroyed
	for {
		select {
		case <-c.podUpdated:
			podsMeta := c.getPodsMeta()
			for _, podMeta := range podsMeta {
				for resourceType, r := range globalCgroupReconcilers.podLevel {
					condition := r.filter.Filter(podMeta)
					reconcileFn, ok := r.fn[condition]
					if !ok {
						klog.V(5).Infof("calling reconcile function %v aborted for pod %v, condition %s not registered",
							r.description[condition], podMeta.Key(), condition)
						continue
					}

					podCtx := protocol.HooksProtocolBuilder.Pod(podMeta)
					start := time.Now()
					if err := reconcileFn(podCtx); err != nil {
						metrics.RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(string(PodLevel), resourceType, err, metrics.SinceInSeconds(start))
						klog.Warningf("calling reconcile function %v for pod %v failed, error %v",
							r.description[condition], podMeta.Key(), err)
					} else {
						podCtx.ReconcilerDone(c.executor)
						metrics.RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(string(PodLevel), resourceType, nil, metrics.SinceInSeconds(start))
						klog.V(5).Infof("calling reconcile function %v for pod %v finished",
							r.description[condition], podMeta.Key())
					}
					podCtx.RecordEvent(c.eventRecorder, podMeta.Pod)
				}

				for resourceType, r := range globalCgroupReconcilers.sandboxContainerLevel {
					condition := r.filter.Filter(podMeta)
					reconcileFn, ok := r.fn[condition]
					if !ok {
						klog.V(5).Infof("calling reconcile function %v aborted for pod %v, condition %s not registered",
							r.description[condition], podMeta.Key(), condition)
						continue
					}
					sandboxContainerCtx := protocol.HooksProtocolBuilder.Sandbox(podMeta)
					start := time.Now()
					if err := reconcileFn(sandboxContainerCtx); err != nil {
						metrics.RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(string(SandboxLevel), resourceType, err, metrics.SinceInSeconds(start))
						klog.Warningf("calling reconcile function %v failed for sandbox %v, error %v",
							r.description[condition], podMeta.Key(), err)
					} else {
						sandboxContainerCtx.ReconcilerDone(c.executor)
						metrics.RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(string(SandboxLevel), resourceType, nil, metrics.SinceInSeconds(start))
						klog.V(5).Infof("calling reconcile function %v for pod sandbox %v finished",
							r.description[condition], podMeta.Key())
					}
				}

				for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
					for resourceType, r := range globalCgroupReconcilers.containerLevel {
						condition := r.filter.Filter(podMeta)
						reconcileFn, ok := r.fn[condition]
						if !ok {
							klog.V(5).Infof("calling reconcile function %v aborted for container %v/%v, condition %s not registered",
								r.description[condition], podMeta.Key(), containerStat.Name, condition)
							continue
						}

						containerCtx := protocol.HooksProtocolBuilder.Container(podMeta, containerStat.Name)
						start := time.Now()
						if err := reconcileFn(containerCtx); err != nil {
							metrics.RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(string(ContainerLevel), resourceType, err, metrics.SinceInSeconds(start))
							klog.Warningf("calling reconcile function %v for container %v/%v failed, error %v",
								r.description[condition], podMeta.Key(), containerStat.Name, err)
						} else {
							containerCtx.ReconcilerDone(c.executor)
							metrics.RecordRuntimeHookReconcilerInvokedDurationMilliSeconds(string(ContainerLevel), resourceType, nil, metrics.SinceInSeconds(start))
							klog.V(5).Infof("calling reconcile function %v for container %v/%v finish",
								r.description[condition], podMeta.Key(), containerStat.Name)
						}
					}
				}

				for _, r := range globalCgroupReconcilers.allPodsLevel {
					currentPods := make([]protocol.HooksProtocol, 0)
					for _, podMeta := range podsMeta {
						if _, ok := r.fn4AllPods[r.filter.Filter(podMeta)]; ok {
							podCtx := protocol.HooksProtocolBuilder.Pod(podMeta)
							currentPods = append(currentPods, podCtx)
						}
					}

					reconcileFn, ok := r.fn4AllPods[r.filter.Name()]
					if !ok {
						klog.V(5).Infof("calling reconcile function %v aborted, condition %s not registered",
							r.description[r.filter.Name()], r.filter.Name())
						continue
					}

					if err := reconcileFn(currentPods); err != nil {
						klog.Warningf("calling reconcile function %v for pod %v failed, error %v",
							r.description[r.filter.Name()], err)
					}
				}
			}

			for _, r := range globalCgroupReconcilers.allPodsLevel {
				currentPods := make([]protocol.HooksProtocol, 0)
				fns := make(map[string]reconcileFunc4AllPods)
				for _, podMeta := range podsMeta {
					key := r.filter.Filter(podMeta)
					if fn, ok := r.fn4AllPods[key]; ok {
						podCtx := protocol.HooksProtocolBuilder.Pod(podMeta)
						currentPods = append(currentPods, podCtx)
						fns[key] = fn
					}
				}

				if len(currentPods) > 0 {
					if len(fns) == 0 {
						klog.V(5).Infof("calling reconcile function %v aborted, condition %s not registered",
							r.description[r.filter.Name()], r.filter.Name())
						continue
					}

					for k, fn := range fns {
						if err := fn(currentPods); err != nil {
							klog.Warningf("calling reconcile function %v for pod %v failed, error %v, condition %s",
								r.description[r.filter.Name()], err, k)
						}
					}
				}
			}

		case <-stopCh:
			klog.V(1).Infof("stop reconcile pod cgroup")
			return
		}
	}
}
