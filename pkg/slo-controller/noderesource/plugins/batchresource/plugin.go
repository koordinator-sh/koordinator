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

package batchresource

import (
	"context"
	"fmt"
	"time"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/metrics"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	resutil "github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/util"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const PluginName = "BatchResource"

var (
	ResourceNames = []corev1.ResourceName{extension.BatchCPU, extension.BatchMemory}
)

var (
	Clock          clock.WithTickerAndDelayedExecution = clock.RealClock{} // for testing
	client         ctrlclient.Client
	nrtHandler     *NRTHandler
	nrtSyncContext *framework.SyncContext
)

// Plugin does 2 things:
// 1. calculate and update the extended resources of batch-cpu and batch-memory on the Node.
// 2. calculate and update the zone resources of batch-cpu and batch-memory on the NodeResourceTopology.
type Plugin struct{}

func (p *Plugin) Name() string {
	return PluginName
}

// +kubebuilder:rbac:groups=topology.node.k8s.io,resources=noderesourcetopologies,verbs=get;list;watch;create;update

func (p *Plugin) Setup(opt *framework.Option) error {
	client = opt.Client
	nrtSyncContext = framework.NewSyncContext()

	if err := topologyv1alpha1.AddToScheme(opt.Scheme); err != nil {
		return fmt.Errorf("failed to add scheme for NodeResourceTopology, err: %w", err)
	}
	if err := topologyv1alpha1.AddToScheme(clientgoscheme.Scheme); err != nil {
		return fmt.Errorf("failed to add client go scheme for NodeResourceTopology, err: %w", err)
	}

	nrtHandler = &NRTHandler{syncContext: nrtSyncContext}
	opt.Builder = opt.Builder.Watches(&topologyv1alpha1.NodeResourceTopology{}, nrtHandler)

	return nil
}

func (p *Plugin) PreUpdate(strategy *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) error {
	// prepare for zone resources on NRT objects
	err := p.prepareForNodeResourceTopology(strategy, node, nr)
	if err != nil {
		return fmt.Errorf("failed to prepare for NRT, err: %w", err)
	}
	return nil
}

func (p *Plugin) NeedSync(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	// batch resource diff is bigger than ResourceDiffThreshold
	resourcesToDiff := ResourceNames
	for _, resourceName := range resourcesToDiff {
		if util.IsResourceDiff(oldNode.Status.Allocatable, newNode.Status.Allocatable, resourceName,
			*strategy.ResourceDiffThreshold) {
			klog.V(4).Infof("node %v resource %v diff bigger than %v, need sync",
				newNode.Name, resourceName, *strategy.ResourceDiffThreshold)
			return true, "batch resource diff is big than threshold"
		}
	}

	return false, ""
}

func (p *Plugin) Prepare(_ *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) error {
	// prepare for node extended resources
	for _, resourceName := range ResourceNames {
		resutil.PrepareNodeForResource(node, nr, resourceName)
	}

	// set origin batch allocatable
	batchMilliCPU := util.GetNodeAllocatableBatchMilliCPU(node)
	//batchMilliCPU := util.GetBatchMilliCPUFromResourceList(node.Status.Allocatable)
	batchMemory := util.GetNodeAllocatableBatchMemory(node)
	originBatchAllocatable := corev1.ResourceList{
		extension.BatchCPU:    *resource.NewQuantity(util.MaxInt64(batchMilliCPU, 0), resource.DecimalSI),
		extension.BatchMemory: *resource.NewQuantity(util.MaxInt64(batchMemory, 0), resource.BinarySI),
	}
	if err := slov1alpha1.SetOriginExtendedAllocatableRes(node.Annotations, originBatchAllocatable); err != nil {
		return err
	}

	if batchMilliCPU < 0 || batchMemory < 0 {
		// batch resources are reset, no need to recalculate node status
		return nil
	}

	// subtract third party allocated
	thirdPartyBatchAllocated, err := slov1alpha1.GetThirdPartyAllocatedResByPriority(node.Annotations, extension.PriorityBatch)
	if err != nil || thirdPartyBatchAllocated == nil {
		return err
	}
	batchZero := corev1.ResourceList{
		extension.BatchCPU:    *resource.NewQuantity(0, resource.DecimalSI),
		extension.BatchMemory: *resource.NewQuantity(0, resource.BinarySI),
	}
	kubeBatchAllocatable := quotav1.Max(quotav1.Subtract(originBatchAllocatable, thirdPartyBatchAllocated), batchZero)
	for _, resourceName := range ResourceNames {
		if value, exist := kubeBatchAllocatable[resourceName]; exist {
			node.Status.Allocatable[resourceName] = value
			node.Status.Capacity[resourceName] = value
		}
	}
	return nil
}

func (p *Plugin) Reset(node *corev1.Node, message string) []framework.ResourceItem {
	items := make([]framework.ResourceItem, len(ResourceNames))
	for i := range ResourceNames {
		items[i].Name = ResourceNames[i]
		items[i].Message = message
		items[i].Reset = true
	}

	return items
}

// Calculate calculates Batch resources using the formula below:
// Node.Total - Node.Reserved - System.Used - Pod(High-Priority).Used, System.Used = Node.Used - Pod(All).Used.
// As node and podList are the nearly latest state at time T1, the resourceMetrics are the node metric and pod
// metrics collected and snapshot at time T0 (T0 < T1). There can be gaps between the states of T0 and T1.
// We firstly calculate an infimum of the batch allocatable at time T0.
// `BatchAllocatable0 = NodeAllocatable * ratio - SystemUsed0 - Pod(HP and in Pods1).Used0` - Pod(not in Pods1).Used0.
// Then we minus the sum requests of the pods newly scheduled but have not been reported metrics to give a safe result.
func (p *Plugin) Calculate(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList,
	resourceMetrics *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	if strategy == nil || node == nil || podList == nil || resourceMetrics == nil || resourceMetrics.NodeMetric == nil {
		return nil, fmt.Errorf("missing essential arguments")
	}

	// if the node metric is abnormal, do degraded calculation
	if p.isDegradeNeeded(strategy, resourceMetrics.NodeMetric, node) {
		klog.InfoS("node need degradation, reset node resources", "node", node.Name)
		return p.degradeCalculate(node,
			"degrade node resource because of abnormal nodeMetric, reason: degradedByBatchResource"), nil
	}

	return p.calculate(strategy, node, podList, resourceMetrics)
}

func (p *Plugin) calculate(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList,
	resourceMetrics *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	// calculate node-level batch resources
	batchAllocatable, cpuMsg, memMsg := p.calculateOnNode(strategy, node, podList, resourceMetrics)

	// calculate NUMA-level batch resources on NodeResourceTopology
	batchZoneCPU, batchZoneMemory, err := p.calculateOnNUMALevel(strategy, node, podList, resourceMetrics)
	if err != nil {
		klog.ErrorS(err, "failed to calculate batch resource for NRT", "node", node.Name)
	}

	return []framework.ResourceItem{
		{
			Name:         extension.BatchCPU,
			Quantity:     resource.NewQuantity(batchAllocatable.Cpu().MilliValue(), resource.DecimalSI),
			ZoneQuantity: batchZoneCPU,
			Message:      cpuMsg,
		},
		{
			Name:         extension.BatchMemory,
			Quantity:     batchAllocatable.Memory(),
			ZoneQuantity: batchZoneMemory,
			Message:      memMsg,
		},
	}, nil
}

// In order to support the colocation requirements of different enterprise environments, a configurable colocation strategy is provided.
// The resource view from the node perspective is as follows:
//
//	https://github.com/koordinator-sh/koordinator/blob/main/docs/images/node-resource-model.png
//
// Typical colocation scenario:
//  1. default policy, and the CPU and memory that can be collocated are automatically calculated based on the load level of the node.
//  2. default policy on CPU, and the Memory is configured not to be overcommitted. This can reduce the probability of batch pods
//     being killed due to high memory water levels (reduce the kill rate)
//
// In each scenario, users can also adjust the resource water level configuration according to your own needs and control the deployment
// density of batch pods.
func (p *Plugin) calculateOnNode(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList,
	resourceMetrics *framework.ResourceMetrics) (corev1.ResourceList, string, string) {
	// compute the requests and usages according to the pods' priority classes.
	// HP means High-Priority (i.e. not Batch or Free) pods
	podsHPRequest := util.NewZeroResourceList()
	podsHPUsed := util.NewZeroResourceList()
	podsHPMaxUsedReq := util.NewZeroResourceList()
	// podsAllUsed is the sum usage of all pods reported in NodeMetric.
	// podsKnownUsed is the sum usage of pods which are both reported in NodeMetric and shown in current pod list.
	podsAllUsed := util.NewZeroResourceList()

	nodeMetric := resourceMetrics.NodeMetric
	podMetricMap := make(map[string]*slov1alpha1.PodMetricInfo)
	podMetricDanglingMap := make(map[string]*slov1alpha1.PodMetricInfo)
	for _, podMetric := range nodeMetric.Status.PodsMetric {
		podKey := util.GetPodMetricKey(podMetric)
		podMetricMap[podKey] = podMetric
		podMetricDanglingMap[podKey] = podMetric

		podUsage := resutil.GetPodMetricUsage(podMetric)
		podsAllUsed = quotav1.Add(podsAllUsed, podUsage)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}

		// check if the pod has metrics
		podKey := util.GetPodKey(pod)
		podMetric, hasMetric := podMetricMap[podKey]
		if hasMetric {
			delete(podMetricDanglingMap, podKey)
		}

		// count the high-priority usage
		podRequest := util.GetPodRequest(pod, corev1.ResourceCPU, corev1.ResourceMemory)
		if priority := extension.GetPodPriorityClassWithDefault(pod); priority == extension.PriorityBatch ||
			priority == extension.PriorityFree { // ignore LP pods
			continue
		}

		podsHPRequest = quotav1.Add(podsHPRequest, podRequest)
		if !hasMetric {
			podsHPUsed = quotav1.Add(podsHPUsed, podRequest)
		} else if qos := extension.GetPodQoSClassWithDefault(pod); qos == extension.QoSLSE {
			// NOTE: Currently qos=LSE pods does not reclaim CPU resource.
			podUsed := resutil.GetPodMetricUsage(podMetric)
			podsHPUsed = quotav1.Add(podsHPUsed, resutil.MixResourceListCPUAndMemory(podRequest, podUsed))
			podsHPMaxUsedReq = quotav1.Add(podsHPMaxUsedReq, quotav1.Max(podRequest, podUsed))
		} else {
			podUsed := resutil.GetPodMetricUsage(podMetric)
			podsHPUsed = quotav1.Add(podsHPUsed, podUsed)
			podsHPMaxUsedReq = quotav1.Add(podsHPMaxUsedReq, quotav1.Max(podRequest, podUsed))
		}
	}

	hostAppHPUsed := resutil.GetHostAppHPUsed(resourceMetrics, extension.PriorityBatch)
	// For the pods reported metrics but not shown in current list, count them according to the metric priority.
	podsDanglingUsed := util.NewZeroResourceList()
	for _, podMetric := range podMetricDanglingMap {
		if priority := podMetric.Priority; priority == extension.PriorityBatch || priority == extension.PriorityFree {
			continue
		}
		podsDanglingUsed = quotav1.Add(podsDanglingUsed, resutil.GetPodMetricUsage(podMetric))
	}
	podsHPUsed = quotav1.Add(podsHPUsed, podsDanglingUsed)
	podsHPMaxUsedReq = quotav1.Add(podsHPMaxUsedReq, podsDanglingUsed)
	klog.V(6).InfoS("batch resource got dangling HP pods used", "node", node.Name,
		"cpu", podsDanglingUsed.Cpu().String(), "memory", podsDanglingUsed.Memory().String())

	nodeCapacity := resutil.GetNodeCapacity(node)
	nodeSafetyMargin := resutil.GetNodeSafetyMargin(strategy, nodeCapacity)

	systemUsed := resutil.GetResourceListForCPUAndMemory(nodeMetric.Status.NodeMetric.SystemUsage.ResourceList)
	// resource usage of host applications with prod priority will be count as host system usage since they consumes the
	// node reserved resource.
	systemUsed = quotav1.Add(systemUsed, hostAppHPUsed)

	// System.Reserved = Node.Anno.Reserved, Node.Kubelet.Reserved)
	nodeAnnoReserved := util.GetNodeReservationFromAnnotation(node.Annotations)
	nodeKubeletReserved := util.GetNodeReservationFromKubelet(node)
	// FIXME: resource reservation taking max is rather confusing.
	nodeReserved := quotav1.Max(nodeKubeletReserved, nodeAnnoReserved)

	batchAllocatable, cpuMsg, memMsg := resutil.CalculateBatchResourceByPolicy(strategy, nodeCapacity, nodeSafetyMargin, nodeReserved,
		systemUsed, podsHPRequest, podsHPUsed, podsHPMaxUsedReq)
	metrics.RecordNodeExtendedResourceAllocatableInternal(node, string(extension.BatchCPU), metrics.UnitInteger, float64(batchAllocatable.Cpu().MilliValue())/1000)
	metrics.RecordNodeExtendedResourceAllocatableInternal(node, string(extension.BatchMemory), metrics.UnitByte, float64(batchAllocatable.Memory().Value()))
	klog.V(6).InfoS("calculate batch resource for node", "node", node.Name, "batch resource",
		batchAllocatable, "cpu", cpuMsg, "memory", memMsg)

	return batchAllocatable, cpuMsg, memMsg
}

func (p *Plugin) calculateOnNUMALevel(strategy *configuration.ColocationStrategy, node *corev1.Node, podList *corev1.PodList,
	resourceMetrics *framework.ResourceMetrics) (map[string]resource.Quantity, map[string]resource.Quantity, error) {
	nrt := &topologyv1alpha1.NodeResourceTopology{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: node.Name}, nrt)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(6).Infof("skip calculate batch resources for NRT, NRT not found, node %s", node.Name)
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to get NodeResourceTopology for batch resource, err: %w", err)
	}
	if !isNRTResourcesCreated(nrt) {
		klog.V(5).Infof("abort to calculate batch resources for NRT, resources %v not found, node %s",
			checkedNRTResourceSet.List(), node.Name)
		return nil, nil, nil
	}

	// separate zone resources
	// assert the zone is mapped into NUMA levels
	// FIXME: Since NUMA-level metrics are not reported, we use an approximation here:
	//        node reservation, system usage and unknown pods usage are the same in each zones.
	zoneNum := len(nrt.Zones)
	zoneIdxMap := map[int]string{}
	nodeMetric := resourceMetrics.NodeMetric

	nodeZoneAllocatable := make([]corev1.ResourceList, zoneNum)
	nodeZoneReserve := make([]corev1.ResourceList, zoneNum)
	systemZoneUsed := make([]corev1.ResourceList, zoneNum)
	systemZoneReserved := make([]corev1.ResourceList, zoneNum)
	podsUnknownUsed := make([]corev1.ResourceList, zoneNum)
	podsHPZoneRequested := make([]corev1.ResourceList, zoneNum)
	podsHPZoneUsed := make([]corev1.ResourceList, zoneNum)
	podsHPZoneMaxUsedReq := make([]corev1.ResourceList, zoneNum)
	batchZoneAllocatable := make([]corev1.ResourceList, zoneNum)

	hostAppHPUsed := resutil.GetHostAppHPUsed(resourceMetrics, extension.PriorityBatch)
	systemUsed := resutil.GetResourceListForCPUAndMemory(nodeMetric.Status.NodeMetric.SystemUsage.ResourceList)
	// resource usage of host applications with prod priority will be count as host system usage since they consumes the
	// node reserved resource. bind host app on single numa node is not supported yet. divide the usage by numa node number.
	systemUsed = quotav1.Add(systemUsed, hostAppHPUsed)
	nodeAnnoReserved := util.GetNodeReservationFromAnnotation(node.Annotations)
	nodeKubeletReserved := util.GetNodeReservationFromKubelet(node)
	nodeReserved := quotav1.Max(nodeKubeletReserved, nodeAnnoReserved)

	for i, zone := range nrt.Zones {
		zoneIdxMap[i] = zone.Name
		nodeZoneAllocatable[i] = corev1.ResourceList{}
		podsUnknownUsed[i] = util.NewZeroResourceList()
		podsHPZoneRequested[i] = util.NewZeroResourceList()
		podsHPZoneUsed[i] = util.NewZeroResourceList()
		podsHPZoneMaxUsedReq[i] = util.NewZeroResourceList()
		for _, resourceInfo := range zone.Resources {
			if checkedNRTResourceSet.Has(resourceInfo.Name) {
				nodeZoneAllocatable[i][corev1.ResourceName(resourceInfo.Name)] = resourceInfo.Allocatable.DeepCopy()
			}
		}
		nodeZoneReserve[i] = resutil.GetNodeSafetyMargin(strategy, nodeZoneAllocatable[i])
		systemZoneUsed[i] = resutil.DivideResourceList(systemUsed, float64(zoneNum))
		systemZoneReserved[i] = resutil.DivideResourceList(nodeReserved, float64(zoneNum))
	}
	podMetricMap := make(map[string]*slov1alpha1.PodMetricInfo)
	podMetricUnknownMap := make(map[string]*slov1alpha1.PodMetricInfo)
	for _, podMetric := range nodeMetric.Status.PodsMetric {
		podKey := util.GetPodMetricKey(podMetric)
		podMetricMap[podKey] = podMetric
		podMetricUnknownMap[podKey] = podMetric
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}

		// check if the pod has metrics
		podKey := util.GetPodKey(pod)
		podMetric, hasMetric := podMetricMap[podKey]
		podRequest := util.GetPodRequest(pod, corev1.ResourceCPU, corev1.ResourceMemory)
		var podUsage corev1.ResourceList
		var podZoneRequests, podZoneUsages []corev1.ResourceList
		if hasMetric {
			delete(podMetricUnknownMap, podKey)
			podUsage = resutil.GetPodMetricUsage(podMetric)
			podZoneRequests, podZoneUsages = resutil.GetPodNUMARequestAndUsage(pod, podRequest, podUsage, zoneNum)
		} else {
			podUsage = podRequest
			podZoneRequests, podZoneUsages = resutil.GetPodNUMARequestAndUsage(pod, podRequest, podUsage, zoneNum)
		}

		// count the high-priority usage
		if priority := extension.GetPodPriorityClassWithDefault(pod); priority == extension.PriorityBatch ||
			priority == extension.PriorityFree {
			continue
		}

		podsHPZoneRequested = resutil.AddZoneResourceList(podsHPZoneRequested, podZoneRequests, zoneNum)
		if !hasMetric {
			podsHPZoneUsed = resutil.AddZoneResourceList(podsHPZoneUsed, podZoneRequests, zoneNum)
			podsHPZoneMaxUsedReq = resutil.AddZoneResourceList(podsHPZoneMaxUsedReq, podZoneRequests, zoneNum)
		} else if qos := extension.GetPodQoSClassWithDefault(pod); qos == extension.QoSLSE {
			// NOTE: Currently qos=LSE pods does not reclaim CPU resource.
			podsHPZoneUsed = resutil.AddZoneResourceList(podsHPZoneUsed,
				resutil.MinxZoneResourceListCPUAndMemory(podZoneRequests, podZoneUsages, zoneNum), zoneNum)
			podsHPZoneMaxUsedReq = resutil.AddZoneResourceList(podsHPZoneMaxUsedReq,
				resutil.MaxZoneResourceList(podZoneUsages, podZoneRequests, zoneNum), zoneNum)
		} else {
			podsHPZoneUsed = resutil.AddZoneResourceList(podsHPZoneUsed, podZoneUsages, zoneNum)
			podsHPZoneMaxUsedReq = resutil.AddZoneResourceList(podsHPZoneMaxUsedReq,
				resutil.MaxZoneResourceList(podZoneUsages, podZoneRequests, zoneNum), zoneNum)
		}
	}

	// For the pods reported metrics but not shown in current list, count them according to the metric priority.
	for _, podMetric := range podMetricUnknownMap {
		if priority := podMetric.Priority; priority == extension.PriorityBatch || priority == extension.PriorityFree {
			continue
		}
		podNUMAUsage := resutil.GetPodUnknownNUMAUsage(resutil.GetPodMetricUsage(podMetric), zoneNum)
		podsUnknownUsed = resutil.AddZoneResourceList(podsUnknownUsed, podNUMAUsage, zoneNum)
	}
	podsHPZoneUsed = resutil.AddZoneResourceList(podsHPZoneUsed, podsUnknownUsed, zoneNum)
	podsHPZoneMaxUsedReq = resutil.AddZoneResourceList(podsHPZoneMaxUsedReq, podsUnknownUsed, zoneNum)

	batchZoneCPU := map[string]resource.Quantity{}
	batchZoneMemory := map[string]resource.Quantity{}
	var cpuMsg, memMsg string
	for i := range batchZoneAllocatable {
		zoneName := zoneIdxMap[i]
		batchZoneAllocatable[i], cpuMsg, memMsg = resutil.CalculateBatchResourceByPolicy(strategy, nodeZoneAllocatable[i],
			nodeZoneReserve[i], systemZoneReserved[i], systemZoneUsed[i],
			podsHPZoneRequested[i], podsHPZoneUsed[i], podsHPZoneMaxUsedReq[i])
		klog.V(6).InfoS("calculate batch resource in NUMA level", "node", node.Name, "zone", zoneName,
			"batch resource", batchZoneAllocatable[i], "cpu", cpuMsg, "memory", memMsg)

		batchZoneCPU[zoneName] = *resource.NewQuantity(batchZoneAllocatable[i].Cpu().MilliValue(), resource.DecimalSI)
		batchZoneMemory[zoneName] = *batchZoneAllocatable[i].Memory()
	}

	return batchZoneCPU, batchZoneMemory, nil
}

func (p *Plugin) isDegradeNeeded(strategy *configuration.ColocationStrategy, nodeMetric *slov1alpha1.NodeMetric, node *corev1.Node) bool {
	if nodeMetric == nil || nodeMetric.Status.UpdateTime == nil {
		klog.V(3).Infof("invalid NodeMetric: %v, need degradation", nodeMetric)
		return true
	}

	now := Clock.Now()
	if now.After(nodeMetric.Status.UpdateTime.Add(time.Duration(*strategy.DegradeTimeMinutes) * time.Minute)) {
		klog.V(3).Infof("timeout NodeMetric: %v, current timestamp: %v, metric last update timestamp: %v",
			nodeMetric.Name, now, nodeMetric.Status.UpdateTime)
		return true
	}

	return false
}

func (p *Plugin) degradeCalculate(node *corev1.Node, message string) []framework.ResourceItem {
	return p.Reset(node, message)
}

func (p *Plugin) prepareForNodeResourceTopology(strategy *configuration.ColocationStrategy, node *corev1.Node,
	nr *framework.NodeResource) error {
	if len(nr.ZoneResources) <= 0 {
		klog.V(6).Infof("skip prepare batch resources for NRT, Zone resources is not calculated, node %s", node.Name)
		return nil
	}

	var nrt *topologyv1alpha1.NodeResourceTopology
	err := util.RetryOnConflictOrTooManyRequests(func() error {
		nrt = &topologyv1alpha1.NodeResourceTopology{}
		err1 := client.Get(context.TODO(), types.NamespacedName{Name: node.Name}, nrt)
		if err1 != nil {
			if errors.IsNotFound(err1) {
				klog.V(6).Infof("skip prepare batch resources for NRT, NRT not found, node %s", node.Name)
				return nil
			}
			return err1
		}
		if !isNRTResourcesCreated(nrt) {
			klog.V(5).Infof("abort to prepare batch resources for NRT, resources %v not found, node %s",
				checkedNRTResourceSet.List(), node.Name)
			return nil
		}

		nrt = nrt.DeepCopy()
		needUpdate := p.updateNRTIfNeeded(strategy, node, nrt, nr)
		if !needUpdate {
			klog.V(6).Infof("skip prepare batch resources for NRT, ZoneList not changed, node %s", node.Name)
			return nil
		}

		return client.Update(context.TODO(), nrt)
	})
	if err != nil {
		metrics.RecordNodeResourceReconcileCount(false, "updateNodeResourceTopology")
		klog.V(4).InfoS("failed to update NodeResourceTopology for batch resources",
			"node", node.Name, "err", err)
		return err
	}
	nrtSyncContext.Store(util.GenerateNodeKey(&node.ObjectMeta), Clock.Now())
	metrics.RecordNodeResourceReconcileCount(true, "updateNodeResourceTopology")
	klog.V(6).InfoS("update NodeResourceTopology successfully for batch resources",
		"node", node.Name, "NRT", nrt)

	return nil
}

func (p *Plugin) updateNRTIfNeeded(strategy *configuration.ColocationStrategy, node *corev1.Node,
	nrt *topologyv1alpha1.NodeResourceTopology, nr *framework.NodeResource) bool {
	resourceDiffChanged := resutil.UpdateNRTZoneListIfNeeded(node, nrt.Zones, nr, *strategy.ResourceDiffThreshold)
	if resourceDiffChanged {
		klog.V(4).InfoS("NRT batch resources diff threshold reached, need sync",
			"node", node.Name, "diffThreshold", *strategy.ResourceDiffThreshold, "ZoneList", nrt.Zones)
		return true
	} else {
		klog.V(6).InfoS("skip updating NRT batch resources, since it differs too little",
			"node", node.Name, "ResourceDiffThreshold", *strategy.ResourceDiffThreshold)
	}

	// update time gap is bigger than UpdateTimeThresholdSeconds
	lastUpdatedTime, ok := nrtSyncContext.Load(util.GenerateNodeKey(&node.ObjectMeta))
	if !ok || Clock.Since(lastUpdatedTime) > time.Duration(*strategy.UpdateTimeThresholdSeconds)*time.Second {
		klog.V(4).InfoS("NRT batch resources expired, need sync", "node", node.Name)
		return true
	} else {
		klog.V(6).InfoS("skip updating NRT batch resources, since it's updated recently",
			"node", node.Name, "UpdateTimeThresholdSeconds", *strategy.UpdateTimeThresholdSeconds)
	}

	return false
}
