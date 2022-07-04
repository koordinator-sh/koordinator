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
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
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

type CPUSuppress struct {
	resmanager             *resmanager
	suppressPolicyStatuses map[string]suppressPolicyStatus
}

func NewCPUSuppress(resmanager *resmanager) *CPUSuppress {
	return &CPUSuppress{resmanager: resmanager, suppressPolicyStatuses: map[string]suppressPolicyStatus{}}
}

// getPodMetricCPUUsage gets pod usage cpu from the PodResourceMetric
func getPodMetricCPUUsage(info *metriccache.PodResourceMetric) *resource.Quantity {
	cpuQuant := info.CPUUsed.CPUUsed
	return resource.NewMilliQuantity(cpuQuant.MilliValue(), cpuQuant.Format)
}

// getBECgroupCPUSetPathsRecursive gets all the be cpuset groups' paths recusively from upper to lower
func getBECgroupCPUSetPathsRecursive() ([]string, error) {
	// walk from root path to lower nodes
	rootCgroupPath := util.GetRootCgroupCPUSetDir(corev1.PodQOSBestEffort)
	_, err := os.Stat(rootCgroupPath)
	if err != nil {
		// make sure the rootCgroupPath is available
		return nil, err
	}
	klog.V(6).Infof("get be rootCgroupPath: %v", rootCgroupPath)

	var paths []string
	err = filepath.Walk(rootCgroupPath, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

// writeBECgroupsCPUSet writes the be cgroups cpuset by order
func writeBECgroupsCPUSet(paths []string, cpusetStr string, isReversed bool) {
	if isReversed {
		for i := len(paths) - 1; i >= 0; i-- {
			err := util.WriteCgroupCPUSet(paths[i], cpusetStr)
			if err != nil {
				klog.Warningf("failed to write be cgroup cpuset: path %s, err %s", paths[i], err)
			}
		}
		return
	}
	for i := range paths {
		err := util.WriteCgroupCPUSet(paths[i], cpusetStr)
		if err != nil {
			klog.Warningf("failed to write be cgroup cpuset: path %s, err %s", paths[i], err)
		}
	}
}

// calculateBESuppressCPU calculates the quantity of cpuset cpus for suppressing be pods
func (r *CPUSuppress) calculateBESuppressCPU(node *corev1.Node, nodeMetric *metriccache.NodeResourceMetric,
	podMetrics []*metriccache.PodResourceMetric, podMetas []*statesinformer.PodMeta, beCPUUsedThreshold int64) *resource.Quantity {
	// node, nodeMetric, podMetric should not be nil
	nodeUsedCPU := &nodeMetric.CPUUsed.CPUUsed

	podAllUsedCPU := *resource.NewMilliQuantity(0, resource.DecimalSI)
	podLSUsedCPU := *resource.NewMilliQuantity(0, resource.DecimalSI)

	podMetaMap := map[string]*statesinformer.PodMeta{}
	for _, podMeta := range podMetas {
		podMetaMap[string(podMeta.Pod.UID)] = podMeta
	}

	for _, podMetric := range podMetrics {
		podAllUsedCPU.Add(*getPodMetricCPUUsage(podMetric))

		podMeta, ok := podMetaMap[podMetric.PodUID]
		if !ok {
			klog.Warningf("podMetric not included in the podMetas %v", podMetric.PodUID)
		}
		if !ok || (apiext.GetPodQoSClass(podMeta.Pod) != apiext.QoSBE && util.GetKubeQosClass(podMeta.Pod) != corev1.PodQOSBestEffort) {
			// NOTE: consider non-BE pods and podMeta-missing pods as LS
			podLSUsedCPU.Add(*getPodMetricCPUUsage(podMetric))
		}
	}

	systemUsedCPU := nodeUsedCPU.DeepCopy()
	systemUsedCPU.Sub(podAllUsedCPU)
	if systemUsedCPU.Value() < 0 {
		// set systemUsedCPU always no less than 0
		systemUsedCPU = *resource.NewMilliQuantity(0, resource.DecimalSI)
	}

	// suppress(BE) := node.Total * SLOPercent - pod(LS).Used - system.Used
	// NOTE: valid milli-cpu values should not larger than 2^20, so there is no overflow during the calculation
	nodeBESuppressCPU := resource.NewMilliQuantity(node.Status.Allocatable.Cpu().MilliValue()*beCPUUsedThreshold/100,
		node.Status.Allocatable.Cpu().Format)
	nodeBESuppressCPU.Sub(podLSUsedCPU)
	nodeBESuppressCPU.Sub(systemUsedCPU)
	klog.Infof("nodeSuppressBE[CPU(Core)]:%v = node.Total:%v * SLOPercent:%v%% - systemUsage:%v - podLSUsed:%v\n",
		nodeBESuppressCPU.Value(), node.Status.Allocatable.Cpu().Value(), beCPUUsedThreshold, systemUsedCPU.Value(),
		podLSUsedCPU.Value())

	return nodeBESuppressCPU
}

// calculateBESuppressPolicy calculates the be cpu suppress policy with cpuset cpus number and node cpu info
func calculateBESuppressCPUSetPolicy(cpusetQuantity *resource.Quantity, oldCPUSetNum int, nodeCPUInfo *metriccache.NodeCPUInfo) []int32 {
	// set the number of cpuset cpus no less than 2
	cpus := int32(math.Ceil(float64(cpusetQuantity.MilliValue()) / 1000))
	if cpus < 2 {
		cpus = 2
	}

	var CPUSets []int32

	numProcessors := int32(len(nodeCPUInfo.ProcessorInfos))
	if numProcessors < cpus {
		klog.Warningf("failed to calculate a proper suppress policy, available cpus is not enough, "+
			"please check the related resource metrics: want cpus %v but got %v", cpus, numProcessors)
		return CPUSets
	}

	beMaxIncreaseCpuNum := int32(math.Ceil(float64(numProcessors) * beMaxIncreaseCPUPercent))
	if cpus-int32(oldCPUSetNum) > beMaxIncreaseCpuNum {
		cpus = int32(oldCPUSetNum) + beMaxIncreaseCpuNum
	}

	// NOTE: here we prioritize the processors by the affinity of numa node/socket and hyper-threading so that the
	//       cpuset cpus can be picked in order, rather than scoring all the possible policies.
	// TBD: may just use a lookup table for a steady state

	// initially sorted by processor id
	prioritizedCPUs := nodeCPUInfo.ProcessorInfos
	sort.Slice(prioritizedCPUs, func(i, j int) bool {
		return prioritizedCPUs[i].CPUID < prioritizedCPUs[j].CPUID
	})

	// getNodeIndex is a function to calculate an index for every numa node or socket
	getNodeIndex := func(info util.ProcessorInfo) int32 {
		return (info.NodeID + numProcessors) * (info.SocketID + 1)
	}

	// get the relative indexes of the processor on its node
	indexOfNode := map[int32]int32{}
	numProcessorsOfNode := map[int32]int32{}
	for _, processor := range prioritizedCPUs {
		numProcessorsOfNode[getNodeIndex(processor)]++
		if indexOfNode[processor.CoreID] <= 0 {
			indexOfNode[processor.CoreID] = numProcessorsOfNode[getNodeIndex(processor)]
		}
	}

	// first we prefer the scattered node/socket, secondly the same core
	// e.g.
	// for a processorInfo/prioritizedCPUs list:
	//	CPUID	CoreID	SocketID	NodeID
	//	0		0		0			0
	//	1		1		0			0
	//	2		2		1			1
	//	3		3		1			1
	//	4		0		0			0
	//	5		1		0			0
	//	6		2		1			1
	//	7		3		1			1
	// it will get sorted as below:
	//	CPUID	CoreID	SocketID	NodeID
	//	7		3		1			1
	//	3		3		1			1
	//	5		1		0			0
	//	1		1		0			0
	//	6		2		1			1
	//	2		2		1			1
	//	4		0		0			0
	//	0		0		0			0
	sort.Slice(prioritizedCPUs, func(i, j int) bool {
		a, b := prioritizedCPUs[i], prioritizedCPUs[j]
		if indexOfNode[a.CoreID] != indexOfNode[b.CoreID] {
			return indexOfNode[a.CoreID] > indexOfNode[b.CoreID]
		}
		if numProcessorsOfNode[getNodeIndex(a)] != numProcessorsOfNode[getNodeIndex(b)] {
			return numProcessorsOfNode[getNodeIndex(a)] > numProcessorsOfNode[getNodeIndex(b)]
		}
		if a.CoreID != b.CoreID {
			return a.CoreID > b.CoreID
		}
		return a.CPUID > b.CPUID
	})

	needCPUs := cpus
	for i := range prioritizedCPUs {
		if needCPUs <= 0 {
			break
		}
		CPUSets = append(CPUSets, prioritizedCPUs[i].CPUID)
		needCPUs--
	}
	klog.V(4).InfoS("calculated BE suppress policy", "cpuset", CPUSets)

	return CPUSets
}

// applyBESuppressPolicy applies the be suppress policy by writing best-effort cgroups
func applyBESuppressCPUSetPolicy(cpuset []int32, oldCPUSet []int32) error {
	// 1. get current be cgroups cpuset
	// 2. temporarily write with a union of old cpuset and new cpuset from upper to lower, to avoid cgroup conflicts
	// 3. write with the new cpuset from lower to upper to apply the real policy
	if len(cpuset) <= 0 {
		klog.Warningf("applyBESuppressPolicy skipped due to the empty cpuset")
		return nil
	}

	cpusetCgroupPaths, err := getBECgroupCPUSetPathsRecursive()
	if err != nil {
		klog.Warningf("applyBESuppressPolicy failed to get be cgroup cpuset paths, err: %s", err)
		return fmt.Errorf("apply be suppress policy failed, err: %s", err)
	}

	// write a loose cpuset for all be cgroups before applying the real policy
	mergedCPUSet := util.MergeCPUSet(oldCPUSet, cpuset)
	mergedCPUSetStr := util.GenerateCPUSetStr(mergedCPUSet)
	klog.V(6).Infof("applyBESuppressPolicy temporarily writes cpuset from upper cgroup to lower, cpuset %v",
		mergedCPUSet)
	writeBECgroupsCPUSet(cpusetCgroupPaths, mergedCPUSetStr, false)

	// apply the suppress policy from lower to upper
	cpusetStr := util.GenerateCPUSetStr(cpuset)
	klog.V(6).Infof("applyBESuppressPolicy writes suppressed cpuset from lower cgroup to upper, cpuset %v",
		cpuset)
	writeBECgroupsCPUSet(cpusetCgroupPaths, cpusetStr, true)
	metrics.RecordBESuppressCores(string(slov1alpha1.CPUSetPolicy), float64(len(cpuset)))
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
	nodeSLO := r.resmanager.getNodeSLOCopy()
	if disabled, err := isFeatureDisabled(nodeSLO, features.BECPUSuppress); err != nil {
		klog.Warningf("suppressBECPU failed, cannot check the featuregate, err: %s", err)
		return
	} else if disabled {
		r.recoverCFSQuotaIfNeed()
		r.recoverCPUSetIfNeed()
		klog.V(5).Infof("suppressBECPU skipped, nodeSLO disable the featuregate")
		return
	}

	// Step 1.
	node := r.resmanager.statesInformer.GetNode()
	if node == nil {
		klog.Warningf("suppressBECPU failed, got nil node %s", r.resmanager.nodeName)
		return
	}
	podMetas := r.resmanager.statesInformer.GetAllPods()
	if podMetas == nil || len(podMetas) <= 0 {
		klog.Warningf("suppressBECPU failed, got empty pod metas %v", podMetas)
		return
	}

	nodeMetric, podMetrics := r.resmanager.collectNodeAndPodMetricLast()
	if nodeMetric == nil || podMetrics == nil {
		klog.Warningf("suppressBECPU failed, got nil node metric or nil pod metrics, nodeMetric %v, podMetrics %v",
			nodeMetric, podMetrics)
		return
	}

	suppressCPUQuantity := r.calculateBESuppressCPU(node, nodeMetric, podMetrics, podMetas,
		*nodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressThresholdPercent)

	// Step 2.
	nodeCPUInfo, err := r.resmanager.metricCache.GetNodeCPUInfo(&metriccache.QueryParam{})
	if err != nil {
		klog.Warningf("suppressBECPU failed to get nodeCPUInfo from metriccache, err: %s", err)
		return
	}

	if nodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressPolicy == slov1alpha1.CPUCfsQuotaPolicy {
		adjustByCfsQuota(suppressCPUQuantity, node)
		r.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)] = policyUsing
		r.recoverCPUSetIfNeed()
	} else {
		adjustByCPUSet(suppressCPUQuantity, nodeCPUInfo)
		r.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)] = policyUsing
		r.recoverCFSQuotaIfNeed()
	}
}

func adjustByCPUSet(cpusetQuantity *resource.Quantity, nodeCPUInfo *metriccache.NodeCPUInfo) {
	oldCPUSet, err := util.GetRootCgroupCurCPUSet(corev1.PodQOSBestEffort)
	if err != nil {
		klog.Warningf("applyBESuppressPolicy failed to get current best-effort cgroup cpuset, err: %s", err)
		return
	}

	beCPUSet := calculateBESuppressCPUSetPolicy(cpusetQuantity, len(oldCPUSet), nodeCPUInfo)

	// the new be suppress always need to apply since:
	// - for a reduce of BE cpuset, we should make effort to protecting LS no matter how huge the decrease is;
	// - for a enlargement of BE cpuset, it is welcome and costless for BE processes.
	err = applyBESuppressCPUSetPolicy(beCPUSet, oldCPUSet)
	if err != nil {
		klog.Warningf("suppressBECPU failed to apply be cpu suppress policy, err: %s", err)
		return
	}
	audit.V(1).Node().Reason(adjustBEByNodeCPUUsage).Message("update BE group to cpuset: %v", beCPUSet).Do()
	klog.V(2).Infof("suppressBECPU finished, suppress be cpu successfully: current cpuset %v", beCPUSet)
}

func (r *CPUSuppress) recoverCPUSetIfNeed() {
	cpusetPolicyStatus, exist := r.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)]
	if exist && cpusetPolicyStatus == policyRecovered {
		return
	}

	rootCPUSet, err := util.GetRootCgroupCurCPUSet(corev1.PodQOSGuaranteed)
	if err != nil {
		klog.Warningf("recover bestEffort cpuset failed, get current root cgroup cpuset err: %s", err)
		return
	}
	cpusetCgroupPaths, err := getBECgroupCPUSetPathsRecursive()
	if err != nil {
		klog.Warningf("recover bestEffort cpuset failed, get be cgroup cpuset paths  err: %s", err)
		return
	}

	cpusetStr := util.GenerateCPUSetStr(rootCPUSet)
	klog.V(6).Infof("recover bestEffort cpuset, cpuset %v", rootCPUSet)
	writeBECgroupsCPUSet(cpusetCgroupPaths, cpusetStr, false)
	r.suppressPolicyStatuses[string(slov1alpha1.CPUSetPolicy)] = policyRecovered
}

func adjustByCfsQuota(cpuQuantity *resource.Quantity, node *corev1.Node) {
	newBeQuota := cpuQuantity.MilliValue() * cfsPeriod / 1000
	newBeQuota = int64(math.Max(float64(newBeQuota), float64(beMinQuota)))

	beCgroupPath := util.GetKubeQosRelativePath(corev1.PodQOSBestEffort)
	// read current offline quota
	currentBeQuota, err := system.CgroupFileReadInt(beCgroupPath, system.CPUCFSQuota)
	if err != nil {
		klog.Warningf("suppressBECPU fail:get currentBeQuota fail,error: %v", err)
		return
	}

	minQuotaDelta := float64(node.Status.Capacity.Cpu().Value()) * float64(cfsPeriod) * suppressBypassQuotaDeltaRatio
	//  delta is large enough
	if math.Abs(float64(newBeQuota)-float64(*currentBeQuota)) < minQuotaDelta && newBeQuota != beMinQuota {
		klog.Infof("suppressBECPU: quota delta is too small, bypass suppress.reason: current quota: %d, target quota: %d, min quota delta: %f",
			currentBeQuota, newBeQuota, minQuotaDelta)
		return
	}

	beMaxIncreaseCPUQuota := float64(node.Status.Capacity.Cpu().Value()) * float64(cfsPeriod) * beMaxIncreaseCPUPercent
	if float64(newBeQuota)-float64(*currentBeQuota) > beMaxIncreaseCPUQuota {
		newBeQuota = *currentBeQuota + int64(beMaxIncreaseCPUQuota)
	}

	if err := system.CgroupFileWrite(beCgroupPath, system.CPUCFSQuota, strconv.FormatInt(newBeQuota, 10)); err != nil {
		klog.Errorf("suppressBECPU: failed to write cfs_quota_us for offline pods, error: %v", err)
		return
	}
	metrics.RecordBESuppressCores(string(slov1alpha1.CPUCfsQuotaPolicy), float64(newBeQuota)/float64(cfsPeriod))
	audit.V(1).Node().Reason(adjustBEByNodeCPUUsage).Message("update BE group to cfs_quota: %v", newBeQuota).Do()
	klog.V(2).Infof("suppressBECPU: succeeded to write cfs_quota_us for offline pods, new value: %d", newBeQuota)
}

func (r *CPUSuppress) recoverCFSQuotaIfNeed() {
	cfsQuotaPolicyStatus, exist := r.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)]
	if exist && cfsQuotaPolicyStatus == policyRecovered {
		return
	}

	beCgroupPath := util.GetKubeQosRelativePath(corev1.PodQOSBestEffort)
	if err := system.CgroupFileWrite(beCgroupPath, system.CPUCFSQuota, "-1"); err != nil {
		klog.Errorf("recover bestEffort cfsQuota error: %v", err)
		return
	}
	r.suppressPolicyStatuses[string(slov1alpha1.CPUCfsQuotaPolicy)] = policyRecovered
}

func getCPUSuppressPolicy(nodeSLO *slov1alpha1.NodeSLO) slov1alpha1.CPUSuppressPolicy {
	if nodeSLO == nil || nodeSLO.Spec.ResourceUsedThresholdWithBE == nil ||
		nodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressPolicy == "" {
		return util.DefaultResourceThresholdStrategy().CPUSuppressPolicy
	}
	return nodeSLO.Spec.ResourceUsedThresholdWithBE.CPUSuppressPolicy
}
