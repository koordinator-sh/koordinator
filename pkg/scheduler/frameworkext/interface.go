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

package frameworkext

import (
	"context"

	nrtinformers "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/workloadauditor"
)

// ExtendedHandle extends the k8s scheduling framework Handle interface
// to facilitate plugins to access Koordinator's resources and states.
type ExtendedHandle interface {
	fwktype.Handle
	// Scheduler return the scheduler adapter to support operating with cache and schedulingQueue.
	// NOTE: Plugins do not acquire a dispatcher instance during plugin initialization,
	// nor are they allowed to hold the object within the plugin object.
	Scheduler() Scheduler
	KoordinatorClientSet() koordinatorclientset.Interface
	KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory
	NodeResourceTopologyInformerFactory() nrtinformers.SharedInformerFactory
	// RegisterErrorHandlerFilters supports registering custom PreErrorHandlerFilter and PostErrorHandlerFilter to intercept scheduling errors.
	// If PreErrorHandlerFilter returns true, the k8s scheduler's default error handler and other handlers will not be called.
	// After handling scheduling errors, will execute PostErrorHandlerFilter, and if return true, other custom handlers will not be called.
	RegisterErrorHandlerFilters(preFilter PreErrorHandlerFilter, afterFilter PostErrorHandlerFilter)
	RegisterForgetPodHandler(handler ForgetPodHandler)
	ForgetPod(logger klog.Logger, pod *corev1.Pod) error
	// GetReservationCache returns the ReservationCache object to support cache operations for reservation infos.
	GetReservationCache() ReservationCache
	// GetReservationNominator returns the ReservationNominator object to support nominating reservation.
	// It returns nil when the framework does not support the resource reservation.
	GetReservationNominator() ReservationNominator
	GetNetworkTopologyTreeManager() networktopology.TreeManager
	// GetCrossSchedulerPodNominator returns the CrossSchedulerPodNominator for cross-scheduler nominated pod tracking.
	// It returns nil when the feature is not enabled or not configured.
	GetCrossSchedulerPodNominator() *CrossSchedulerPodNominator
	GetWorkloadAuditor() workloadauditor.WorkloadAuditor
}

// FrameworkExtender extends the K8s Scheduling Framework interface to provide more extension methods to support Koordinator.
type FrameworkExtender interface {
	framework.Framework
	ExtendedHandle

	// RunFindOneNodePlugin invokes the registered FindOneNodePlugin (if any) during the PreFilter phase.
	// The plugin's FindOneNode method attempts to deterministically select a single target node name for the given pod.
	// The normal Filter/Score cycle will still run as a validation step, but only against the single node returned by FindOneNode.
	// It returns the chosen node name and a Status. If no plugin is registered, or the plugin decides not to intervene,
	// the status will be fwktype.Skip and the scheduler falls back to the standard multi-node filtering flow.
	RunFindOneNodePlugin(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, result *fwktype.PreFilterResult) (string, *fwktype.Status)
	SetConfiguredPlugins(plugins *schedconfig.Plugins)

	RunReservationExtensionPreRestoreReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod) *fwktype.Status
	RunReservationExtensionRestoreReservation(ctx context.Context, cycleState fwktype.CycleState, podToSchedule *corev1.Pod, matched []*ReservationInfo, unmatched []*ReservationInfo, nodeInfo fwktype.NodeInfo) (PluginToReservationRestoreStates, *fwktype.Status)
	// RunReservationExtensionFinalRestoreReservation is deprecated, and will be removed next version.
	// DEPRECATED: use RunReservationExtensionRestoreReservation instead.
	RunReservationExtensionFinalRestoreReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, states PluginToNodeReservationRestoreStates) *fwktype.Status
	RunReservationExtensionPreRestoreReservationPreAllocation(ctx context.Context, cycleState fwktype.CycleState, rInfo *ReservationInfo) *fwktype.Status
	RunReservationExtensionRestoreReservationPreAllocation(ctx context.Context, cycleState fwktype.CycleState, rInfo *ReservationInfo, preAllocatable []*corev1.Pod, nodeInfo fwktype.NodeInfo) (PluginToReservationRestoreStates, *fwktype.Status)

	RunReservationFilterPlugins(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeInfo fwktype.NodeInfo) *fwktype.Status
	RunNominateReservationFilterPlugins(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeName string) *fwktype.Status
	RunReservationScorePlugins(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, reservationInfos []*ReservationInfo, nodeName string) (PluginToReservationScores, *fwktype.Status)
	RunReservationPreAllocationScorePlugins(ctx context.Context, cycleState fwktype.CycleState, rInfo *ReservationInfo, pods []*corev1.Pod, nodeName string) (PluginToReservationScores, *fwktype.Status)

	RunNUMATopologyManagerAdmit(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, node *corev1.Node, numaNodes []int, policyType apiext.NUMATopologyPolicy, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) *fwktype.Status

	RunResizePod(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status
}

// SchedulingTransformer is the parent type for all the custom transformer plugins.
type SchedulingTransformer interface {
	Name() string
}

// PreFilterTransformer is executed before and after PreFilter.
type PreFilterTransformer interface {
	SchedulingTransformer
	// BeforePreFilter If there is a change to the incoming Pod, it needs to be modified after DeepCopy and returned.
	BeforePreFilter(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod) (*corev1.Pod, bool, *fwktype.Status)
	// AfterPreFilter is executed after PreFilter.
	// There is a chance to trigger the correction of the State data of each plugin or do additional Node filtering after the PreFilter.
	AfterPreFilter(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, preFilterResult *fwktype.PreFilterResult) *fwktype.Status
}

type FindOneNodePluginProvider interface {
	FindOneNodePlugin() FindOneNodePlugin
}

// FindOneNodePlugin is responsible for finding a node for the pod according to calculated placement plans.
type FindOneNodePlugin interface {
	fwktype.Plugin
	// FindOneNode  This extension point is used to help the scheduler customize the Pod Filter and Score process
	FindOneNode(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, result *fwktype.PreFilterResult) (string, *fwktype.Status)
}

type PreferNodesPluginProvider interface {
	PreferNodesPlugin() PreferNodesPlugin
}

// PreferNodesPlugin is responsible to provide preferred nodes for the pod according to scheduling hints.
type PreferNodesPlugin interface {
	fwktype.Plugin
	// PreferNodes is used to provide preferred nodes for the scheduler to try scheduling first.
	PreferNodes(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, result *fwktype.PreFilterResult) ([]string, *fwktype.Status)
}

// FilterTransformer is executed before Filter.
type FilterTransformer interface {
	SchedulingTransformer
	BeforeFilter(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeInfo fwktype.NodeInfo) (*corev1.Pod, fwktype.NodeInfo, bool, *fwktype.Status)
}

// ScoreTransformer is executed before Score.
type ScoreTransformer interface {
	SchedulingTransformer
	BeforeScore(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeInfos []fwktype.NodeInfo) (*corev1.Pod, []fwktype.NodeInfo, bool, *fwktype.Status)
}

// PostFilterTransformer is executed before PostFilter.
type PostFilterTransformer interface {
	SchedulingTransformer
	AfterPostFilter(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, filteredNodeStatusMap fwktype.NodeToStatusReader)
}

// PluginToReservationRestoreStates declares a map from plugin name to its ReservationRestoreState.
type PluginToReservationRestoreStates map[string]interface{}

// PluginToNodeReservationRestoreStates declares a map from plugin name to its NodeReservationRestoreStates.
type PluginToNodeReservationRestoreStates map[string]NodeReservationRestoreStates

// NodeReservationRestoreStates declares a map from node name to its ReservationRestoreState.
type NodeReservationRestoreStates map[string]interface{}

// ReservationRestorePlugin is used to support the return of fine-grained resources
// held by Reservation, such as CPU Cores, GPU Devices, etc. During Pod scheduling, resources
// held by these reservations need to be allocated first, otherwise resources will be wasted.
type ReservationRestorePlugin interface {
	fwktype.Plugin
	PreRestoreReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod) *fwktype.Status
	RestoreReservation(ctx context.Context, cycleState fwktype.CycleState, podToSchedule *corev1.Pod, matched []*ReservationInfo, unmatched []*ReservationInfo, nodeInfo fwktype.NodeInfo) (interface{}, *fwktype.Status)
	// DEPRECATED
	FinalRestoreReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, states NodeReservationRestoreStates) *fwktype.Status
}

// ReservationPreAllocationRestorePlugin is used to support the return of fine-grained resources
// held by pre-allocatable pods, such as CPU Cores, GPU Devices, etc. During Pod scheduling, resources
// held by these pre-allocatable pods need to be allocated first, otherwise resources will be wasted.
type ReservationPreAllocationRestorePlugin interface {
	fwktype.Plugin
	PreRestoreReservationPreAllocation(ctx context.Context, cycleState fwktype.CycleState, r *ReservationInfo) *fwktype.Status
	RestoreReservationPreAllocation(ctx context.Context, cycleState fwktype.CycleState, r *ReservationInfo, preAllocatable []*corev1.Pod, nodeInfo fwktype.NodeInfo) (interface{}, *fwktype.Status)
}

// ReservationFilterPlugin is an interface for Filter Reservation plugins.
// FilterReservation will be called in the Filter phase for determining which reservations are available.
// FilterNominateReservation will be called in the PreScore or the Reserve phase to nominate a reservation whether it
// can participate the Reserve.
// TODO: Looking forward a merged method.
type ReservationFilterPlugin interface {
	fwktype.Plugin
	FilterReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeInfo fwktype.NodeInfo) *fwktype.Status
	FilterNominateReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeName string) *fwktype.Status
}

// ReservationNominator nominates a more suitable Reservation in the Reserve stage and Pod will bind this Reservation.
// The Reservation will be recorded in CycleState through SetNominatedReservation.
// When executing Reserve, each plugin will obtain the currently used Reservation through GetNominatedReservation,
// and locate the previously returned reusable resources for Pod allocation.
type ReservationNominator interface {
	fwktype.Plugin
	NominateReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) (*ReservationInfo, *fwktype.Status)
	AddNominatedReservation(pod *corev1.Pod, nodeName string, rInfo *ReservationInfo)
	// RemoveNominatedReservations is used to delete the nominated reserve pod.
	// DEPRECATED: use DeleteNominatedReservePodOrReservation instead.
	RemoveNominatedReservations(pod *corev1.Pod)
	// GetNominatedReservation returns the ReservationInfo of the nominated reservation assumed by the pod on the node.
	// NOTE: It returns the ReservationInfo from the cache instead of the object added to the nominator.
	GetNominatedReservation(pod *corev1.Pod, nodeName string) *ReservationInfo
	AddNominatedReservePod(reservePod *corev1.Pod, nodeName string)
	// DeleteNominatedReservePod is used to delete the nominated reserve pod.
	// DEPRECATED: use DeleteNominatedReservePodOrReservation instead.
	DeleteNominatedReservePod(reservePod *corev1.Pod)
	// NominatedReservePodForNode returns nominated reserve pods on the given node.
	NominatedReservePodForNode(nodeName string) []*framework.PodInfo
	NominatePreAllocation(ctx context.Context, cycleState fwktype.CycleState, rInfo *ReservationInfo, nodeName string) (*corev1.Pod, *fwktype.Status)
	AddNominatedPreAllocation(rInfo *ReservationInfo, nodeName string, pod *corev1.Pod)
	GetNominatedPreAllocation(rInfo *ReservationInfo, nodeName string) *corev1.Pod
	// ReservationNominate is used to nominate a pod for an available reservation or nominate a reservation for a pre-allocatable pod.
	ReservationNominate(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status
	// DeleteNominatedReservePodOrReservation is used to delete the nominated reserve pod or
	// the nominated reservation for the pod.
	DeleteNominatedReservePodOrReservation(pod *corev1.Pod)
	// AddNominatedPreAllocations nominates multiple pre-allocatable pods for a reservation.
	AddNominatedPreAllocations(rInfo *ReservationInfo, nodeName string, pods []*corev1.Pod)
	// GetNominatedPreAllocations returns the nominated pre-allocatable pods for a reservation.
	GetNominatedPreAllocations(rInfo *ReservationInfo, nodeName string) []*corev1.Pod
}

const (
	// MaxReservationScore is the maximum score a ReservationScorePlugin plugin is expected to return.
	MaxReservationScore int64 = 100

	// MinReservationScore is the minimum score a ReservationScorePlugin plugin is expected to return.
	MinReservationScore int64 = 0
)

// ReservationScoreList declares a list of reservations and their scores.
type ReservationScoreList []ReservationScore

// ReservationScore is a struct with reservation name and score.
type ReservationScore struct {
	Name      string
	Namespace string
	UID       types.UID
	Score     int64
}

// PluginToReservationScores declares a map from plugin name to its ReservationScoreList.
type PluginToReservationScores map[string]ReservationScoreList

// ReservationScorePlugin is an interface that must be implemented by "ScoreReservation" plugins to rank
// reservations that passed the reserve phase.
type ReservationScorePlugin interface {
	fwktype.Plugin
	ScoreReservation(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeName string) (int64, *fwktype.Status)
	// ReservationScoreExtensions returns a ReservationScoreExtensions interface if it implements one, or nil if does not.
	ReservationScoreExtensions() ReservationScoreExtensions
}

// ReservationScoreExtensions is an interface for Score extended functionality.
type ReservationScoreExtensions interface {
	// NormalizeReservationScore is called for all node scores produced by the same plugin's "ScoreReservation"
	// method. A successful run of NormalizeReservationScore will update the scores list and return
	// a success status.
	NormalizeReservationScore(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, scores ReservationScoreList) *fwktype.Status
}

// ResizePodPlugin is an interface that resize the pod resource spec before the Assume phase.
// If you want to use the feature, must enable the feature gate ResizePod=true
type ResizePodPlugin interface {
	fwktype.Plugin
	ResizePod(ctx context.Context, cycleState fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status
}

// ReservationPreBindPlugin performs special binding logic specifically for Reservation in the PreBind phase.
// Similar to the built-in VolumeBinding plugin of kube-scheduler, it does not support Reservation,
// and how Reservation itself uses PVC reserved resources also needs special handling.
// In addition, implementing this interface can clearly indicate that the plugin supports Reservation.
type ReservationPreBindPlugin interface {
	fwktype.Plugin
	PreBindReservation(ctx context.Context, cycleState fwktype.CycleState, reservation *schedulingv1alpha1.Reservation, nodeName string) *fwktype.Status
}

// PreBindExtensions is an extension to PreBind, which supports converting multiple modifications to the same object into a Patch operation.
// It supports configuring multiple plugin instances. A certain instance can be skipped if it does not need to be processed.
// Once a plugin instance returns success or failure, the process ends.
type PreBindExtensions interface {
	fwktype.Plugin
	ApplyPatch(ctx context.Context, cycleState fwktype.CycleState, originalObj, modifiedObj metav1.Object) *fwktype.Status
}

type NextPodPlugin interface {
	fwktype.Plugin
	// NextPod returns nil directly if NextPodPlugin has no suggestion for which Pod to dequeue next.
	NextPod() *corev1.Pod
}

type ForgetPodHandler func(pod *corev1.Pod)
