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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

const (
	kubeQOSReconcileSeconds = 10
)

type ReconcilerLevel string

const (
	KubeQOSLevel   ReconcilerLevel = "kubeqos"
	PodLevel       ReconcilerLevel = "pod"
	ContainerLevel ReconcilerLevel = "container"
)

// map[string]*cgroupReconciler: key is cgroup filename,
var globalCgroupReconciler = map[ReconcilerLevel]map[string]*cgroupReconciler{
	KubeQOSLevel:   {},
	PodLevel:       {},
	ContainerLevel: {},
}

type cgroupReconciler struct {
	cgroupFile  system.CgroupFile
	fn          reconcileFunc
	description string
}

type reconcileFunc func(protocol.HooksProtocol) error

func RegisterCgroupReconciler(level ReconcilerLevel, cgroupFile system.CgroupFile,
	fn reconcileFunc, description string) {
	if _, ok := globalCgroupReconciler[level]; !ok {
		klog.Fatalf("resource level %v has not init", level)
	}
	if c, exist := globalCgroupReconciler[level][cgroupFile.ResourceFileName]; exist {
		klog.Fatalf("%v already registered by %v", cgroupFile.ResourceFileName, c.description)
	}
	globalCgroupReconciler[level][cgroupFile.ResourceFileName] = &cgroupReconciler{
		cgroupFile:  cgroupFile,
		fn:          fn,
		description: description,
	}
	klog.V(1).Infof("register reconcile function %v finished, detailed info: level=%v, filename=%v",
		description, level, cgroupFile.ResourceFileName)
}

type Reconciler interface {
	Run(stopCh <-chan struct{}) error
}

func NewReconciler(s statesinformer.StatesInformer) Reconciler {
	r := &reconciler{
		podUpdated: make(chan struct{}, 1),
	}
	// TODO register individual pod event
	s.RegisterCallbacks(statesinformer.RegisterTypeAllPods, "runtime-hooks-reconciler",
		"Reconcile cgroup files if pod updated", r.podRefreshCallback)
	return r
}

type reconciler struct {
	podsMutex  sync.RWMutex
	podsMeta   []*statesinformer.PodMeta
	podUpdated chan struct{}
}

func (c *reconciler) Run(stopCh <-chan struct{}) error {
	go c.reconcilePodCgroup(stopCh)
	go c.reconcileKubeQOSCgroup(stopCh)
	klog.V(1).Infof("start runtime hook reconciler successfully")
	return nil
}

func (c *reconciler) podRefreshCallback(t statesinformer.RegisterType, o interface{},
	podsMeta []*statesinformer.PodMeta) {
	c.podsMutex.Lock()
	defer c.podsMutex.Unlock()
	c.podsMeta = podsMeta
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
	duration := time.Duration(kubeQOSReconcileSeconds) * time.Second
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			doKubeQOSCgroup()
			timer.Reset(duration)
		case <-stopCh:
			klog.V(1).Infof("stop reconcile kube qos cgroup")
		}
	}
}

func doKubeQOSCgroup() {
	for _, kubeQOS := range []corev1.PodQOSClass{
		corev1.PodQOSGuaranteed, corev1.PodQOSBurstable, corev1.PodQOSBestEffort} {
		for _, r := range globalCgroupReconciler[KubeQOSLevel] {
			kubeQOSCtx := protocol.HooksProtocolBuilder.KubeQOS(kubeQOS)
			if err := r.fn(kubeQOSCtx); err != nil {
				klog.Warningf("calling reconcile function %v failed, error %v", r.description, err)
			} else {
				kubeQOSCtx.ReconcilerDone()
				klog.V(5).Infof("calling reconcile function %v for kube qos %v finish",
					r.description, kubeQOS)
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
				for _, r := range globalCgroupReconciler[PodLevel] {
					podCtx := protocol.HooksProtocolBuilder.Pod(podMeta)
					if err := r.fn(podCtx); err != nil {
						klog.Warningf("calling reconcile function %v failed, error %v", r.description, err)
					} else {
						podCtx.ReconcilerDone()
						klog.V(5).Infof("calling reconcile function %v for pod %v finished",
							r.description, util.GetPodKey(podMeta.Pod))
					}
				}
				for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
					for _, r := range globalCgroupReconciler[ContainerLevel] {
						containerCtx := protocol.HooksProtocolBuilder.Container(
							podMeta, containerStat.Name)
						if err := r.fn(containerCtx); err != nil {
							klog.Warningf("calling reconcile function %v failed, error %v", r.description, err)
						} else {
							containerCtx.ReconcilerDone()
							klog.V(5).Infof("calling reconcile function %v for container %v/%v finish",
								r.description, util.GetPodKey(podMeta.Pod), containerStat.Name)
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
