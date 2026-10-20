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

package containercgroup

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/component-base/featuregate"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

// FeatureContainerCgroupOverride is enabled via --qos-extension-plugins, not --feature-gates.
const FeatureContainerCgroupOverride featuregate.Feature = "ContainerCgroupOverride"

var containerCgroupOverrideGVR = schema.GroupVersionResource{
	Group:    "slo.koordinator.sh",
	Version:  "v1alpha1",
	Resource: "containercgroupoverrides",
}

func init() {
	_ = framework.RegisterQOSExtPlugin(
		FeatureContainerCgroupOverride,
		featuregate.FeatureSpec{Default: false, PreRelease: featuregate.Alpha},
		NewPlugin(),
	)
}

// OverrideSpec is annotation JSON. Prefer nested resources; flat fields remain for POC.
type OverrideSpec struct {
	ContainerName string `json:"containerName"`

	Resources *apiext.ContainerCgroupResources `json:"resources,omitempty"`

	MemoryMax string `json:"memoryMax,omitempty"`
	CPUQuota  string `json:"cpuQuota,omitempty"`
	CPUSet    string `json:"cpuset,omitempty"`

	WritebackOnDelete *bool `json:"writebackOnDelete,omitempty"`
}

func (s OverrideSpec) writebackEnabled() bool {
	if s.WritebackOnDelete == nil {
		return true
	}
	return *s.WritebackOnDelete
}

func (s OverrideSpec) resources() apiext.ContainerCgroupResources {
	if s.Resources != nil && !s.Resources.Empty() {
		return *s.Resources.DeepCopy()
	}
	out := apiext.ContainerCgroupResources{}
	if s.MemoryMax != "" {
		out.Memory = &apiext.MemoryCgroupOverride{Max: s.MemoryMax}
	}
	if s.CPUQuota != "" || s.CPUSet != "" {
		out.CPU = &apiext.CPUCgroupOverride{Quota: s.CPUQuota, CPUSet: s.CPUSet}
	}
	return out
}

type targetKey struct {
	id string
}

type activeEntry struct {
	containerName string
	resources     apiext.ContainerCgroupResources
	cgroupParent  string
	crNamespace   string
	crName        string
	desiredHash   string
	fromCR        bool
	writeback     bool
}

type plugin struct {
	statesInformer statesinformer.StatesInformer
	executor       resourceexecutor.ResourceUpdateExecutor
	cgroupReader   resourceexecutor.CgroupReader
	dynClient      dynamic.Interface
	interval       time.Duration

	mu        sync.Mutex
	baselines map[targetKey]apiext.ContainerCgroupResources
	active    map[targetKey]activeEntry
}

func NewPlugin() *plugin {
	return &plugin{
		interval:  2 * time.Second,
		baselines: map[targetKey]apiext.ContainerCgroupResources{},
		active:    map[targetKey]activeEntry{},
	}
}

func (p *plugin) InitFlags(fs *flag.FlagSet) {
	fs.DurationVar(&p.interval, "container-cgroup-override-interval", 2*time.Second,
		"Reconcile interval for ContainerCgroupOverride qos-extension plugin")
}

func (p *plugin) Setup(client clientset.Interface, metricCache metriccache.MetricCache, statesInformer statesinformer.StatesInformer) {
	p.statesInformer = statesInformer
	p.executor = resourceexecutor.NewResourceUpdateExecutor()
	p.cgroupReader = resourceexecutor.NewCgroupReader()
}

func (p *plugin) SetupDynamicClient(dyn dynamic.Interface) {
	p.dynClient = dyn
}

func (p *plugin) Run(stopCh <-chan struct{}) {
	if p.executor != nil {
		p.executor.Run(stopCh)
	}
	klog.Info("ContainerCgroupOverride plugin started (nested resources via NodeSLO.Extensions + annotation fallback)")
	go wait.Until(p.reconcile, p.interval, stopCh)
}

func (p *plugin) reconcile() {
	if p.statesInformer == nil || p.executor == nil || p.cgroupReader == nil {
		return
	}
	pods := p.statesInformer.GetAllPods()
	podByUID := map[string]*statesinformer.PodMeta{}
	podByNSName := map[string]*statesinformer.PodMeta{}
	for _, pm := range pods {
		if pm == nil || pm.Pod == nil {
			continue
		}
		podByUID[string(pm.Pod.UID)] = pm
		podByNSName[pm.Pod.Namespace+"/"+pm.Pod.Name] = pm
	}

	seen := map[targetKey]struct{}{}
	crOwned := map[string]struct{}{}

	if nodeSLO := p.statesInformer.GetNodeSLO(); nodeSLO != nil && nodeSLO.Spec.Extensions != nil && nodeSLO.Spec.Extensions.Object != nil {
		raw := nodeSLO.Spec.Extensions.Object[apiext.ExtensionContainerCgroupOverrides]
		overrides, err := apiext.ParseNodeCgroupOverrides(raw)
		if err != nil {
			klog.V(4).Infof("parse NodeSLO containerCgroupOverrides failed: %v", err)
		} else {
			for i := range overrides.Items {
				item := &overrides.Items[i]
				key := targetKey{id: "cr/" + item.Namespace + "/" + item.Name}
				seen[key] = struct{}{}
				podMeta := podByUID[item.PodUID]
				if podMeta == nil {
					podMeta = podByNSName[item.PodNamespace+"/"+item.PodName]
				}
				if podMeta == nil {
					klog.V(5).Infof("cgroup-override CR %s/%s: pod not on node yet", item.Namespace, item.Name)
					continue
				}
				crOwned[string(podMeta.Pod.UID)+"/"+item.ContainerName] = struct{}{}
				if isTruthy(podMeta.Pod.Annotations[apiext.AnnotationContainerCgroupOverrideSkip]) {
					continue
				}
				if err := p.applyExtensionItem(podMeta, item, key); err != nil {
					klog.Infof("apply cgroup-override CR %s/%s err=%v", item.Namespace, item.Name, err)
				}
			}
		}
	}

	for _, podMeta := range pods {
		if podMeta == nil || podMeta.Pod == nil {
			continue
		}
		pod := podMeta.Pod
		if isTruthy(pod.Annotations[apiext.AnnotationContainerCgroupOverrideSkip]) {
			continue
		}
		raw, ok := pod.Annotations[apiext.AnnotationContainerCgroupOverride]
		if !ok || raw == "" {
			continue
		}
		specs, err := ParseOverrideSpecs(raw)
		if err != nil {
			klog.V(4).Infof("pod %s/%s invalid cgroup-override: %v", pod.Namespace, pod.Name, err)
			continue
		}
		for _, spec := range specs {
			if _, owned := crOwned[string(pod.UID)+"/"+spec.ContainerName]; owned {
				continue
			}
			key := targetKey{id: "ann/" + string(pod.UID) + "/" + spec.ContainerName}
			seen[key] = struct{}{}
			if err := p.applyNamed(podMeta, spec.ContainerName, spec.resources(), key, "", "", "", nil, false, spec.writebackEnabled()); err != nil {
				klog.Infof("apply cgroup-override pod=%s/%s container=%s err=%v",
					pod.Namespace, pod.Name, spec.ContainerName, err)
			}
		}
	}

	p.writebackMissing(seen)
}

func (p *plugin) applyExtensionItem(podMeta *statesinformer.PodMeta, item *apiext.NodeCgroupOverrideItem, key targetKey) error {
	if item.PendingDelete {
		return p.handlePendingDelete(podMeta, item, key)
	}
	return p.applyNamed(podMeta, item.ContainerName, item.Resources, key,
		item.Namespace, item.Name, item.DesiredHash, item.Baseline, true, item.WritebackOnDelete)
}

func (p *plugin) handlePendingDelete(podMeta *statesinformer.PodMeta, item *apiext.NodeCgroupOverrideItem, key targetKey) error {
	containerDir, err := p.containerDir(podMeta, item.ContainerName)
	if err != nil {
		return err
	}
	bl := p.resolveBaseline(key, item.Baseline)
	if item.WritebackOnDelete {
		if err := p.writeback(containerDir, item.Resources, bl); err != nil {
			return err
		}
		klog.Infof("cgroup-override writeback (pendingDelete) CR=%s/%s container=%s",
			item.Namespace, item.Name, item.ContainerName)
	}
	if err := p.patchCRStatus(item.Namespace, item.Name, map[string]interface{}{
		"phase":   string(apiext.CgroupOverridePhaseReleased),
		"message": "writeback completed",
	}); err != nil {
		klog.Infof("patch Released status CR=%s/%s err=%v", item.Namespace, item.Name, err)
	}
	p.mu.Lock()
	delete(p.active, key)
	delete(p.baselines, key)
	p.mu.Unlock()
	return nil
}

func (p *plugin) applyNamed(podMeta *statesinformer.PodMeta, containerName string, res apiext.ContainerCgroupResources,
	key targetKey, crNS, crName, desiredHash string, extBaseline *apiext.ContainerCgroupResources, fromCR, writeback bool) error {
	if res.Empty() {
		return fmt.Errorf("empty resources")
	}
	pod := podMeta.Pod
	containerDir, err := p.containerDir(podMeta, containerName)
	if err != nil {
		return err
	}

	p.mu.Lock()
	if _, ok := p.baselines[key]; !ok {
		bl := apiext.ContainerCgroupResources{}
		if extBaseline != nil && !extBaseline.Empty() {
			bl = *extBaseline.DeepCopy()
		} else {
			if res.MemoryMax() != "" {
				if v, rerr := p.cgroupReader.ReadMemoryLimit(containerDir); rerr == nil {
					bl.Memory = &apiext.MemoryCgroupOverride{Max: formatMemoryLimitForWrite(v)}
				}
			}
			if res.CPUQuota() != "" {
				if q, rerr := p.cgroupReader.ReadCPUQuota(containerDir); rerr == nil {
					bl.CPU = &apiext.CPUCgroupOverride{Quota: strconv.FormatInt(q, 10)}
				}
			}
			if res.CPUSet() != "" {
				// baseline for cpuset not always readable; leave empty if unsupported
				if bl.CPU == nil {
					bl.CPU = &apiext.CPUCgroupOverride{}
				}
			}
			if fromCR && crNS != "" && crName != "" {
				_ = p.patchCRStatus(crNS, crName, map[string]interface{}{
					"phase":               string(apiext.CgroupOverridePhaseApplied),
					"baseline":            baselineToUnstructured(&bl),
					"observedDesiredHash": desiredHash,
					"message":             "baseline captured",
				})
			}
			klog.Infof("cgroup-override baseline captured pod=%s/%s container=%s memory=%q cpuQuota=%q",
				pod.Namespace, pod.Name, containerName, bl.MemoryMax(), bl.CPUQuota())
		}
		p.baselines[key] = bl
	}
	p.active[key] = activeEntry{
		containerName: containerName,
		resources:     res,
		cgroupParent:  containerDir,
		crNamespace:   crNS,
		crName:        crName,
		desiredHash:   desiredHash,
		fromCR:        fromCR,
		writeback:     writeback,
	}
	p.mu.Unlock()

	changed := false
	if max := res.MemoryMax(); max != "" {
		val, err := MemoryMaxToCgroupValue(max)
		if err != nil {
			return err
		}
		if !p.memoryAlreadyDesired(containerDir, val) {
			updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.MemoryLimitName, containerDir, val, nil)
			if err != nil {
				return err
			}
			if _, err := p.executor.Update(false, updater); err != nil {
				return fmt.Errorf("update memory limit: %w", err)
			}
			changed = true
			klog.Infof("cgroup-override applied memory pod=%s/%s container=%s value=%s",
				pod.Namespace, pod.Name, containerName, val)
		}
	}
	if quota := res.CPUQuota(); quota != "" {
		period := system.CFSBasePeriodValue
		if res.CPU != nil && res.CPU.Period != nil && *res.CPU.Period > 0 {
			period = *res.CPU.Period
		} else if per, rerr := p.cgroupReader.ReadCPUPeriod(containerDir); rerr == nil && per > 0 {
			period = per
		}
		quotaVal, err := CPUQuotaToCgroupValueWithPeriod(quota, period)
		if err != nil {
			return err
		}
		if !p.cpuAlreadyDesired(containerDir, quotaVal) {
			updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.CPUCFSQuotaName, containerDir, quotaVal, nil)
			if err != nil {
				return err
			}
			if _, err := p.executor.Update(false, updater); err != nil {
				return fmt.Errorf("update cpu quota: %w", err)
			}
			changed = true
			klog.Infof("cgroup-override applied cpuQuota pod=%s/%s container=%s value=%s period=%d",
				pod.Namespace, pod.Name, containerName, quotaVal, period)
		}
	}
	if cpuset := res.CPUSet(); cpuset != "" {
		updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.CPUSetCPUSName, containerDir, cpuset, nil)
		if err != nil {
			return err
		}
		if _, err := p.executor.Update(false, updater); err != nil {
			return fmt.Errorf("update cpuset: %w", err)
		}
		changed = true
		klog.Infof("cgroup-override applied cpuset pod=%s/%s container=%s value=%s",
			pod.Namespace, pod.Name, containerName, cpuset)
	}

	if fromCR && crNS != "" && crName != "" && changed {
		now := time.Now().UTC().Format(time.RFC3339)
		_ = p.patchCRStatus(crNS, crName, map[string]interface{}{
			"phase":               string(apiext.CgroupOverridePhaseApplied),
			"observedDesiredHash": desiredHash,
			"lastAppliedTime":     now,
			"message":             "applied",
		})
	}
	return nil
}

func (p *plugin) containerDir(podMeta *statesinformer.PodMeta, containerName string) (string, error) {
	pod := podMeta.Pod
	var containerStatus *corev1.ContainerStatus
	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.Name == containerName {
			containerStatus = cs
			break
		}
	}
	if containerStatus == nil {
		return "", fmt.Errorf("container %s not found in status", containerName)
	}
	return koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, containerStatus)
}

func (p *plugin) resolveBaseline(key targetKey, ext *apiext.ContainerCgroupResources) apiext.ContainerCgroupResources {
	p.mu.Lock()
	defer p.mu.Unlock()
	if bl, ok := p.baselines[key]; ok {
		return bl
	}
	if ext != nil {
		return *ext.DeepCopy()
	}
	return apiext.ContainerCgroupResources{}
}

func (p *plugin) memoryAlreadyDesired(containerDir, desired string) bool {
	v, err := p.cgroupReader.ReadMemoryLimit(containerDir)
	if err != nil {
		return false
	}
	return formatMemoryLimitForWrite(v) == desired || strconv.FormatInt(v, 10) == desired
}

func (p *plugin) cpuAlreadyDesired(containerDir, desired string) bool {
	q, err := p.cgroupReader.ReadCPUQuota(containerDir)
	if err != nil {
		return false
	}
	return strconv.FormatInt(q, 10) == desired
}

func baselineToUnstructured(bl *apiext.ContainerCgroupResources) map[string]interface{} {
	if bl == nil {
		return nil
	}
	b, err := json.Marshal(bl)
	if err != nil {
		return nil
	}
	out := map[string]interface{}{}
	_ = json.Unmarshal(b, &out)
	return out
}

func (p *plugin) patchCRStatus(ns, name string, fields map[string]interface{}) error {
	if p.dynClient == nil || ns == "" || name == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	obj, err := p.dynClient.Resource(containerCgroupOverrideGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	status, ok, _ := unstructured.NestedMap(obj.Object, "status")
	if !ok || status == nil {
		status = map[string]interface{}{}
	}
	for k, v := range fields {
		status[k] = v
	}
	if err := unstructured.SetNestedMap(obj.Object, status, "status"); err != nil {
		return err
	}
	_, err = p.dynClient.Resource(containerCgroupOverrideGVR).Namespace(ns).UpdateStatus(ctx, obj, metav1.UpdateOptions{})
	return err
}

func (p *plugin) writebackMissing(seen map[targetKey]struct{}) {
	type pending struct {
		key   targetKey
		entry activeEntry
		base  apiext.ContainerCgroupResources
	}
	var toWB []pending

	p.mu.Lock()
	for key, entry := range p.active {
		if _, ok := seen[key]; ok {
			continue
		}
		if !entry.writeback {
			klog.Infof("cgroup-override dropped without writeback id=%s container=%s",
				key.id, entry.containerName)
			delete(p.active, key)
			delete(p.baselines, key)
			continue
		}
		toWB = append(toWB, pending{key: key, entry: entry, base: p.baselines[key]})
	}
	p.mu.Unlock()

	for _, item := range toWB {
		if err := p.writeback(item.entry.cgroupParent, item.entry.resources, item.base); err != nil {
			klog.Infof("writeback cgroup-override failed id=%s container=%s err=%v",
				item.key.id, item.entry.containerName, err)
			continue
		}
		klog.Infof("cgroup-override writeback ok id=%s container=%s memory=%q cpuQuota=%q",
			item.key.id, item.entry.containerName, item.base.MemoryMax(), item.base.CPUQuota())
		p.mu.Lock()
		delete(p.active, item.key)
		delete(p.baselines, item.key)
		p.mu.Unlock()
	}
}

func (p *plugin) writeback(containerDir string, desired, bl apiext.ContainerCgroupResources) error {
	if containerDir == "" {
		return fmt.Errorf("empty cgroup parent")
	}
	if desired.MemoryMax() != "" && bl.MemoryMax() != "" {
		updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.MemoryLimitName, containerDir, bl.MemoryMax(), nil)
		if err != nil {
			return err
		}
		if _, err := p.executor.Update(false, updater); err != nil {
			return err
		}
	}
	if desired.CPUQuota() != "" && bl.CPUQuota() != "" {
		updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.CPUCFSQuotaName, containerDir, bl.CPUQuota(), nil)
		if err != nil {
			return err
		}
		if _, err := p.executor.Update(false, updater); err != nil {
			return err
		}
	}
	if desired.CPUSet() != "" && bl.CPUSet() != "" {
		updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.CPUSetCPUSName, containerDir, bl.CPUSet(), nil)
		if err != nil {
			return err
		}
		if _, err := p.executor.Update(false, updater); err != nil {
			return err
		}
	}
	return nil
}

// ParseOverrideSpecs accepts a single object or a JSON array.
func ParseOverrideSpecs(raw string) ([]OverrideSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty override")
	}
	if raw[0] == '[' {
		var specs []OverrideSpec
		if err := json.Unmarshal([]byte(raw), &specs); err != nil {
			return nil, err
		}
		if len(specs) == 0 {
			return nil, fmt.Errorf("empty override array")
		}
		for i := range specs {
			if err := validateSpec(specs[i]); err != nil {
				return nil, fmt.Errorf("index %d: %w", i, err)
			}
		}
		return specs, nil
	}
	spec, err := ParseOverrideSpec(raw)
	if err != nil {
		return nil, err
	}
	return []OverrideSpec{spec}, nil
}

// ParseOverrideSpec parses a single annotation JSON object.
func ParseOverrideSpec(raw string) (OverrideSpec, error) {
	var spec OverrideSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return OverrideSpec{}, err
	}
	if err := validateSpec(spec); err != nil {
		return OverrideSpec{}, err
	}
	return spec, nil
}

func validateSpec(spec OverrideSpec) error {
	if spec.ContainerName == "" {
		return fmt.Errorf("containerName is required")
	}
	if spec.resources().Empty() {
		return fmt.Errorf("resources (or memoryMax/cpuQuota) is required")
	}
	return nil
}

// MemoryMaxToCgroupValue converts quantity or "max" to cgroup file content.
func MemoryMaxToCgroupValue(s string) (string, error) {
	if s == "max" {
		if system.GetCurrentCgroupVersion() == system.CgroupVersionV2 {
			return "max", nil
		}
		return "-1", nil
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(q.Value(), 10), nil
}

// CPUQuotaToCgroupValue converts core quantity to cfs_quota_us with default period.
func CPUQuotaToCgroupValue(s string) (string, error) {
	return CPUQuotaToCgroupValueWithPeriod(s, system.CFSBasePeriodValue)
}

// CPUQuotaToCgroupValueWithPeriod uses the container's cfs period.
func CPUQuotaToCgroupValueWithPeriod(s string, period int64) (string, error) {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return "", err
	}
	if period <= 0 {
		period = system.CFSBasePeriodValue
	}
	milli := q.MilliValue()
	quota := milli * period / 1000
	if quota <= 0 {
		return "-1", nil
	}
	if quota < system.CFSQuotaMinValue {
		quota = system.CFSQuotaMinValue
	}
	return strconv.FormatInt(quota, 10), nil
}

func formatMemoryLimitForWrite(v int64) string {
	if v < 0 {
		if system.GetCurrentCgroupVersion() == system.CgroupVersionV2 {
			return "max"
		}
		return "-1"
	}
	return strconv.FormatInt(v, 10)
}

func isTruthy(v string) bool {
	return v == "true" || v == "1" || v == "TRUE"
}
