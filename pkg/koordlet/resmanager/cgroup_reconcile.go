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

package resmanager

import (
	"math"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

const (
	CgroupResourcesReconcileForceUpdateSeconds int = 60
)

type CgroupResourcesReconcile struct {
	resmanager *resmanager
	executor   *LeveledResourceUpdateExecutor
}

// cgroupResourceSummary summarizes values of cgroup resources to update; nil value means not to update
type cgroupResourceSummary struct {
	// Memory
	memoryMin              *int64
	memoryLow              *int64
	memoryHigh             *int64
	memoryWmarkRatio       *int64
	memoryWmarkScaleFactor *int64
	memoryWmarkMinAdj      *int64
	memoryUsePriorityOom   *int64
	memoryPriority         *int64
	memoryOomKillGroup     *int64
}

func NewCgroupResourcesReconcile(resmanager *resmanager) *CgroupResourcesReconcile {
	executor := NewLeveledResourceUpdateExecutor("CgroupResourcesExecutor", CgroupResourcesReconcileForceUpdateSeconds)
	return &CgroupResourcesReconcile{
		resmanager: resmanager,
		executor:   executor,
	}
}

func (m *CgroupResourcesReconcile) RunInit(stopCh <-chan struct{}) error {
	m.executor.Run(stopCh)
	return nil
}

func (m *CgroupResourcesReconcile) reconcile() {
	nodeSLO := m.resmanager.getNodeSLOCopy()
	if nodeSLO == nil || nodeSLO.Spec.ResourceQoSStrategy == nil {
		// do nothing if nodeSLO == nil || nodeSLO.Spec.ResourceQoSStrategy == nil
		klog.Warning("nodeSLO or nodeSLO.Spec.ResourceQoSStrategy is nil %v", util.DumpJSON(nodeSLO))
		return
	}

	// apply CgroupReconcile: calculate resources to update, and then update them by a leveled order to avoid dynamic
	// resource overcommitment/leak
	m.calculateAndUpdateResources(nodeSLO)
	klog.V(5).Infof("finish reconciling Cgroups!")
}

func (m *CgroupResourcesReconcile) calculateAndUpdateResources(nodeSLO *slov1alpha1.NodeSLO) {
	// 1. sort cgroup resources by the owner level (qos, pod, container).
	//    e.g. for hierarchical resources of memoryMin, when qos-level memoryMin increases, they should be updated from
	//         the top to bottom; while resources should be updated from the bottom to top when qos-level memoryMin
	//         decreases to avoid higher-level's over-commit.
	// 2. update resources in level order
	if m.resmanager == nil || m.resmanager.statesInformer == nil {
		klog.Errorf("failed to calculate cgroup resources, err: reconcile uninitialized, resmgr %v", m.resmanager)
		return
	}
	node := m.resmanager.statesInformer.GetNode()
	if node == nil || node.Status.Allocatable == nil {
		klog.Errorf("failed to calculate resources, err: node is invalid: %v", util.DumpJSON(node))
		return
	}
	podMetas := m.resmanager.statesInformer.GetAllPods()

	// calculate qos-level, pod-level and container-level resources
	qosResources, podResources, containerResources := m.calculateResources(nodeSLO.Spec.ResourceQoSStrategy, node, podMetas)

	// to make sure the hierarchical cgroup resources are correctly updated, we simply update the resources by
	// cgroup-level order.
	// e.g. /kubepods.slice/memory.min, /kubepods.slice-podxxx/memory.min, /kubepods.slice-podxxx/docker-yyy/memory.min
	leveledResources := [][]MergeableResourceUpdater{qosResources, podResources, containerResources}
	updated := m.executor.LeveledUpdateBatchByCache(leveledResources)
	if updated {
		klog.V(5).Info("cgroup resources is exactly updated")
	}
}

// calculateResources calculates qos-level, pod-level and container-level resources with nodeCfg and podMetas
func (m *CgroupResourcesReconcile) calculateResources(nodeCfg *slov1alpha1.ResourceQoSStrategy, node *corev1.Node,
	podMetas []*statesinformer.PodMeta) (qosLevelResources, podLevelResources, containerLevelResources []MergeableResourceUpdater) {
	// TODO: check anolis os version
	qosSummary := map[corev1.PodQOSClass]*cgroupResourceSummary{
		corev1.PodQOSGuaranteed: {},
		corev1.PodQOSBurstable:  {},
		corev1.PodQOSBestEffort: {},
	}

	for _, podMeta := range podMetas {
		pod := podMeta.Pod
		// ignore non-running pods
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			klog.V(5).Infof("skip calculate cgroup summary for non-running pod %s", util.GetPodKey(pod))
			continue
		}

		// retrieve pod-level config
		kubeQoS := util.GetKubeQosClass(pod) // assert kubeQoS belongs to {Guaranteed, Burstable, Besteffort}
		podQoSCfg := getPodResourceQoSByQoSClass(pod, nodeCfg, m.resmanager.config)
		mergedPodCfg, err := m.getMergedPodResourceQoS(pod, podQoSCfg)
		if err != nil {
			klog.Errorf("failed to retrieve pod resourceQoS, err: %v", err)
			continue
		}

		// update summary for qos resources
		updateCgroupSummaryForQoS(qosSummary[kubeQoS], pod, mergedPodCfg)

		// calculate pod-level and container-level resources and make resourceUpdaters
		podResources, containerResources := m.calculatePodAndContainerResources(podMeta, node, mergedPodCfg)
		podLevelResources = append(podLevelResources, podResources...)
		containerLevelResources = append(containerLevelResources, containerResources...)
	}
	// summarize qos-level resources
	completeCgroupSummaryForQoS(qosSummary)

	// calculate qos-level resources with the qos summary
	// NOTE: first visit Guaranteed since it actually has a higher level cgroup than others'
	for _, kubeQoS := range []corev1.PodQOSClass{corev1.PodQOSGuaranteed, corev1.PodQOSBurstable, corev1.PodQOSBestEffort} {
		qosCfg := getKubeQoSResourceQoSByQoSClass(kubeQoS, nodeCfg, m.resmanager.config)
		// make qos resourceUpdaters
		qosResources := m.calculateQoSResources(qosSummary[kubeQoS], kubeQoS, qosCfg)
		qosLevelResources = append(qosLevelResources, qosResources...)
	}

	return
}

func (m *CgroupResourcesReconcile) calculateQoSResources(summary *cgroupResourceSummary, qos corev1.PodQOSClass,
	qosCfg *slov1alpha1.ResourceQoS) []MergeableResourceUpdater {
	// double-check qosCfg is not nil
	if qosCfg == nil {
		klog.Warningf("calculateQoSResources aborts since qos config is %v", qosCfg)
		return nil
	}

	qosDir := util.GetKubeQosRelativePath(qos)

	// Mem QoS
	if qosCfg.MemoryQoS != nil {
		summary.memoryUsePriorityOom = qosCfg.MemoryQoS.PriorityEnable
		summary.memoryPriority = qosCfg.MemoryQoS.Priority
		summary.memoryOomKillGroup = qosCfg.MemoryQoS.OomKillGroup
	}

	return makeCgroupResources(GroupOwnerRef(string(qos)), qosDir, summary)
}

func (m *CgroupResourcesReconcile) calculatePodAndContainerResources(podMeta *statesinformer.PodMeta, node *corev1.Node,
	podCfg *slov1alpha1.ResourceQoS) (podResources, containerResources []MergeableResourceUpdater) {
	pod := podMeta.Pod
	podDir := util.GetPodCgroupDirWithKube(podMeta.CgroupDir)

	podResources = m.calculatePodResources(pod, podDir, podCfg)

	for _, container := range pod.Spec.Containers {
		_, containerStatus, err := util.FindContainerIdAndStatusByName(&pod.Status, container.Name)
		if err != nil {
			klog.Warningf("failed to find containerStatus, pod %s, container %s, err: %v", util.GetPodKey(pod),
				container.Name, err)
			continue
		}
		containerDir, err := util.GetContainerCgroupPathWithKube(podMeta.CgroupDir, containerStatus)
		if err != nil {
			klog.Warningf("parse containerDir error! msg: %v", err)
			continue
		}

		curContainerResources := m.calculateContainerResources(&container, pod, node, containerDir, podCfg)
		containerResources = append(containerResources, curContainerResources...)
	}

	return
}

func (m *CgroupResourcesReconcile) calculatePodResources(pod *corev1.Pod, parentDir string, podCfg *slov1alpha1.ResourceQoS) []MergeableResourceUpdater {
	// double-check qos config is not nil
	if podCfg == nil {
		klog.V(5).Infof("calculatePodResources aborts since pod-level config is empty, cfg: %v", podCfg)
		return nil
	}
	summary := &cgroupResourceSummary{}

	// Mem QoS
	// resources statically use configured values
	if podCfg.MemoryQoS != nil {
		summary.memoryWmarkRatio = podCfg.MemoryQoS.WmarkRatio
		summary.memoryWmarkScaleFactor = podCfg.MemoryQoS.WmarkScalePermill
		summary.memoryWmarkMinAdj = podCfg.MemoryQoS.WmarkMinAdj
		summary.memoryUsePriorityOom = podCfg.MemoryQoS.PriorityEnable
		summary.memoryPriority = podCfg.MemoryQoS.Priority
		summary.memoryOomKillGroup = podCfg.MemoryQoS.OomKillGroup
		// resources calculated with pod spec
		var memRequest int64
		// memory.min, memory.low: just sum all containers' memory requests; regard as no memory protection when any
		// of containers does not set request
		if apiext.GetPodQoSClass(pod) != apiext.QoSBE {
			podRequest := util.GetPodRequest(pod)
			memRequest = podRequest.Memory().Value()
		} else {
			memRequest = util.GetPodBEMemoryByteRequestIgnoreUnlimited(pod)
		}
		if podCfg.MemoryQoS.MinLimitPercent != nil {
			// assert no overflow for request < 1PiB
			summary.memoryMin = pointer.Int64Ptr(memRequest * (*podCfg.MemoryQoS.MinLimitPercent) / 100)
		}
		if podCfg.MemoryQoS.LowLimitPercent != nil {
			summary.memoryLow = pointer.Int64Ptr(memRequest * (*podCfg.MemoryQoS.LowLimitPercent) / 100)
		}
		// values improved: memory.low is no less than memory.min
		if summary.memoryMin != nil && summary.memoryLow != nil && *summary.memoryLow > 0 &&
			*summary.memoryLow < *summary.memoryMin {
			*summary.memoryLow = *summary.memoryMin
			klog.V(5).Infof("correct calculated memory.low for pod since it is lower than memory.min, "+
				"pod %s, current value %v", util.GetPodKey(pod), summary.memoryLow)
		}
	}

	return makeCgroupResources(PodOwnerRef(pod.Namespace, pod.Name), parentDir, summary)
}

func (m *CgroupResourcesReconcile) calculateContainerResources(container *corev1.Container, pod *corev1.Pod,
	node *corev1.Node, parentDir string, podCfg *slov1alpha1.ResourceQoS) []MergeableResourceUpdater {
	// double-check qos config is not nil
	if podCfg == nil {
		klog.V(5).Infof("calculateContainerResources aborts since pod-level config is empty, cfg: %v", podCfg)
		return nil
	}
	summary := &cgroupResourceSummary{}

	// Mem QoS
	// resources statically use configured values
	if podCfg.MemoryQoS != nil {
		summary.memoryWmarkRatio = podCfg.MemoryQoS.WmarkRatio
		summary.memoryWmarkScaleFactor = podCfg.MemoryQoS.WmarkScalePermill
		summary.memoryWmarkMinAdj = podCfg.MemoryQoS.WmarkMinAdj
		summary.memoryUsePriorityOom = podCfg.MemoryQoS.PriorityEnable
		summary.memoryPriority = podCfg.MemoryQoS.Priority
		summary.memoryOomKillGroup = podCfg.MemoryQoS.OomKillGroup
		// resources calculated with container spec
		var memRequest int64
		var memLimit int64
		if apiext.GetPodQoSClass(pod) != apiext.QoSBE {
			memRequest = container.Resources.Requests.Memory().Value()
			memLimit = util.GetContainerMemoryByteLimit(container)
		} else {
			memRequest = util.GetContainerBEMemoryByteRequest(container)
			memLimit = util.GetContainerBEMemoryByteLimit(container)
		}
		if memRequest < 0 {
			// when container request not set, memory request is counted as zero but not unlimited(-1)
			memRequest = 0
		}
		// memory.min, memory.low: if container's memory request is not set, just consider it as zero
		if podCfg.MemoryQoS.MinLimitPercent != nil {
			summary.memoryMin = pointer.Int64Ptr(memRequest * (*podCfg.MemoryQoS.MinLimitPercent) / 100)
		}
		if podCfg.MemoryQoS.LowLimitPercent != nil {
			summary.memoryLow = pointer.Int64Ptr(memRequest * (*podCfg.MemoryQoS.LowLimitPercent) / 100)
		}
		// memory.high: if container's memory throttling factor is set as zero, disable memory.high by set to maximal;
		// else if factor is set while container's limit not set, set memory.high with node memory allocatable
		if podCfg.MemoryQoS.ThrottlingPercent != nil {
			if *podCfg.MemoryQoS.ThrottlingPercent == 0 { // reset to system default if set 0
				summary.memoryHigh = pointer.Int64Ptr(math.MaxInt64) // writing MaxInt64 is equal to write "max"
			} else if memLimit > 0 {
				summary.memoryHigh = pointer.Int64Ptr(memLimit * (*podCfg.MemoryQoS.ThrottlingPercent) / 100)
			} else {
				nodeLimit := node.Status.Allocatable.Memory().Value()
				summary.memoryHigh = pointer.Int64Ptr(nodeLimit * (*podCfg.MemoryQoS.ThrottlingPercent) / 100)
			}
		}
		// values improved: memory.low is no less than memory.min
		if summary.memoryMin != nil && summary.memoryLow != nil && *summary.memoryLow > 0 &&
			*summary.memoryLow < *summary.memoryMin {
			*summary.memoryLow = *summary.memoryMin
			klog.V(5).Infof("correct calculated memory.low for container since it is lower than memory.min,"+
				" pod %s, container %s, current value %v", util.GetPodKey(pod), container.Name, *summary.memoryLow)
		}
		// values improved: memory.high is no less than memory.min
		if summary.memoryHigh != nil && summary.memoryMin != nil && *summary.memoryHigh > 0 &&
			*summary.memoryHigh < *summary.memoryMin {
			*summary.memoryHigh = *summary.memoryMin
			klog.V(5).Infof("correct calculated memory.high for container since it is lower than memory.min,"+
				" pod %s, container %s, current value %v", util.GetPodKey(pod), container.Name, *summary.memoryHigh)
		}
	}

	return makeCgroupResources(ContainerOwnerRef(pod.Namespace, pod.Name, container.Name), parentDir, summary)
}

// getMergedPodResourceQoS returns a merged ResourceQoS for the pod (i.e. a pod-level qos config).
// 1. merge pod-level cfg with node-level cfg if pod annotation of advanced qos config exists;
// 2. calculates and finally returns the pod-level cfg with each feature cfg (e.g. pod-level memory qos config).
func (m *CgroupResourcesReconcile) getMergedPodResourceQoS(pod *corev1.Pod, cfg *slov1alpha1.ResourceQoS) (*slov1alpha1.ResourceQoS, error) {
	// deep-copy node config into pod config; assert cfg == NoneResourceQoS when node disables
	mergedCfg := cfg.DeepCopy()

	// update with memory qos config
	m.mergePodResourceQoSForMemoryQoS(pod, mergedCfg)

	klog.V(5).Infof("get merged pod ResourceQoS %v for pod %s", util.DumpJSON(mergedCfg), util.GetPodKey(pod))
	return mergedCfg, nil
}

// mergePodResourceQoSForMemoryQoS merges pod-level memory qos config with node-level resource qos config
// config overwrite: pod-level config > pod policy template > node-level config
func (m *CgroupResourcesReconcile) mergePodResourceQoSForMemoryQoS(pod *corev1.Pod, cfg *slov1alpha1.ResourceQoS) {
	// get the pod-level config and determine if the pod is allowed
	// TODO: support namespaced switch
	if cfg.MemoryQoS == nil {
		cfg.MemoryQoS = &slov1alpha1.MemoryQoSCfg{}
	}
	policy := slov1alpha1.PodMemoryQoSPolicyDefault

	// get pod-level config
	podCfg, err := apiext.GetPodMemoryQoSConfig(pod)
	if err != nil { // ignore pod-level memory qos config when parse error
		klog.Errorf("failed to parse memory qos config, pod %s, err: %s", util.GetPodKey(pod), err)
		podCfg = nil
	}
	if podCfg != nil {
		policy = podCfg.Policy // policy="" is equal to policy="default"
	}
	klog.V(5).Infof("memory qos podPolicy=%s for pod %s", policy, util.GetPodKey(pod))

	// if policy is not default, replace memory qos config with the policy template
	if policy == slov1alpha1.PodMemoryQoSPolicyNone { // fully disable memory qos for policy=None
		cfg.MemoryQoS.MemoryQoS = *util.NoneMemoryQoS()
		cfg.MemoryQoS.Enable = pointer.BoolPtr(false)
		return
	} else if policy == slov1alpha1.PodMemoryQoSPolicyAuto { // qos=None would be set with kubeQoS for policy=Auto
		cfg.MemoryQoS.MemoryQoS = getPodResourceQoSByQoSClass(pod, util.DefaultResourceQoSStrategy(), m.resmanager.config).MemoryQoS.MemoryQoS
	}

	// no need to merge config if pod-level config is nil
	if podCfg == nil {
		return
	}
	// otherwise detailed pod-level config is specified, merge with node-level config for the pod
	merged, err := util.MergeCfg(&cfg.MemoryQoS.MemoryQoS, &podCfg.MemoryQoS) // node config has been deep-copied
	if err != nil {
		// not change memory qos config if merge error
		klog.Errorf("failed to merge memory qos config with node config, pod %s, err: %s", util.GetPodKey(pod), err)
		return
	}
	cfg.MemoryQoS.MemoryQoS = *merged.(*slov1alpha1.MemoryQoS)
	klog.V(6).Infof("get merged memory qos %v", util.DumpJSON(cfg.MemoryQoS))
}

// updateCgroupSummaryForQoS updates qos cgroup summary by pod to summarize qos-level cgroup according to belonging pods
func updateCgroupSummaryForQoS(summary *cgroupResourceSummary, pod *corev1.Pod, podCfg *slov1alpha1.ResourceQoS) {
	// Memory QoS
	// `memory.min` for qos := sum(requests of pod with the qos * minLimitPercent); if factor is nil, set kernel default
	// `memory.low` for qos := sum(requests of pod with the qos * lowLimitPercent); if factor is nil, set kernel default
	var memRequest int64
	// if any container's memory request is not set, just consider it as zero
	if apiext.GetPodQoSClass(pod) != apiext.QoSBE {
		podRequest := util.GetPodRequest(pod)
		memRequest = podRequest.Memory().Value()
	} else {
		memRequest = util.GetPodBEMemoryByteRequestIgnoreUnlimited(pod)
	}
	if podCfg.MemoryQoS.MinLimitPercent != nil {
		if summary.memoryMin == nil {
			summary.memoryMin = pointer.Int64Ptr(0)
		}
		// assert no overflow for req < 1PiB
		*summary.memoryMin += memRequest * (*podCfg.MemoryQoS.MinLimitPercent) / 100
	}
	if podCfg.MemoryQoS.LowLimitPercent != nil {
		if summary.memoryLow == nil {
			summary.memoryLow = pointer.Int64Ptr(0)
		}
		*summary.memoryLow += memRequest * (*podCfg.MemoryQoS.LowLimitPercent) / 100
	}
}

// completeCgroupSummaryForQoS completes qos cgroup summary considering Guaranteed qos is higher than the others
func completeCgroupSummaryForQoS(qosSummary map[corev1.PodQOSClass]*cgroupResourceSummary) {
	// memory qos
	// Guaranteed cgroup is the ancestor node of Burstable and Besteffort, so the `min` and `low` derive from the sum
	var memMinGuaranteed int64
	var isMemMinGuaranteedEnabled bool
	if qosSummary[corev1.PodQOSGuaranteed].memoryMin != nil {
		memMinGuaranteed += *qosSummary[corev1.PodQOSGuaranteed].memoryMin
		isMemMinGuaranteedEnabled = true
	}
	if qosSummary[corev1.PodQOSBurstable].memoryMin != nil {
		memMinGuaranteed += *qosSummary[corev1.PodQOSBurstable].memoryMin
		isMemMinGuaranteedEnabled = true
	}
	if qosSummary[corev1.PodQOSBestEffort].memoryMin != nil {
		memMinGuaranteed += *qosSummary[corev1.PodQOSBestEffort].memoryMin
		isMemMinGuaranteedEnabled = true
	}
	if isMemMinGuaranteedEnabled {
		qosSummary[corev1.PodQOSGuaranteed].memoryMin = pointer.Int64Ptr(memMinGuaranteed)
	}

	var memLowGuaranteed int64
	var isMemLowGuaranteedEnabled bool
	if qosSummary[corev1.PodQOSGuaranteed].memoryLow != nil {
		memLowGuaranteed += *qosSummary[corev1.PodQOSGuaranteed].memoryLow
		isMemLowGuaranteedEnabled = true
	}
	if qosSummary[corev1.PodQOSBurstable].memoryLow != nil {
		memLowGuaranteed += *qosSummary[corev1.PodQOSBurstable].memoryLow
		isMemLowGuaranteedEnabled = true
	}
	if qosSummary[corev1.PodQOSBestEffort].memoryLow != nil {
		memLowGuaranteed += *qosSummary[corev1.PodQOSBestEffort].memoryLow
		isMemLowGuaranteedEnabled = true
	}
	if isMemLowGuaranteedEnabled {
		qosSummary[corev1.PodQOSGuaranteed].memoryLow = pointer.Int64Ptr(memLowGuaranteed)
	}
}

func makeCgroupResources(owner *OwnerRef, parentDir string, summary *cgroupResourceSummary) []MergeableResourceUpdater {
	var resources []MergeableResourceUpdater

	anolisResources := makeCgroupResourcesForAnolis(owner, parentDir, summary)
	if len(anolisResources) > 0 {
		resources = append(resources, anolisResources...)
	}

	return resources
}

func makeCgroupResourcesForAnolis(owner *OwnerRef, parentDir string, summary *cgroupResourceSummary) []MergeableResourceUpdater {
	var resources []MergeableResourceUpdater

	if !system.HostSystemInfo.IsAnolisOS {
		klog.V(5).Infof("ignored cgroup resources which required non Anolis OS, owner: %v, parentDir: %v",
			owner, parentDir)
		return nil
	}

	//Memory
	if v := summary.memoryMin; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemMin) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewMergeableCgroupResourceUpdater(owner, parentDir, system.MemMin,
			valueStr, mergeFuncUpdateCgroupIfLarger))
	}
	if v := summary.memoryLow; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemLow) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewMergeableCgroupResourceUpdater(owner, parentDir, system.MemLow,
			valueStr, mergeFuncUpdateCgroupIfLarger))
	}
	if v := summary.memoryHigh; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemHigh) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewMergeableCgroupResourceUpdater(owner, parentDir, system.MemHigh,
			valueStr, mergeFuncUpdateCgroupIfLarger))
	}
	if v := summary.memoryWmarkRatio; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemWmarkRatio) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewCommonCgroupResourceUpdater(owner, parentDir, system.MemWmarkRatio, valueStr))
	}
	if v := summary.memoryWmarkScaleFactor; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemWmarkScaleFactor) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewCommonCgroupResourceUpdater(owner, parentDir, system.MemWmarkScaleFactor, valueStr))
	}
	if v := summary.memoryWmarkMinAdj; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemWmarkMinAdj) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewCommonCgroupResourceUpdater(owner, parentDir, system.MemWmarkMinAdj, valueStr))
	}
	// TBD: handle memory priority and oom group
	if v := summary.memoryPriority; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemPriority) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewCommonCgroupResourceUpdater(owner, parentDir, system.MemPriority, valueStr))
	}
	if v := summary.memoryUsePriorityOom; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemUsePriorityOom) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewCommonCgroupResourceUpdater(owner, parentDir, system.MemUsePriorityOom, valueStr))
	}
	if v := summary.memoryOomKillGroup; v != nil && system.ValidateCgroupValue(v, parentDir, system.MemOomGroup) {
		valueStr := strconv.FormatInt(*v, 10)
		resources = append(resources, NewCommonCgroupResourceUpdater(owner, parentDir, system.MemOomGroup, valueStr))
	}

	return resources
}

// getKubeQoSResourceQoSByQoSClass gets pod config by mapping kube qos into koordinator qos.
// https://koordinator.sh/docs/core-concepts/qos/#koordinator-qos-vs-kubernetes-qos
func getKubeQoSResourceQoSByQoSClass(qosClass corev1.PodQOSClass, strategy *slov1alpha1.ResourceQoSStrategy,
	config *Config) *slov1alpha1.ResourceQoS {
	// NOTE: only used for static qos resource calculation here, and it may be incorrect mapping for dynamic qos
	// resource, e.g. qos class of a LS pod can be corev1.PodQOSGuaranteed
	if strategy == nil {
		return nil
	}
	var resourceQoS *slov1alpha1.ResourceQoS
	switch qosClass {
	case corev1.PodQOSGuaranteed:
		resourceQoS = strategy.LSR
	case corev1.PodQOSBurstable:
		resourceQoS = strategy.LS
	case corev1.PodQOSBestEffort:
		resourceQoS = strategy.BE
	}
	return resourceQoS
}

func getPodResourceQoSByQoSClass(pod *corev1.Pod, strategy *slov1alpha1.ResourceQoSStrategy, config *Config) *slov1alpha1.ResourceQoS {
	if strategy == nil {
		return nil
	}
	var resourceQoS *slov1alpha1.ResourceQoS
	podQoS := apiext.GetPodQoSClass(pod)
	switch podQoS {
	case apiext.QoSLSR:
		resourceQoS = strategy.LSR
	case apiext.QoSLS:
		resourceQoS = strategy.LS
	case apiext.QoSBE:
		resourceQoS = strategy.BE
	default:
		// qos=None pods uses config mapped from kubeQoS
		resourceQoS = getKubeQoSResourceQoSByQoSClass(util.GetKubeQosClass(pod), strategy, config)
		klog.V(6).Infof("get pod ResourceQoS according to kubeQoS for QoS=None pods, pod %s, "+
			"resourceQoS %v", util.GetPodKey(pod), util.DumpJSON(resourceQoS))
	}
	return resourceQoS
}
