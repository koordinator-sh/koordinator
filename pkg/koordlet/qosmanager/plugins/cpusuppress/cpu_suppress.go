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

package cpusuppress

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

const (
	CPUSuppressName = "CPUSuppresss"
)

var (
	// if destQuota - currentQuota < suppressMinQuotaDeltaRatio * totalCpu; then bypass;
	suppressBypassQuotaDeltaRatio = 0.01

	cfsPeriod               int64 = 100000
	beMinQuota              int64 = 2000
	beMaxIncreaseCPUPercent       = 0.1 // scale up slow
)

type suppressPolicyStatus string

var (
	policyUsing     suppressPolicyStatus = "using"
	policyRecovered suppressPolicyStatus = "recovered"
)

var _ framework.QOSStrategy = &CPUSuppress{}

type CPUSuppress struct {
	interval               time.Duration
	metricCollectInterval  time.Duration
	statesInformer         statesinformer.StatesInformer
	metricCache            metriccache.MetricCache
	executor               resourceexecutor.ResourceUpdateExecutor
	cgroupReader           resourceexecutor.CgroupReader
	suppressPolicyStatuses map[string]suppressPolicyStatus
}

func New(opt *framework.Options) framework.QOSStrategy {
	return &CPUSuppress{
		interval:               time.Duration(opt.Config.CPUSuppressIntervalSeconds) * time.Second,
		metricCollectInterval:  opt.MetricAdvisorConfig.CollectResUsedInterval,
		statesInformer:         opt.StatesInformer,
		metricCache:            opt.MetricCache,
		executor:               resourceexecutor.NewResourceUpdateExecutor(),
		cgroupReader:           opt.CgroupReader,
		suppressPolicyStatuses: map[string]suppressPolicyStatus{},
	}
}

func (r *CPUSuppress) Enabled() bool {
	return features.DefaultKoordletFeatureGate.Enabled(features.BECPUSuppress) && r.interval > 0
}

func (r *CPUSuppress) Setup(*framework.Context) {

}

func (r *CPUSuppress) Run(stopCh <-chan struct{}) {
	r.init(stopCh)
	go wait.Until(r.suppressBECPU, r.interval, stopCh)
}

func (r *CPUSuppress) init(stopCh <-chan struct{}) {
	r.executor.Run(stopCh)
}

// writeBECgroupsCPUSet writes the be cgroups cpuset by order
func (r *CPUSuppress) writeBECgroupsCPUSet(paths []string, cpusetStr string, isReversed bool) {
	var updaters []resourceexecutor.ResourceUpdater
	eventHelper := audit.V(3).Reason(resourceexecutor.AdjustBEByNodeCPUUsage).Message("update BE group to cpuset: %v", cpusetStr)
	if isReversed {
		for i := len(paths) - 1; i >= 0; i-- {
			u, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.CPUSetCPUSName, paths[i], cpusetStr, eventHelper)
			if err != nil {
				klog.V(4).Infof("failed to get cpuset updater: path %s, err %s", paths[i], err)
				continue
			}

			updaters = append(updaters, u)
		}
	} else {
		for i := range paths {
			u, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.CPUSetCPUSName, paths[i], cpusetStr, eventHelper)
			if err != nil {
				klog.V(4).Infof("failed to get cpuset updater: path %s, err %s", paths[i], err)
				continue
			}

			updaters = append(updaters, u)
		}
	}
	r.executor.UpdateBatch(true, updaters...)
}

// calculateBESuppressCPU calculates the quantity of cpuset cpus for suppressing BE pods.
func (r *CPUSuppress) calculateBESuppressCPU(node *corev1.Node, nodeMetric float64, podMetrics map[string]float64,
	podMetas []*statesinformer.PodMeta, hostApps []slov1alpha1.HostApplicationSpec,
	hostAppMetrics map[string]float64, beCPUUsedThreshold int64) *resource.Quantity {
	nodeReserved := helpers.GetNodeResourceReserved(node)
	nodeReservedCPU := float64(nodeReserved.Cpu().MilliValue()) / 1000

	// calculate pod(non-BE).Used and system.Used
	podNonBEUsedCPU, hostAppNonBEUsedCPU, systemUsedCPU := helpers.CalculateFilterPodsUsed(nodeMetric, nodeReservedCPU,
		podMetas, podMetrics, hostApps, hostAppMetrics, helpers.NonBEPodFilter, helpers.NonBEHostAppFilter)
	podNonBEUsed := resource.NewMilliQuantity(int64(podNonBEUsedCPU*1000), resource.DecimalSI)
	hostAppNonBEUsed := resource.NewMilliQuantity(int64(hostAppNonBEUsedCPU*1000), resource.DecimalSI)
	systemUsed := resource.NewMilliQuantity(int64(systemUsedCPU*1000), resource.DecimalSI)

	// suppress(BE) := node.Capacity * SLOPercent - pod(non-BE).Used - max(system.Used, node.anno.reserved, node.kubelet.reserved)
	// NOTE: valid milli-cpu values should not larger than 2^20, so there is no overflow during the calculation
	nodeBESuppress := resource.NewMilliQuantity(node.Status.Capacity.Cpu().MilliValue()*beCPUUsedThreshold/100, resource.DecimalSI)
	nodeBESuppress.Sub(*podNonBEUsed)
	nodeBESuppress.Sub(*hostAppNonBEUsed)
	nodeBESuppress.Sub(*systemUsed)

	metrics.RecordBESuppressLSUsedCPU(podNonBEUsedCPU)
	klog.V(6).Infof("nodeSuppressBE[CPU(Core)]:%v = node.Capacity:%v * SLOPercent:%v%% - systemUsage:%v - podLSUsed:%v - hostAppLSUsed:%v, upper to %v\n",
		nodeBESuppress.AsApproximateFloat64(), node.Status.Capacity.Cpu().Value(), beCPUUsedThreshold, systemUsedCPU,
		podNonBEUsedCPU, hostAppNonBEUsedCPU, nodeBESuppress.Value())

	return nodeBESuppress
}

func (r *CPUSuppress) applyBESuppressCPUSet(beCPUSet []int32, oldCPUSet []int32) error {
	nodeTopo := r.statesInformer.GetNodeTopo()
	if nodeTopo == nil {
		return errors.New("NodeTopo is nil")
	}
	kubeletPolicy, err := apiext.GetKubeletCPUManagerPolicy(nodeTopo.Annotations)
	if err != nil {
		klog.Errorf("failed to get kubelet cpu manager policy, error %v", err)
		return fmt.Errorf("failed to get kubelet cpu manager policy, %w", err)
	}
	if kubeletPolicy.Policy == apiext.KubeletCPUManagerPolicyStatic {
		r.recoverCPUSetIfNeed(koordletutil.PodCgroupPathRelativeDepth)
		err = r.applyCPUSetWithStaticPolicy(beCPUSet)
	} else {
		err = r.applyCPUSetWithNonePolicy(beCPUSet, oldCPUSet)
	}
	if err != nil {
		return fmt.Errorf("failed with kubelet policy %v, %w", kubeletPolicy.Policy, err)
	}
	return nil
}

// applyCPUSetWithNonePolicy applies the be suppress policy by writing best-effort cgroups
func (r *CPUSuppress) applyCPUSetWithNonePolicy(cpus []int32, oldCPUSet []int32) error {
	// 1. get current be cgroups cpuset
	// 2. temporarily write with a union of old cpuset and new cpuset from upper to lower, to avoid cgroup conflicts
	// 3. write with the new cpuset from lower to upper to apply the real policy
	if len(cpus) <= 0 {
		klog.Warningf("applyCPUSetWithNonePolicy skipped due to the empty cpuset")
		return nil
	}

	cpusetCgroupPaths, err := koordletutil.GetBECPUSetPathsByMaxDepth(koordletutil.ContainerCgroupPathRelativeDepth)
	if err != nil {
		klog.Warningf("applyCPUSetWithNonePolicy failed to get be cgroup cpuset paths, err: %s", err)
		return fmt.Errorf("apply be suppress policy failed, err: %s", err)
	}

	// write a loose cpuset for all be cgroups before applying the real policy
	mergedCPUSet := cpuset.MergeCPUSet(oldCPUSet, cpus)
	mergedCPUSetStr := cpuset.GenerateCPUSetStr(mergedCPUSet)
	klog.V(6).Infof("applyCPUSetWithNonePolicy temporarily writes cpuset from upper cgroup to lower, cpuset %v",
		mergedCPUSet)
	r.writeBECgroupsCPUSet(cpusetCgroupPaths, mergedCPUSetStr, false)

	// apply the suppress policy from lower to upper
	cpusetStr := cpuset.GenerateCPUSetStr(cpus)
	klog.V(6).Infof("applyCPUSetWithNonePolicy writes suppressed cpuset from lower cgroup to upper, cpuset %v",
		cpus)
	r.writeBECgroupsCPUSet(cpusetCgroupPaths, cpusetStr, true)
	metrics.RecordBESuppressCores(string(slov1alpha1.CPUSetPolicy), float64(len(cpus)))
	return nil
}

func (r *CPUSuppress) applyCPUSetWithStaticPolicy(cpus []int32) error {
	if len(cpus) <= 0 {
		klog.Warningf("applyCPUSetWithStaticPolicy skipped due to the empty cpuset")
		return nil
	}

	containerPaths, err := koordletutil.GetBECPUSetPathsByTargetDepth(koordletutil.ContainerCgroupPathRelativeDepth)
	if err != nil {
		klog.Warningf("applyCPUSetWithStaticPolicy failed to get be cgroup cpuset paths, err: %s", err)
		return fmt.Errorf("apply be suppress policy failed, err: %s", err)
	}

	cpusetStr := cpuset.GenerateCPUSetStr(cpus)
	klog.V(6).Infof("applyCPUSetWithStaticPolicy writes suppressed cpuset to containers, cpuset %v", cpus)
	r.writeBECgroupsCPUSet(containerPaths, cpusetStr, false)
	metrics.RecordBESuppressCores(string(slov1alpha1.CPUSetPolicy), float64(len(cpus)))
	return nil
}

// suppressBECPU adjusts the cpusets of BE pods to suppress BE cpu usage
func (r *CPUSuppress) suppressBECPU() {
	// 1. calculate be suppress threshold and check if the suppress is needed
	//    1.1. retrieve latest node resource usage from the metricCache
	//    1.2  calculate the quantity of be suppress cpuset cpus
	// 2. calculate be suppress policy
	//    2.1. new policy should try to get cpuset cpus scattered by numa node, paired by ht core, no less than 2,
	//         less jitter as far as possible
	// 3. apply best-effort cgroups cpuset or cfsquota

	// Step 0.
	nodeSLO := r.statesInformer.GetNodeSLO()
	if disabled, err := features.IsFeatureDisabled(nodeSLO, features.BECPUSuppress); err != nil {
		klog.Warningf("suppressBECPU failed, cannot check the featuregate, err: %s", err)
		return
	} else if features.DefaultKoordletFeatureGate.Enabled(features.BECPUSuppress) &&
		features.DefaultKoordletFeatureGate.Enabled(features.BECPUManager) {
		r.recoverCFSQuotaIfNeed()
		r.recoverCPUSetForBECPUManager()
		klog.V(5).Infof("suppressBECPU cannot work with BECPUManager together, suppress will be skipped, " +
			"recover cpuset on all level if be pod does not specified numa node, and let be cpu set hook handle the others")
		return
	} else if disabled {
		r.recoverCFSQuotaIfNeed()
		r.recoverCPUSetIfNeed(koordletutil.ContainerCgroupPathRelativeDepth)
		klog.V(5).Infof("suppressBECPU skipped, nodeSLO disable the featuregate")
		return
	}

	// Step 1.
	node := r.statesInformer.GetNode()
	if node == nil {
		klog.Warningf("suppressBECPU failed, got nil node")
		return
	}
	podMetas := r.statesInformer.GetAllPods()
	if podMetas == nil || len(podMetas) <= 0 {
		klog.Warningf("suppressBECPU failed, got empty pod metas %v", podMetas)
		return
	}

	podMetrics := helpers.CollectAllPodMetricsLast(r.statesInformer, r.metricCache, metriccache.PodCPUUsageMetric, r.metricCollectInterval)
	if podMetrics == nil {
		klog.Warningf("suppressBECPU failed, got nil node metric or nil pod metrics, podMetrics %v", podMetrics)
		return
	}
	queryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
	if err != nil {
		klog.Warningf("build node query meta failed, error: %v", err)
		return
	}
	nodeCPUUsage, err := helpers.CollectorNodeMetricLast(r.metricCache, queryMeta, r.metricCollectInterval)
	if err != nil {
		klog.Warningf("query node cpu metrics failed, error: %v", err)
		return
	}
	hostAppMetrics := helpers.CollectAllHostAppMetricsLast(nodeSLO.Spec.HostApplications, r.metricCache,
		metriccache.HostAppCPUUsageMetric, r.metricCollectInterval)

	suppressCPUQuantity := r.calculateBESuppressCPU(node, nodeCPUUsage, podMetrics, podMetas,
		nodeSLO.Spec.HostApplications, hostAppMetrics,
		*nodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent)

	// Step 2.
	nodeCPUInfoRaw, exist := r.metricCache.Get(metriccache.NodeCPUInfoKey)
	if !exist {
		klog.Warning("suppressBECPU failed to get nodeCPUInfo from metriccache: not exist")
		return
	}
	nodeCPUInfo, ok := nodeCPUInfoRaw.(*metriccache.NodeCPUInfo)
	if !ok {
		klog.Fatalf("type error, expect %T， but got %T", metriccache.NodeCPUInfo{}, nodeCPUInfoRaw)
	}
	if nodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressPolicy == slov1alpha1.CPUCfsQuotaPolicy {
		r.adjustByCfsQuota(suppressCPUQuantity, node)
		r.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)] = policyUsing
		r.recoverCPUSetIfNeed(koordletutil.ContainerCgroupPathRelativeDepth)
	} else {
		r.adjustByCPUSet(suppressCPUQuantity, nodeCPUInfo)
		r.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)] = policyUsing
		r.recoverCFSQuotaIfNeed()
	}
}

func (r *CPUSuppress) adjustByCPUSet(cpusetQuantity *resource.Quantity, nodeCPUInfo *metriccache.NodeCPUInfo) {
	rootCgroupParentDir := koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
	oldCPUS, err := r.cgroupReader.ReadCPUSet(rootCgroupParentDir)
	if err != nil {
		klog.Warningf("applyBESuppressPolicy failed to get current best-effort cgroup cpuset, err: %s", err)
		return
	}
	oldCPUSet := oldCPUS.ToInt32Slice()

	podMetas := r.statesInformer.GetAllPods()
	// value: 0 -> lse, 1 -> lsr, not exists -> others
	cpuIdToPool := map[int32]apiext.QoSClass{}
	for _, podMeta := range podMetas {
		alloc, err := apiext.GetResourceStatus(podMeta.Pod.Annotations)
		if err != nil {
			continue
		}
		if alloc.CPUSet == "" {
			continue
		}
		set, err := cpuset.Parse(alloc.CPUSet)
		if err != nil {
			klog.Errorf("failed to parse cpuset info of pod %s, err: %v", podMeta.Pod.Name, err)
			continue
		}
		for _, cpuID := range set.ToSliceNoSort() {
			cpuIdToPool[int32(cpuID)] = apiext.GetPodQoSClassRaw(podMeta.Pod)
		}
	}

	topo := r.statesInformer.GetNodeTopo()
	if topo == nil {
		klog.Errorf("node topo is nil")
		return
	}

	var cpusetReserved cpuset.CPUSet
	cpusReservedByAnno, _ := apiext.GetReservedCPUs(topo.Annotations)
	if cpusetReserved, err = cpuset.Parse(cpusReservedByAnno); err != nil {
		klog.Warningf("failed to parse cpuset from node reservation %v, err: %v", cpusReservedByAnno, err)
	}

	// system qos exclusive cpuset
	exclusiveSystemQOSCPUSet, err := getSystemQOSExclusiveCPU(topo.Annotations)
	if err != nil {
		klog.Warningf("get system qos exclusive cpuset failed, error: %v", err)
	}

	var lsrCpus []koordletutil.ProcessorInfo
	var lsCpus []koordletutil.ProcessorInfo
	// FIXME: be pods might be starved since lse pods can run out of all cpus
	for _, processor := range nodeCPUInfo.ProcessorInfos {
		cpuCoreID := cpuset.NewCPUSet(int(processor.CPUID))
		if cpuCoreID.IsSubsetOf(cpusetReserved) || cpuCoreID.IsSubsetOf(exclusiveSystemQOSCPUSet) {
			continue
		}

		if cpuIdToPool[processor.CPUID] == apiext.QoSLSR {
			lsrCpus = append(lsrCpus, processor)
		} else if cpuIdToPool[processor.CPUID] != apiext.QoSLSE {
			lsCpus = append(lsCpus, processor)
		}
	}

	// set the number of cpuset cpus no less than 2
	cpus := int32(math.Ceil(float64(cpusetQuantity.MilliValue()) / 1000))
	if cpus < 2 {
		cpus = 2
	}
	beMaxIncreaseCpuNum := int32(math.Ceil(float64(len(nodeCPUInfo.ProcessorInfos)) * beMaxIncreaseCPUPercent))
	if cpus-int32(len(oldCPUSet)) > beMaxIncreaseCpuNum {
		cpus = int32(len(oldCPUSet)) + beMaxIncreaseCpuNum
	}
	var beCPUSet []int32
	lsrCpuNums := int32(int(cpus) * len(lsrCpus) / (len(lsrCpus) + len(lsCpus)))

	if lsrCpuNums > 0 {
		beCPUSetFromLSR := calculateBESuppressCPUSetPolicy(lsrCpuNums, lsrCpus)
		beCPUSet = append(beCPUSet, beCPUSetFromLSR...)
	}
	if cpus-lsrCpuNums > 0 {
		beCPUSetFromLS := calculateBESuppressCPUSetPolicy(cpus-lsrCpuNums, lsCpus)
		beCPUSet = append(beCPUSet, beCPUSetFromLS...)
	}

	// the new be suppress always need to apply since:
	// - for a reduce of BE cpuset, we should make effort to protecting LS no matter how huge the decrease is;
	// - for a enlargement of BE cpuset, it is welcome and costless for BE processes.
	err = r.applyBESuppressCPUSet(beCPUSet, oldCPUSet)
	if err != nil {
		klog.Warningf("suppressBECPU failed to apply be cpu suppress policy, err: %s", err)
		return
	}
	klog.Infof("suppressBECPU finished, suppress be cpu successfully: current cpuset %v", beCPUSet)
}

// recover cpuset path as be share pool for the following dirs:
// - besteffort dir
// - besteffort/pod dir
// - besteffort/pod/container if pod does not specify resource status(cpuset/numa node)
func (r *CPUSuppress) recoverCPUSetForBECPUManager() {
	beCPUSet, err := r.calcBECPUSet()
	if err != nil {
		klog.Warningf("get be cpuset failed during recoverCPUSetForBECPUManager, error %v", err)
		return
	} else if beCPUSet == nil {
		klog.Warningf("got nil be cpuset during recoverCPUSetForBECPUManager")
		return
	}

	// cpuset path under besteffort dir, include root, pod
	cpusetPathOfAllBEPods, err := koordletutil.GetBECPUSetPathsByMaxDepth(koordletutil.PodCgroupPathRelativeDepth)
	if err != nil {
		klog.Warningf("GetBECPUSetPathsByMaxDepth until pod level failed, error %v", err)
		return
	}

	// cpuset path under besteffort, only include container/sandbox dir
	cpusetPathOfAllBEContainer, err := koordletutil.GetBECPUSetPathsByTargetDepth(koordletutil.ContainerCgroupPathRelativeDepth)
	if err != nil {
		klog.Warningf("GetBECPUSetPathsByTargetDepth until pod level failed, error %v", err)
		return
	}

	// cgroup path of pods which has specified resource status
	cgroupPathOfPodWithSpecifiedCPUSet := make([]string, 0)
	podMetas := r.statesInformer.GetAllPods()
	for _, podMeta := range podMetas {
		if podMeta == nil || podMeta.Pod == nil || podMeta.Pod.Annotations == nil {
			continue
		}
		resourceStatus, err := apiext.GetResourceStatus(podMeta.Pod.Annotations)
		if err != nil {
			klog.Warningf("get resource status for pod %s failed, error %v", podMeta.Key(), err.Error())
			continue
		}

		// no resource status or not specified
		if resourceStatus == nil || (len(resourceStatus.CPUSet) == 0 && len(resourceStatus.NUMANodeResources) == 0) {
			klog.V(6).Infof("skip empty resource status pod %s during parse cpuset, resourceStatus %+v",
				podMeta.Key(), resourceStatus)
			continue
		}

		cgroupPathOfPodWithSpecifiedCPUSet = append(cgroupPathOfPodWithSpecifiedCPUSet, podMeta.CgroupDir)
		klog.V(6).Infof("recoverCPUSet excludes pod %s since it specifies resource status", podMeta.Key())
	}

	// exclude cgroupPathOfPodWithSpecifiedCPUSet from cpusetPathOfAllBEContainer
	cpusetPathOfContainerWithoutSpecified := make([]string, 0)
	for _, cpusetPathOfBEContainer := range cpusetPathOfAllBEContainer {
		shouldExclude := false
		for _, excludePath := range cgroupPathOfPodWithSpecifiedCPUSet {
			if strings.Contains(cpusetPathOfBEContainer, excludePath) {
				shouldExclude = true
				break
			}
		}
		if !shouldExclude {
			cpusetPathOfContainerWithoutSpecified = append(cpusetPathOfContainerWithoutSpecified, cpusetPathOfBEContainer)
		}
	}

	cpusetToRecover := make([]string, 0, len(cpusetPathOfContainerWithoutSpecified)+len(cpusetPathOfAllBEPods))
	cpusetToRecover = append(cpusetToRecover, cpusetPathOfAllBEPods...)
	cpusetToRecover = append(cpusetToRecover, cpusetPathOfContainerWithoutSpecified...)

	cpusetStr := beCPUSet.String()
	klog.V(5).Infof("recover bestEffort cpuset with be cpu manager, cpuset %v", cpusetStr)
	r.writeBECgroupsCPUSet(cpusetToRecover, cpusetStr, false)
	r.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)] = policyRecovered
}

func (r *CPUSuppress) recoverCPUSetIfNeed(maxDepth int) {
	beCPUSet, err := r.calcBECPUSet()
	if err != nil {
		klog.Warningf("get be cpuset failed during recoverCPUSetIfNeed, error %v", err)
		return
	} else if beCPUSet == nil {
		klog.Warningf("got nil be cpuset during recoverCPUSetIfNeed")
		return
	}

	cpusetCgroupPaths, err := koordletutil.GetBECPUSetPathsByMaxDepth(maxDepth)
	if err != nil {
		klog.Warningf("recover bestEffort cpuset failed, get be cgroup cpuset paths  err: %s", err)
		return
	}

	cpusetStr := beCPUSet.String()
	klog.V(6).Infof("recover bestEffort cpuset, cpuset %v", cpusetStr)
	r.writeBECgroupsCPUSet(cpusetCgroupPaths, cpusetStr, false)
	r.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)] = policyRecovered
}

func (r *CPUSuppress) calcBECPUSet() (*cpuset.CPUSet, error) {
	var cpus []int
	nodeCPUInfoRaw, exist := r.metricCache.Get(metriccache.NodeCPUInfoKey)
	if !exist {
		return nil, fmt.Errorf("get node cpu info failed : not exist")
	}
	nodeInfo, ok := nodeCPUInfoRaw.(*metriccache.NodeCPUInfo)
	if !ok {
		klog.Fatalf("type error, expect %T， but got %T", metriccache.NodeCPUInfo{}, nodeCPUInfoRaw)
	}
	for _, p := range nodeInfo.ProcessorInfos {
		cpus = append(cpus, int(p.CPUID))
	}
	beCPUSet := cpuset.NewCPUSet(cpus...)

	topo := r.statesInformer.GetNodeTopo()
	if topo == nil {
		return nil, fmt.Errorf("node topo is nil")
	}

	// LSE pod, reserved cpu and system qos exclusive
	exclusiveCPUID := make(map[int]bool)

	// System pod, exclude cpuset if exclusive
	exclusiveSystemQOSCPUSet, err := getSystemQOSExclusiveCPU(topo.Annotations)
	if err != nil {
		klog.Warningf("get system qos exclusive cpuset failed, error: %v", err)
	} else if !exclusiveSystemQOSCPUSet.IsEmpty() {
		for _, cpuID := range exclusiveSystemQOSCPUSet.ToSliceNoSort() {
			exclusiveCPUID[cpuID] = true
		}
	}

	// exclude reserved cpuset
	var cpusetReserved cpuset.CPUSet
	cpusReservedByAnno, _ := apiext.GetReservedCPUs(topo.Annotations)
	if cpusetReserved, err = cpuset.Parse(cpusReservedByAnno); err != nil {
		klog.Warningf("failed to parse cpuset from node reservation %v, err: %v", cpusReservedByAnno, err)
	} else {
		for _, cpuID := range cpusetReserved.ToSliceNoSort() {
			exclusiveCPUID[cpuID] = true
		}
	}

	podMetas := r.statesInformer.GetAllPods()
	for _, podMeta := range podMetas {
		alloc, err := apiext.GetResourceStatus(podMeta.Pod.Annotations)
		if err != nil {
			continue
		}
		if apiext.GetPodQoSClassRaw(podMeta.Pod) != apiext.QoSLSE {
			continue
		}
		if alloc.CPUSet == "" {
			continue
		}
		set, err := cpuset.Parse(alloc.CPUSet)
		if err != nil {
			klog.Errorf("failed to parse cpuset info of pod %s, err: %v", podMeta.Pod.Name, err)
			continue
		}
		for _, cpuID := range set.ToSliceNoSort() {
			exclusiveCPUID[cpuID] = true
		}
	}
	beCPUSet = beCPUSet.Filter(func(ID int) bool {
		return !exclusiveCPUID[ID]
	})
	return &beCPUSet, nil
}

func (r *CPUSuppress) adjustByCfsQuota(cpuQuantity *resource.Quantity, node *corev1.Node) {
	newBeQuota := cpuQuantity.MilliValue() * cfsPeriod / 1000
	newBeQuota = int64(math.Max(float64(newBeQuota), float64(beMinQuota)))

	beCgroupPath := koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
	// read current offline quota
	currentBeQuota, err := r.cgroupReader.ReadCPUQuota(beCgroupPath)
	if err != nil {
		klog.Warningf("suppressBECPU fail:get currentBeQuota fail,error: %v", err)
		return
	}

	minQuotaDelta := float64(node.Status.Capacity.Cpu().Value()) * float64(cfsPeriod) * suppressBypassQuotaDeltaRatio
	//  delta is large enough
	if math.Abs(float64(newBeQuota)-float64(currentBeQuota)) < minQuotaDelta && newBeQuota != beMinQuota {
		klog.Infof("suppressBECPU: quota delta is too small, bypass suppress.reason: current quota: %d, target quota: %d, min quota delta: %f",
			currentBeQuota, newBeQuota, minQuotaDelta)
		return
	}

	beMaxIncreaseCPUQuota := float64(node.Status.Capacity.Cpu().Value()) * float64(cfsPeriod) * beMaxIncreaseCPUPercent
	if float64(newBeQuota)-float64(currentBeQuota) > beMaxIncreaseCPUQuota {
		newBeQuota = currentBeQuota + int64(beMaxIncreaseCPUQuota)
	}

	eventHelper := audit.V(3).Node().Reason(resourceexecutor.AdjustBEByNodeCPUUsage).Message("update BE group to cfs_quota: %v", newBeQuota)
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.CPUCFSQuotaName, beCgroupPath, strconv.FormatInt(newBeQuota, 10), eventHelper)
	if err != nil {
		klog.V(4).Infof("failed to get be cfs quota updater, err: %v", err)
		return
	}
	isUpdated, err := r.executor.Update(false, updater)
	if err != nil {
		klog.Errorf("suppressBECPU: failed to write cfs_quota_us for be pods, error: %v", err)
		return
	}
	metrics.RecordBESuppressCores(string(slov1alpha1.CPUCfsQuotaPolicy), float64(newBeQuota)/float64(cfsPeriod))
	_ = audit.V(1).Node().Reason(resourceexecutor.AdjustBEByNodeCPUUsage).Message("update BE group to cfs_quota: %v", newBeQuota).Do()
	klog.Infof("suppressBECPU: succeeded to write cfs_quota_us for offline pods, isUpdated %v, new value: %d", isUpdated, newBeQuota)
}

func (r *CPUSuppress) recoverCFSQuotaIfNeed() {
	cfsQuotaPolicyStatus, exist := r.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)]
	if exist && cfsQuotaPolicyStatus == policyRecovered {
		return
	}

	beCgroupPath := koordletutil.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
	eventHelper := audit.V(3).Reason("suppressBECPU").Message("recover bestEffort cfsQuota, isUpdated %v", "-1")
	updater, err := resourceexecutor.DefaultCgroupUpdaterFactory.New(system.CPUCFSQuotaName, beCgroupPath, "-1", eventHelper)
	if err != nil {
		klog.V(4).Infof("failed to get be cfs quota updater, err: %v", err)
		return
	}
	isUpdated, err := r.executor.Update(false, updater)
	if err != nil {
		klog.Errorf("recover bestEffort cfsQuota err: %v", err)
		return
	}
	klog.V(5).Infof("successfully recover bestEffort cfsQuota, isUpdated %v", isUpdated)
	r.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)] = policyRecovered
}

// calculateBESuppressPolicy calculates the be cpu suppress policy with cpuset cpus number and node cpu info
func calculateBESuppressCPUSetPolicy(cpus int32, processorInfos []koordletutil.ProcessorInfo) []int32 {
	var CPUSets []int32
	numProcessors := int32(len(processorInfos))
	if numProcessors < cpus {
		klog.Warningf("failed to calculate a proper suppress policy, available cpus is not enough, "+
			"please check the related resource metrics: want cpus %v but got %v", cpus, numProcessors)
		return CPUSets
	}

	// getNodeIndex is a function to calculate an index for every numa node or socket
	getNodeIndex := func(info koordletutil.ProcessorInfo) int32 {
		// (nodeId, socketId) => nodeIndex
		return (info.NodeID + numProcessors) * (info.SocketID + 1)
	}
	cpuBucketOfNode := map[int32][]koordletutil.ProcessorInfo{}
	for _, p := range processorInfos {
		nodeIndex := getNodeIndex(p)
		cpuBucketOfNode[nodeIndex] = append(cpuBucketOfNode[nodeIndex], p)
	}

	// change cpuBucket map to array
	var cpuBucket [][]koordletutil.ProcessorInfo
	for _, processorInfos := range cpuBucketOfNode {
		cpuBucket = append(cpuBucket, processorInfos)
	}

	for index := range cpuBucket {
		sort.Slice(cpuBucket[index], func(i, j int) bool {
			if cpuBucket[index][i].CoreID == cpuBucket[index][j].CoreID {
				return cpuBucket[index][i].CPUID < cpuBucket[index][j].CPUID
			}
			return cpuBucket[index][i].CoreID < cpuBucket[index][j].CoreID
		})
	}

	sort.Slice(cpuBucket, func(i, j int) bool {
		if len(cpuBucket[i]) == len(cpuBucket[j]) {
			return cpuBucket[i][0].CPUID < cpuBucket[j][0].CPUID
		}
		return len(cpuBucket[i]) > len(cpuBucket[j])
	})

	needCPUs := cpus
	usedCpu := map[int32]bool{}
	// select same core cpu id
	preNeedCpus := int32(-1)
	i := 0
	for ; i < len(cpuBucket); i = (i + 1) % len(cpuBucket) {
		if needCPUs <= 1 {
			break
		}
		if i == 0 {
			// if we don't pick any cpu, we need break this cycle
			if preNeedCpus == needCPUs {
				break
			}
			preNeedCpus = needCPUs
		}
		selectdIndex := -1
		for j := 0; j < len(cpuBucket[i])-1; j++ {
			if usedCpu[cpuBucket[i][j].CPUID] {
				continue
			}
			if cpuBucket[i][j].CoreID == cpuBucket[i][j+1].CoreID {
				selectdIndex = j
				break
			}
		}
		if selectdIndex != -1 {
			CPUSets = append(CPUSets, cpuBucket[i][selectdIndex].CPUID, cpuBucket[i][selectdIndex+1].CPUID)
			usedCpu[cpuBucket[i][selectdIndex].CPUID] = true
			usedCpu[cpuBucket[i][selectdIndex+1].CPUID] = true
			needCPUs = needCPUs - 2
		}
	}

	// select single cpu id
	preNeedCpus = int32(-1)
	startIndex := i
	for ; i < len(cpuBucket); i = (i + 1) % len(cpuBucket) {
		if needCPUs <= 0 {
			break
		}
		if i == startIndex {
			// if we don't pick any cpu, we need break this cycle
			if preNeedCpus == needCPUs {
				break
			}
			preNeedCpus = needCPUs
		}
		selectdIndex := -1
		for j := 0; j < len(cpuBucket[i]); j++ {
			if usedCpu[cpuBucket[i][j].CPUID] {
				continue
			}
			selectdIndex = j
			break
		}
		if selectdIndex != -1 {
			CPUSets = append(CPUSets, cpuBucket[i][selectdIndex].CPUID)
			usedCpu[cpuBucket[i][selectdIndex].CPUID] = true
			needCPUs--
		}
	}
	klog.Infof("calculated BE suppress policy: cpuset %v", CPUSets)
	return CPUSets
}

func getSystemQOSExclusiveCPU(nodeTopoAnno map[string]string) (cpuset.CPUSet, error) {
	exclusiveSystemQOSCPUSet := cpuset.CPUSet{}
	// system qos cpuset exist and exclusive
	if systemQOSRes, err := apiext.GetSystemQOSResource(nodeTopoAnno); err == nil && systemQOSRes != nil && len(systemQOSRes.CPUSet) > 0 && systemQOSRes.IsCPUSetExclusive() {
		// parse cpuset string to struct
		if exclusiveSystemQOSCPUSet, err = cpuset.Parse(systemQOSRes.CPUSet); err != nil {
			return exclusiveSystemQOSCPUSet, fmt.Errorf("parse cpuset from system qos resource failed, origin %v, error %v",
				systemQOSRes.CPUSet, err)
		}
	}
	return exclusiveSystemQOSCPUSet, nil
}
