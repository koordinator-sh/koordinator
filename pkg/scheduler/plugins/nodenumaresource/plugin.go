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

package nodenumaresource

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

const (
	Name     = "NodeNUMAResource"
	stateKey = Name
)

const (
	// socketScoreWeight controls the range of the final score when scoring according to the NUMA Socket dimension.
	// the NUMA Socket dimension score formulas: nodeFinalScore = math.Log(numaSocketFinalScore) * socketScoreWeight
	// use the prime number 7, we can get the range of the final score = [0, 32.23]
	socketScoreWeight = 7
)

const (
	ErrMissingNodeResourceTopology = "node(s) missing NodeResourceTopology"
	ErrInvalidCPUTopology          = "node(s) invalid CPU Topology"
)

var (
	_ framework.PreFilterPlugin = &Plugin{}
	_ framework.FilterPlugin    = &Plugin{}
	_ framework.ScorePlugin     = &Plugin{}
	_ framework.ReservePlugin   = &Plugin{}
	_ framework.PreBindPlugin   = &Plugin{}
)

type Plugin struct {
	handle        framework.Handle
	pluginArgs    *schedulingconfig.NodeNUMAResourceArgs
	nodeInfoCache *nodeNumaInfoCache
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	pluginArgs, ok := args.(*schedulingconfig.NodeNUMAResourceArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type LoadAwareSchedulingArgs, got %T", args)
	}

	extendedHandle, ok := handle.(frameworkext.ExtendedHandle)
	if !ok {
		return nil, fmt.Errorf("want handle to be of type frameworkext.ExtendedHandle, got %T", handle)
	}

	numaInfoCache := newNodeNUMAInfoCache()
	podInformer := extendedHandle.SharedInformerFactory().Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    numaInfoCache.onPodAdd,
		UpdateFunc: numaInfoCache.onPodUpdate,
		DeleteFunc: numaInfoCache.onPodDelete,
	})

	nodeResTopologyInformer := extendedHandle.NodeResourceTopologySharedInformerFactory().Topology().V1alpha1().NodeResourceTopologies().Informer()
	nodeResTopologyInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    numaInfoCache.onNodeResourceTopologyAdd,
		UpdateFunc: numaInfoCache.onNodeResourceTopologyUpdate,
		DeleteFunc: numaInfoCache.onNodeResourceTopologyDelete,
	})

	return &Plugin{
		handle:        handle,
		pluginArgs:    pluginArgs,
		nodeInfoCache: numaInfoCache,
	}, nil
}

func (p *Plugin) Name() string { return Name }

type preFilterState struct {
	skip                        bool
	resourceSpec                *extension.ResourceSpec
	preferredCPUBindPolicy      schedulingconfig.CPUBindPolicy
	preferredCPUExclusivePolicy schedulingconfig.CPUExclusivePolicy
	numCPUsNeeded               int
	allocatedCPUs               CPUSet
}

func (s *preFilterState) Clone() framework.StateData {
	return &preFilterState{
		skip:          s.skip,
		resourceSpec:  s.resourceSpec,
		allocatedCPUs: s.allocatedCPUs.Clone(),
	}
}

func (p *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	resourceSpec, err := extension.GetResourceSpec(pod.Annotations)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	state := &preFilterState{
		skip: true,
	}

	qosClass := extension.GetPodQoSClass(pod)
	priorityClass := extension.GetPriorityClass(pod)
	if (qosClass == extension.QoSLSE || qosClass == extension.QoSLSR) && priorityClass == extension.PriorityProd {
		preferredCPUBindPolicy := resourceSpec.PreferredCPUBindPolicy
		if preferredCPUBindPolicy == "" || preferredCPUBindPolicy == schedulingconfig.CPUBindPolicyDefault {
			preferredCPUBindPolicy = p.pluginArgs.DefaultCPUBindPolicy
		}
		if preferredCPUBindPolicy == schedulingconfig.CPUBindPolicyFullPCPUs ||
			preferredCPUBindPolicy == schedulingconfig.CPUBindPolicySpreadByPCPUs {
			requests, _ := resourceapi.PodRequestsAndLimits(pod)
			requestedCPU := requests.Cpu().MilliValue()
			if requestedCPU%1000 != 0 {
				return framework.NewStatus(framework.Error, "the requested CPUs must be integer")
			}

			if requestedCPU > 0 {
				state.skip = false
				state.resourceSpec = resourceSpec
				state.preferredCPUBindPolicy = preferredCPUBindPolicy
				state.preferredCPUExclusivePolicy = resourceSpec.PreferredCPUExclusivePolicy
				state.numCPUsNeeded = int(requestedCPU / 1000)
			}
		}
	}

	cycleState.Write(stateKey, state)
	return nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func getPreFilterState(cycleState *framework.CycleState) (*preFilterState, *framework.Status) {
	value, err := cycleState.Read(stateKey)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	state := value.(*preFilterState)
	return state, nil
}

func (p *Plugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	// It's necessary to force node to have NodeResourceTopology and CPUTopology
	// We must satisfy the user's CPUSet request. Even if some nodes in the cluster have resources,
	// they cannot be allocated without valid CPU topology.
	numaInfo := p.nodeInfoCache.getNodeNUMAInfo(node.Name)
	if numaInfo == nil {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrMissingNodeResourceTopology)
	}

	numaInfo.lock.Lock()
	defer numaInfo.lock.Unlock()
	if !numaInfo.cpuTopology.IsValid() {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrInvalidCPUTopology)
	}

	return nil
}

func (p *Plugin) Score(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return 0, status
	}
	if state.skip {
		return 0, nil
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}
	node := nodeInfo.Node()
	if node == nil {
		return 0, framework.NewStatus(framework.Error, "node not found")
	}

	// There is no need to force nodes to have a NodeResourceTopology during the scoring phase.
	numaInfo := p.nodeInfoCache.getNodeNUMAInfo(nodeName)
	if numaInfo == nil {
		return 0, nil
	}

	numaInfo.lock.Lock()
	defer numaInfo.lock.Unlock()
	if !numaInfo.cpuTopology.IsValid() {
		return 0, nil
	}

	score := p.calcScore(state.numCPUsNeeded, state.preferredCPUBindPolicy, state.preferredCPUExclusivePolicy, numaInfo)
	return score, nil
}

func (p *Plugin) calcScore(numCPUsNeeded int, cpuBindPolicy schedulingconfig.CPUBindPolicy, cpuExclusivePolicy schedulingconfig.CPUExclusivePolicy, numaInfo *nodeNUMAInfo) int64 {
	availableCPUs, allocated := getAvailableCPUsFunc(numaInfo)
	acc := newCPUAccumulator(
		numaInfo.cpuTopology,
		availableCPUs,
		allocated,
		numCPUsNeeded,
		cpuExclusivePolicy,
		p.pluginArgs.NUMAAllocateStrategy,
	)

	var freeCPUs [][]int
	if cpuBindPolicy == schedulingconfig.CPUBindPolicyFullPCPUs {
		if numCPUsNeeded <= numaInfo.cpuTopology.CPUsPerNode() {
			freeCPUs = acc.freeCoresInNode(true, true)
		} else if numCPUsNeeded <= numaInfo.cpuTopology.CPUsPerSocket() {
			freeCPUs = acc.freeCoresInSocket(true)
		}
	} else {
		if numCPUsNeeded <= numaInfo.cpuTopology.CPUsPerNode() {
			freeCPUs = acc.freeCPUsInNode(true)
		} else if numCPUsNeeded <= numaInfo.cpuTopology.CPUsPerSocket() {
			freeCPUs = acc.freeCPUsInSocket(true)
		}
	}

	scoreFn := mostRequestedScore
	if p.pluginArgs.ScoringStrategy != nil && p.pluginArgs.ScoringStrategy.Type == schedulingconfig.LeastAllocated {
		scoreFn = leastRequestedScore
	}

	var maxScore int64
	for _, cpus := range freeCPUs {
		if len(cpus) < numCPUsNeeded {
			continue
		}

		numaScore := scoreFn(int64(numCPUsNeeded), int64(len(cpus)))
		if numaScore > maxScore {
			maxScore = numaScore
		}
	}

	// If the requested CPUs can be aligned according to NUMA Socket, it should be scored,
	// but in order to avoid the situation where the number of CPUs in the NUMA Socket of
	// some special models in the cluster is equal to the number of CPUs in the NUMA Node
	// of other models, it is necessary to reduce the weight of the score of such machines.
	if numCPUsNeeded > numaInfo.cpuTopology.CPUsPerNode() && numCPUsNeeded <= numaInfo.cpuTopology.CPUsPerSocket() {
		maxScore = int64(math.Ceil(math.Log(float64(maxScore)) * socketScoreWeight))
	}

	return maxScore
}

// The used capacity is calculated on a scale of 0-MaxNodeScore (MaxNodeScore is
// constant with value set to 100).
// 0 being the lowest priority and 100 being the highest.
// The more resources are used the higher the score is. This function
// is almost a reversed version of leastRequestedScore.
func mostRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return (requested * framework.MaxNodeScore) / capacity
}

// The unused capacity is calculated on a scale of 0-MaxNodeScore
// 0 being the lowest priority and `MaxNodeScore` being the highest.
// The more unused resources the higher the score is.
func leastRequestedScore(requested, capacity int64) int64 {
	if capacity == 0 {
		return 0
	}
	if requested > capacity {
		return 0
	}

	return ((capacity - requested) * framework.MaxNodeScore) / capacity
}

func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (p *Plugin) Reserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	// The Pod requires the CPU to be allocated according to CPUBindPolicy,
	// but the current node does not have a NodeResourceTopology or a valid CPUTopology,
	// so this error should be exposed to the user
	numaInfo := p.nodeInfoCache.getNodeNUMAInfo(nodeName)
	if numaInfo == nil {
		return framework.NewStatus(framework.Error, ErrMissingNodeResourceTopology)
	}

	numaInfo.lock.Lock()
	defer numaInfo.lock.Unlock()
	if !numaInfo.cpuTopology.IsValid() {
		return framework.NewStatus(framework.Error, ErrInvalidCPUTopology)
	}

	availableCPUs, allocated := getAvailableCPUsFunc(numaInfo)
	result, err := takeCPUs(
		numaInfo.cpuTopology,
		availableCPUs,
		allocated,
		state.numCPUsNeeded,
		state.preferredCPUBindPolicy,
		state.resourceSpec.PreferredCPUExclusivePolicy,
		p.pluginArgs.NUMAAllocateStrategy,
	)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	numaInfo.allocateCPUs(pod.UID, result, state.preferredCPUExclusivePolicy)
	state.allocatedCPUs = result
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return
	}
	if state.skip {
		return
	}

	numaInfo := p.nodeInfoCache.getNodeNUMAInfo(nodeName)
	if numaInfo == nil {
		return
	}

	numaInfo.lock.Lock()
	defer numaInfo.lock.Unlock()
	if !numaInfo.cpuTopology.IsValid() {
		return
	}
	numaInfo.releaseCPUs(pod.UID, state.allocatedCPUs)
	state.allocatedCPUs = NewCPUSet()
}

func (p *Plugin) PreBind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.skip {
		return nil
	}

	resourceStatus := &extension.ResourceStatus{
		CPUSet: state.allocatedCPUs.String(),
	}
	data, err := json.Marshal(resourceStatus)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	podOriginal, err := p.handle.SharedInformerFactory().Core().V1().Pods().Lister().Pods(pod.Namespace).Get(pod.Name)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	pod = podOriginal.DeepCopy()
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[extension.AnnotationResourceStatus] = string(data)

	// Write back ResourceSpec annotation if LSR Pod hasn't specified CPUBindPolicy
	if state.resourceSpec.PreferredCPUBindPolicy == "" ||
		state.resourceSpec.PreferredCPUBindPolicy == schedulingconfig.CPUBindPolicyDefault {
		resourceSpec := &extension.ResourceSpec{
			PreferredCPUBindPolicy: p.pluginArgs.DefaultCPUBindPolicy,
		}
		data, err = json.Marshal(resourceSpec)
		if err != nil {
			return framework.NewStatus(framework.Error, err.Error())
		}
		pod.Annotations[extension.AnnotationResourceSpec] = string(data)
	}

	patchBytes, err := generatePodPatch(podOriginal, pod)
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}
	if string(patchBytes) == "{}" {
		return nil
	}

	err = retry.OnError(
		retry.DefaultRetry,
		errors.IsTooManyRequests,
		func() error {
			_, err := p.handle.ClientSet().CoreV1().Pods(pod.Namespace).
				Patch(ctx, pod.Name, apimachinerytypes.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
			if err != nil {
				klog.Error("Failed to patch Pod %s/%s, patch: %v, err: %v", pod.Namespace, pod.Name, string(patchBytes), err)
			}
			return err
		})
	if err != nil {
		return framework.NewStatus(framework.Error, err.Error())
	}

	klog.V(4).Infof("Successfully preBind Pod %s/%s with CPUSet %s", pod.Namespace, pod.Name, resourceStatus.CPUSet)
	return nil
}
