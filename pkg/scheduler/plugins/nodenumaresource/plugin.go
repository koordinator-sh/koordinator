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
	"errors"
	"fmt"

	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	topologylister "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/listers/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	resourceapi "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

const (
	Name     = "NodeNUMAResource"
	stateKey = Name
)

const (
	ErrNotFoundCPUTopology     = "node(s) CPU Topology not found"
	ErrInvalidCPUTopology      = "node(s) invalid CPU Topology"
	ErrSMTAlignmentError       = "node(s) requested cpus not multiple cpus per core"
	ErrRequiredFullPCPUsPolicy = "node(s) required FullPCPUs policy"
)

var (
	_ framework.EnqueueExtensions = &Plugin{}

	_ framework.PreFilterPlugin = &Plugin{}
	_ framework.FilterPlugin    = &Plugin{}
	_ framework.ScorePlugin     = &Plugin{}
	_ framework.ReservePlugin   = &Plugin{}
	_ framework.PreBindPlugin   = &Plugin{}

	_ frameworkext.ReservationRestorePlugin    = &Plugin{}
	_ frameworkext.ReservationPreBindPlugin    = &Plugin{}
	_ topologymanager.NUMATopologyHintProvider = &Plugin{}
)

type Plugin struct {
	handle          framework.Handle
	pluginArgs      *schedulingconfig.NodeNUMAResourceArgs
	nrtLister       topologylister.NodeResourceTopologyLister
	scorer          *resourceAllocationScorer
	resourceManager ResourceManager

	topologyOptionsManager TopologyOptionsManager
}

type Option func(*pluginOptions)

type pluginOptions struct {
	topologyOptionsManager TopologyOptionsManager
	resourceManager        ResourceManager
}

func WithTopologyOptionsManager(topologyOptionsManager TopologyOptionsManager) Option {
	return func(opts *pluginOptions) {
		opts.topologyOptionsManager = topologyOptionsManager
	}
}

func WithResourceManager(resourceManager ResourceManager) Option {
	return func(opts *pluginOptions) {
		opts.resourceManager = resourceManager
	}
}

func NewWithOptions(args runtime.Object, handle framework.Handle, opts ...Option) (framework.Plugin, error) {
	pluginArgs, ok := args.(*schedulingconfig.NodeNUMAResourceArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type NodeNUMAResourceArgs, got %T", args)
	}
	if err := validation.ValidateNodeNUMAResourceArgs(nil, pluginArgs); err != nil {
		return nil, err
	}
	if pluginArgs.ScoringStrategy == nil {
		return nil, fmt.Errorf("scoring strategy not specified")
	}
	strategy := pluginArgs.ScoringStrategy.Type
	scorePlugin, exists := resourceStrategyTypeMap[strategy]
	if !exists {
		return nil, fmt.Errorf("scoring strategy %s is not supported", strategy)
	}

	options := &pluginOptions{}
	for _, optFnc := range opts {
		optFnc(options)
	}

	if options.topologyOptionsManager == nil {
		options.topologyOptionsManager = NewTopologyOptionsManager()
	}

	if options.resourceManager == nil {
		defaultNUMAAllocateStrategy := GetDefaultNUMAAllocateStrategy(pluginArgs)
		options.resourceManager = NewResourceManager(handle, defaultNUMAAllocateStrategy, options.topologyOptionsManager)
	}

	nrtInformerFactory, err := initNRTInformerFactory(handle)
	if err != nil {
		return nil, err
	}
	if err := registerNodeResourceTopologyEventHandler(nrtInformerFactory, options.topologyOptionsManager); err != nil {
		return nil, err
	}
	registerPodEventHandler(handle, options.resourceManager)

	nrtLister := nrtInformerFactory.Topology().V1alpha1().NodeResourceTopologies().Lister()

	return &Plugin{
		handle:                 handle,
		pluginArgs:             pluginArgs,
		nrtLister:              nrtLister,
		scorer:                 scorePlugin(pluginArgs),
		resourceManager:        options.resourceManager,
		topologyOptionsManager: options.topologyOptionsManager,
	}, nil
}

func New(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	return NewWithOptions(args, handle)
}

func (p *Plugin) Name() string { return Name }

func (p *Plugin) GetResourceManager() ResourceManager {
	return p.resourceManager
}

func (p *Plugin) GetTopologyOptionsManager() TopologyOptionsManager {
	return p.topologyOptionsManager
}

type preFilterState struct {
	requests                    corev1.ResourceList
	requestCPUBind              bool
	resourceSpec                *extension.ResourceSpec
	preferredCPUBindPolicy      schedulingconfig.CPUBindPolicy
	preferredCPUExclusivePolicy schedulingconfig.CPUExclusivePolicy
	numCPUsNeeded               int
	allocation                  *PodAllocation
}

func (s *preFilterState) Clone() framework.StateData {
	ns := &preFilterState{
		requestCPUBind:              s.requestCPUBind,
		resourceSpec:                s.resourceSpec,
		preferredCPUBindPolicy:      s.preferredCPUBindPolicy,
		preferredCPUExclusivePolicy: s.preferredCPUExclusivePolicy,
		numCPUsNeeded:               s.numCPUsNeeded,
		allocation:                  s.allocation,
	}
	return ns
}

func getPreFilterState(cycleState *framework.CycleState) (*preFilterState, *framework.Status) {
	value, err := cycleState.Read(stateKey)
	if err != nil {
		return nil, framework.AsStatus(err)
	}
	state := value.(*preFilterState)
	return state, nil
}

func (p *Plugin) EventsToRegister() []framework.ClusterEvent {
	// To register a custom event, follow the naming convention at:
	// https://github.com/kubernetes/kubernetes/blob/e1ad9bee5bba8fbe85a6bf6201379ce8b1a611b1/pkg/scheduler/eventhandlers.go#L415-L422
	gvk := fmt.Sprintf("noderesourcetopologies.%v.%v", nrtv1alpha1.SchemeGroupVersion.Version, nrtv1alpha1.SchemeGroupVersion.Group)
	return []framework.ClusterEvent{
		{Resource: framework.GVK(gvk), ActionType: framework.Add | framework.Update | framework.Delete},
	}
}

func (p *Plugin) PreFilter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	resourceSpec, err := extension.GetResourceSpec(pod.Annotations)
	if err != nil {
		return nil, framework.NewStatus(framework.Error, err.Error())
	}

	requests, _ := resourceapi.PodRequestsAndLimits(pod)
	state := &preFilterState{
		requestCPUBind: false,
		requests:       requests,
	}
	if AllowUseCPUSet(pod) {
		preferredCPUBindPolicy := schedulingconfig.CPUBindPolicy(resourceSpec.PreferredCPUBindPolicy)
		if preferredCPUBindPolicy == "" || preferredCPUBindPolicy == schedulingconfig.CPUBindPolicyDefault {
			preferredCPUBindPolicy = p.pluginArgs.DefaultCPUBindPolicy
		}
		if preferredCPUBindPolicy == schedulingconfig.CPUBindPolicyFullPCPUs ||
			preferredCPUBindPolicy == schedulingconfig.CPUBindPolicySpreadByPCPUs {
			requestedCPU := requests.Cpu().MilliValue()
			if requestedCPU%1000 != 0 {
				return nil, framework.NewStatus(framework.Error, "the requested CPUs must be integer")
			}

			if requestedCPU > 0 {
				state.requestCPUBind = true
				state.resourceSpec = resourceSpec
				state.preferredCPUBindPolicy = preferredCPUBindPolicy
				state.preferredCPUExclusivePolicy = resourceSpec.PreferredCPUExclusivePolicy
				state.numCPUsNeeded = int(requestedCPU / 1000)
			}
		}
	}

	cycleState.Write(stateKey, state)
	topologymanager.InitStore(cycleState)
	return nil, nil
}

func (p *Plugin) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func (p *Plugin) Filter(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}

	node := nodeInfo.Node()
	topologyOptions := p.topologyOptionsManager.GetTopologyOptions(node.Name)
	if state.requestCPUBind {
		if topologyOptions.CPUTopology == nil {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrNotFoundCPUTopology)
		}

		// It's necessary to force node to have NodeResourceTopology and CPUTopology
		// We must satisfy the user's CPUSet request. Even if some nodes in the cluster have resources,
		// they cannot be allocated without valid CPU topology.
		if !topologyOptions.CPUTopology.IsValid() {
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrInvalidCPUTopology)
		}
		kubeletCPUPolicy := topologyOptions.Policy
		if extension.GetNodeCPUBindPolicy(node.Labels, kubeletCPUPolicy) == extension.NodeCPUBindPolicyFullPCPUsOnly {
			if state.numCPUsNeeded%topologyOptions.CPUTopology.CPUsPerCore() != 0 {
				return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrSMTAlignmentError)
			}
			if state.preferredCPUBindPolicy != schedulingconfig.CPUBindPolicyFullPCPUs {
				return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrRequiredFullPCPUsPolicy)
			}
		}
	}

	policyType := getNUMATopologyPolicy(node.Labels, topologyOptions.NUMATopologyPolicy)
	if policyType != extension.NUMATopologyPolicyNone {
		return p.FilterByNUMANode(ctx, cycleState, pod, node.Name, policyType)
	}

	return nil
}

func (p *Plugin) Reserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}

	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}
	node := nodeInfo.Node()
	topologyOptions := p.topologyOptionsManager.GetTopologyOptions(node.Name)
	if state.requestCPUBind {
		if topologyOptions.CPUTopology == nil {
			return framework.NewStatus(framework.Error, ErrNotFoundCPUTopology)
		}
		if !topologyOptions.CPUTopology.IsValid() {
			return framework.NewStatus(framework.Error, ErrInvalidCPUTopology)
		}
	}

	policyType := getNUMATopologyPolicy(node.Labels, topologyOptions.NUMATopologyPolicy)
	if policyType != extension.NUMATopologyPolicyNone {
		status := p.ReserveByNUMANode(ctx, cycleState, pod, nodeName, policyType)
		if !status.IsSuccess() {
			return status
		}
		return nil
	}

	// Only request CPUs by bind policy and exclusive policy.
	if !state.requestCPUBind {
		return nil
	}

	resourceOptions, err := p.getResourceOptions(cycleState, state, node, pod, topologymanager.NUMATopologyHint{})
	if err != nil {
		return framework.AsStatus(err)
	}
	result, err := p.resourceManager.Allocate(node, pod, resourceOptions)
	if err != nil {
		return framework.AsStatus(err)
	}
	p.resourceManager.Update(nodeName, result)
	state.allocation = result
	state.preferredCPUBindPolicy = resourceOptions.cpuBindPolicy
	return nil
}

func (p *Plugin) Unreserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return
	}
	if state.allocation != nil {
		p.resourceManager.Release(nodeName, pod.UID)
	}
}

func (p *Plugin) PreBind(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	return p.preBindObject(ctx, cycleState, pod, nodeName)
}

func (p *Plugin) PreBindReservation(ctx context.Context, cycleState *framework.CycleState, reservation *schedulingv1alpha1.Reservation, nodeName string) *framework.Status {
	return p.preBindObject(ctx, cycleState, reservation, nodeName)
}

func (p *Plugin) preBindObject(ctx context.Context, cycleState *framework.CycleState, object metav1.Object, nodeName string) *framework.Status {
	state, status := getPreFilterState(cycleState)
	if !status.IsSuccess() {
		return status
	}
	if state.allocation == nil {
		return nil
	}

	if state.requestCPUBind {
		if err := appendResourceSpecIfMissed(object, state); err != nil {
			return framework.AsStatus(err)
		}
	}

	resourceStatus := &extension.ResourceStatus{
		CPUSet: state.allocation.CPUSet.String(),
	}
	for _, nodeRes := range state.allocation.NUMANodeResources {
		resourceStatus.NUMANodeResources = append(resourceStatus.NUMANodeResources, extension.NUMANodeResource{
			Node:      int32(nodeRes.Node),
			Resources: nodeRes.Resources,
		})
	}
	if err := extension.SetResourceStatus(object, resourceStatus); err != nil {
		return framework.AsStatus(err)
	}
	return nil
}

func (p *Plugin) getResourceOptions(cycleState *framework.CycleState, state *preFilterState, node *corev1.Node, pod *corev1.Pod, affinity topologymanager.NUMATopologyHint) (*ResourceOptions, error) {
	preferredCPUBindPolicy, err := p.getPreferredCPUBindPolicy(node, state.preferredCPUBindPolicy)
	if err != nil {
		return nil, err
	}

	reservationReservedCPUs, err := p.getReservationReservedCPUs(cycleState, pod, node.Name)
	if err != nil {
		return nil, err
	}
	options := &ResourceOptions{
		requests:           state.requests,
		numCPUsNeeded:      state.numCPUsNeeded,
		requestCPUBind:     state.requestCPUBind,
		cpuBindPolicy:      preferredCPUBindPolicy,
		cpuExclusivePolicy: state.preferredCPUExclusivePolicy,
		preferredCPUs:      reservationReservedCPUs,
		hint:               affinity,
	}
	return options, nil
}

func (p *Plugin) getReservationReservedCPUs(cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) (cpuset.CPUSet, error) {
	var result cpuset.CPUSet
	if reservationutil.IsReservePod(pod) {
		return result, nil
	}
	nominatedReservation := frameworkext.GetNominatedReservation(cycleState, nodeName)
	if nominatedReservation == nil {
		return result, nil
	}

	allocatedCPUs, _ := p.resourceManager.GetAllocatedCPUSet(nodeName, nominatedReservation.UID())
	if allocatedCPUs.IsEmpty() {
		return result, nil
	}
	reservationRestoreState := getReservationRestoreState(cycleState)
	nodeReservationRestoreState := reservationRestoreState.getNodeState(nodeName)
	reservedCPUs := nodeReservationRestoreState.reservedCPUs[nominatedReservation.UID()]
	if !reservedCPUs.IsEmpty() && !reservedCPUs.IsSubsetOf(allocatedCPUs) {
		return result, fmt.Errorf("reservation reserved CPUs are invalid")
	}
	return reservedCPUs, nil
}

func appendResourceSpecIfMissed(object metav1.Object, state *preFilterState) error {
	annotations := object.GetAnnotations()
	// Write back ResourceSpec annotation if LSR Pod hasn't specified CPUBindPolicy
	preferredCPUBindPolicy := schedulingconfig.CPUBindPolicy(state.resourceSpec.PreferredCPUBindPolicy)
	if preferredCPUBindPolicy == "" ||
		preferredCPUBindPolicy == schedulingconfig.CPUBindPolicyDefault ||
		preferredCPUBindPolicy != state.preferredCPUBindPolicy {
		resourceSpec := &extension.ResourceSpec{
			PreferredCPUBindPolicy: extension.CPUBindPolicy(state.preferredCPUBindPolicy),
		}
		resourceSpecData, err := json.Marshal(resourceSpec)
		if err != nil {
			return err
		}
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[extension.AnnotationResourceSpec] = string(resourceSpecData)
		object.SetAnnotations(annotations)
	}
	return nil
}

func (p *Plugin) getPreferredCPUBindPolicy(node *corev1.Node, preferredCPUBindPolicy schedulingconfig.CPUBindPolicy) (schedulingconfig.CPUBindPolicy, error) {
	topologyOptions := p.topologyOptionsManager.GetTopologyOptions(node.Name)
	if topologyOptions.CPUTopology == nil {
		return preferredCPUBindPolicy, errors.New(ErrNotFoundCPUTopology)
	}
	if !topologyOptions.CPUTopology.IsValid() {
		return preferredCPUBindPolicy, errors.New(ErrInvalidCPUTopology)
	}

	kubeletCPUPolicy := topologyOptions.Policy
	nodeCPUBindPolicy := extension.GetNodeCPUBindPolicy(node.Labels, kubeletCPUPolicy)
	switch nodeCPUBindPolicy {
	default:
	case extension.NodeCPUBindPolicyNone:
	case extension.NodeCPUBindPolicySpreadByPCPUs:
		preferredCPUBindPolicy = schedulingconfig.CPUBindPolicySpreadByPCPUs
	case extension.NodeCPUBindPolicyFullPCPUsOnly:
		preferredCPUBindPolicy = schedulingconfig.CPUBindPolicyFullPCPUs
	}
	return preferredCPUBindPolicy, nil
}
