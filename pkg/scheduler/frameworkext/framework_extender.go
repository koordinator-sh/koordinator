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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	schedconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/metrics"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/schedulingphase"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var _ FrameworkExtender = &frameworkExtenderImpl{}
var _ topologymanager.NUMATopologyHintProviderFactory = &frameworkExtenderImpl{}

type frameworkExtenderImpl struct {
	framework.Framework
	*errorHandlerDispatcher
	forgetPodHandlers []ForgetPodHandler

	schedulerFn       func() Scheduler
	configuredPlugins *schedconfig.Plugins
	monitor           *SchedulerMonitor

	koordinatorClientSet             koordinatorclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory

	preFilterTransformers        map[string]PreFilterTransformer
	filterTransformers           map[string]FilterTransformer
	scoreTransformers            map[string]ScoreTransformer
	preFilterTransformersEnabled []PreFilterTransformer
	filterTransformersEnabled    []FilterTransformer
	scoreTransformersEnabled     []ScoreTransformer

	reservationNominator      ReservationNominator
	reservationFilterPlugins  []ReservationFilterPlugin
	reservationScorePlugins   []ReservationScorePlugin
	reservationPreBindPlugins []ReservationPreBindPlugin
	reservationRestorePlugins []ReservationRestorePlugin

	resizePodPlugins         []ResizePodPlugin
	preBindExtensionsPlugins map[string]PreBindExtensions

	numaTopologyHintProviders []topologymanager.NUMATopologyHintProvider
	topologyManager           topologymanager.Interface

	metricsRecorder *metrics.MetricAsyncRecorder
}

func NewFrameworkExtender(f *FrameworkExtenderFactory, fw framework.Framework) FrameworkExtender {
	schedulerFn := func() Scheduler {
		return f.Scheduler()
	}

	frameworkExtender := &frameworkExtenderImpl{
		Framework:                        fw,
		errorHandlerDispatcher:           f.errorHandlerDispatcher,
		schedulerFn:                      schedulerFn,
		monitor:                          f.monitor,
		koordinatorClientSet:             f.KoordinatorClientSet(),
		koordinatorSharedInformerFactory: f.koordinatorSharedInformerFactory,
		reservationNominator:             f.reservationNominator,
		preFilterTransformers:            map[string]PreFilterTransformer{},
		filterTransformers:               map[string]FilterTransformer{},
		scoreTransformers:                map[string]ScoreTransformer{},
		preBindExtensionsPlugins:         map[string]PreBindExtensions{},
		metricsRecorder:                  metrics.NewMetricsAsyncRecorder(1000, time.Second, wait.NeverStop),
	}
	frameworkExtender.topologyManager = topologymanager.New(frameworkExtender)
	return frameworkExtender
}

func (ext *frameworkExtenderImpl) updateTransformer(transformers ...SchedulingTransformer) {
	for _, transformer := range transformers {
		preFilterTransformer, ok := transformer.(PreFilterTransformer)
		if ok {
			ext.preFilterTransformers[transformer.Name()] = preFilterTransformer
			klog.V(4).InfoS("framework extender got scheduling transformer registered", "preFilter", preFilterTransformer.Name())
		}
		filterTransformer, ok := transformer.(FilterTransformer)
		if ok {
			ext.filterTransformers[transformer.Name()] = filterTransformer
			klog.V(4).InfoS("framework extender got scheduling transformer registered", "filter", filterTransformer.Name())
		}
		scoreTransformer, ok := transformer.(ScoreTransformer)
		if ok {
			ext.scoreTransformers[transformer.Name()] = scoreTransformer
			klog.V(4).InfoS("framework extender got scheduling transformer registered", "score", scoreTransformer.Name())
		}
	}
}

func (ext *frameworkExtenderImpl) updatePlugins(pl framework.Plugin) {
	if transformer, ok := pl.(SchedulingTransformer); ok {
		ext.updateTransformer(transformer)
	}
	// TODO(joseph): In the future, use only the default ReservationNominator
	if r, ok := pl.(ReservationNominator); ok {
		ext.reservationNominator = r
	}
	if r, ok := pl.(ReservationFilterPlugin); ok {
		ext.reservationFilterPlugins = append(ext.reservationFilterPlugins, r)
	}
	if r, ok := pl.(ReservationScorePlugin); ok {
		ext.reservationScorePlugins = append(ext.reservationScorePlugins, r)
	}
	if r, ok := pl.(ReservationPreBindPlugin); ok {
		ext.reservationPreBindPlugins = append(ext.reservationPreBindPlugins, r)
	}
	if r, ok := pl.(ReservationRestorePlugin); ok {
		ext.reservationRestorePlugins = append(ext.reservationRestorePlugins, r)
	}
	if r, ok := pl.(ResizePodPlugin); ok {
		ext.resizePodPlugins = append(ext.resizePodPlugins, r)
	}
	if p, ok := pl.(PreBindExtensions); ok {
		ext.preBindExtensionsPlugins[p.Name()] = p
	}
	if p, ok := pl.(topologymanager.NUMATopologyHintProvider); ok {
		ext.numaTopologyHintProviders = append(ext.numaTopologyHintProviders, p)
	}
}

func (ext *frameworkExtenderImpl) SetConfiguredPlugins(plugins *schedconfig.Plugins) {
	ext.configuredPlugins = plugins

	for _, pl := range ext.configuredPlugins.PreFilter.Enabled {
		transformer := ext.preFilterTransformers[pl.Name]
		if transformer != nil {
			ext.preFilterTransformersEnabled = append(ext.preFilterTransformersEnabled, transformer)
		}
	}
	for _, pl := range ext.configuredPlugins.Filter.Enabled {
		transformer := ext.filterTransformers[pl.Name]
		if transformer != nil {
			ext.filterTransformersEnabled = append(ext.filterTransformersEnabled, transformer)
		}
	}
	for _, pl := range ext.configuredPlugins.Score.Enabled {
		transformer := ext.scoreTransformers[pl.Name]
		if transformer != nil {
			ext.scoreTransformersEnabled = append(ext.scoreTransformersEnabled, transformer)
		}
	}
	klog.V(5).InfoS("Set configured transformer plugins",
		"PreFilterTransformer", len(ext.preFilterTransformersEnabled),
		"FilterTransformer", len(ext.filterTransformersEnabled),
		"ScoreTransformer", len(ext.scoreTransformersEnabled))
}

func (ext *frameworkExtenderImpl) KoordinatorClientSet() koordinatorclientset.Interface {
	return ext.koordinatorClientSet
}

func (ext *frameworkExtenderImpl) KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory {
	return ext.koordinatorSharedInformerFactory
}

// Scheduler return the scheduler adapter to support operating with cache and schedulingQueue.
// NOTE: Plugins do not acquire a dispatcher instance during plugin initialization,
// nor are they allowed to hold the object within the plugin object.
func (ext *frameworkExtenderImpl) Scheduler() Scheduler {
	return ext.schedulerFn()
}

func (ext *frameworkExtenderImpl) GetReservationNominator() ReservationNominator {
	return ext.reservationNominator
}

// RunPreFilterPlugins transforms the PreFilter phase of framework with pre-filter transformers.
func (ext *frameworkExtenderImpl) RunPreFilterPlugins(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) (*framework.PreFilterResult, *framework.Status) {
	for _, transformer := range ext.preFilterTransformersEnabled {
		startTime := time.Now()
		newPod, transformed, status := transformer.BeforePreFilter(ctx, cycleState, pod)
		ext.metricsRecorder.ObservePluginDurationAsync("BeforePreFilter", transformer.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
		if !status.IsSuccess() {
			status.SetFailedPlugin(transformer.Name())
			klog.ErrorS(status.AsError(), "Failed to run BeforePreFilter", "pod", klog.KObj(pod), "plugin", transformer.Name())
			return nil, status
		}
		if transformed {
			klog.V(5).InfoS("BeforePreFilter transformed", "transformer", transformer.Name(), "pod", klog.KObj(pod))
			pod = newPod
		}
	}

	result, status := ext.Framework.RunPreFilterPlugins(ctx, cycleState, pod)
	if !status.IsSuccess() {
		return result, status
	}

	for _, transformer := range ext.preFilterTransformersEnabled {
		startTime := time.Now()
		status = transformer.AfterPreFilter(ctx, cycleState, pod, result)
		ext.metricsRecorder.ObservePluginDurationAsync("AfterPreFilter", transformer.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
		if !status.IsSuccess() {
			status.SetFailedPlugin(transformer.Name())
			klog.ErrorS(status.AsError(), "Failed to run AfterPreFilter", "pod", klog.KObj(pod), "plugin", transformer.Name())
			return nil, status
		}
	}
	return result, nil
}

// RunFilterPluginsWithNominatedPods transforms the Filter phase of framework with filter transformers.
// We don't transform RunFilterPlugins since framework's RunFilterPluginsWithNominatedPods just calls its RunFilterPlugins.
func (ext *frameworkExtenderImpl) RunFilterPluginsWithNominatedPods(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	for _, transformer := range ext.filterTransformersEnabled {
		startTime := time.Now()
		newPod, newNodeInfo, transformed, status := transformer.BeforeFilter(ctx, cycleState, pod, nodeInfo)
		ext.metricsRecorder.ObservePluginDurationAsync("BeforeFilter", transformer.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
		if !status.IsSuccess() {
			status.SetFailedPlugin(transformer.Name())
			klog.ErrorS(status.AsError(), "Failed to run BeforeFilter", "pod", klog.KObj(pod), "plugin", transformer.Name())
			return status
		}
		if transformed {
			klog.V(5).InfoS("BeforeFilter transformed", "transformer", transformer.Name(), "pod", klog.KObj(pod))
			pod = newPod
			nodeInfo = newNodeInfo
		}
	}
	status := ext.Framework.RunFilterPluginsWithNominatedPods(ctx, cycleState, pod, nodeInfo)
	if !status.IsSuccess() && debugFilterFailure {
		klog.Infof("Failed to filter for Pod %q on Node %q, failedPlugin: %s, reason: %s", klog.KObj(pod), klog.KObj(nodeInfo.Node()), status.FailedPlugin(), status.Message())
	}
	return status
}

func (ext *frameworkExtenderImpl) RunScorePlugins(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodes []*corev1.Node) ([]framework.NodePluginScores, *framework.Status) {
	for _, transformer := range ext.scoreTransformersEnabled {
		startTime := time.Now()
		newPod, newNodes, transformed, status := transformer.BeforeScore(ctx, state, pod, nodes)
		ext.metricsRecorder.ObservePluginDurationAsync("BeforeScore", transformer.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
		if !status.IsSuccess() {
			klog.ErrorS(status.AsError(), "Failed to run BeforeScore", "pod", klog.KObj(pod), "plugin", transformer.Name())
			return nil, status
		}
		if transformed {
			klog.V(5).InfoS("BeforeScore transformed", "transformer", transformer.Name(), "pod", klog.KObj(pod))
			pod = newPod
			nodes = newNodes
		}
	}
	pluginToNodeScores, status := ext.Framework.RunScorePlugins(ctx, state, pod, nodes)
	if status.IsSuccess() && debugTopNScores > 0 {
		debugScores(debugTopNScores, pod, pluginToNodeScores, nodes)
	}
	return pluginToNodeScores, status
}

func (ext *frameworkExtenderImpl) RunPostFilterPlugins(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, filteredNodeStatusMap framework.NodeToStatusMap) (_ *framework.PostFilterResult, status *framework.Status) {
	schedulingphase.RecordPhase(state, schedulingphase.PostFilter)
	defer func() { schedulingphase.RecordPhase(state, "") }()
	return ext.Framework.RunPostFilterPlugins(ctx, state, pod, filteredNodeStatusMap)
}

// RunPreBindPlugins supports PreBindReservation for Reservation
func (ext *frameworkExtenderImpl) RunPreBindPlugins(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if !reservationutil.IsReservePod(pod) {
		original := pod
		pod = pod.DeepCopy()
		status := ext.Framework.RunPreBindPlugins(ctx, state, pod, nodeName)
		if !status.IsSuccess() {
			return status
		}
		return ext.runPreBindExtensionPlugins(ctx, state, original, pod)
	}

	reservationLister := ext.koordinatorSharedInformerFactory.Scheduling().V1alpha1().Reservations().Lister()
	rName := reservationutil.GetReservationNameFromReservePod(pod)
	reservation, err := reservationLister.Get(rName)
	if err != nil {
		return framework.AsStatus(err)
	}

	original := reservation
	reservation = reservation.DeepCopy()
	reservation.Status.NodeName = nodeName
	for _, pl := range ext.reservationPreBindPlugins {
		startTime := time.Now()
		status := pl.PreBindReservation(ctx, state, reservation, nodeName)
		ext.metricsRecorder.ObservePluginDurationAsync("PreBindReservation", pl.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
		if !status.IsSuccess() {
			status.SetFailedPlugin(pl.Name())
			err := status.AsError()
			klog.ErrorS(err, "Failed running ReservationPreBindPlugin plugin", "plugin", pl.Name(), "reservation", klog.KObj(reservation))
			return framework.AsStatus(fmt.Errorf("running ReservationPreBindPlugin plugin %q: %w", pl.Name(), err))
		}
	}
	return ext.runPreBindExtensionPlugins(ctx, state, original, reservation)
}

func (ext *frameworkExtenderImpl) runPreBindExtensionPlugins(ctx context.Context, cycleState *framework.CycleState, originalObj, modifiedObj metav1.Object) *framework.Status {
	plugins := ext.configuredPlugins
	for _, plugin := range plugins.PreBind.Enabled {
		pl := ext.preBindExtensionsPlugins[plugin.Name]
		if pl == nil {
			continue
		}
		status := pl.ApplyPatch(ctx, cycleState, originalObj, modifiedObj)
		if status != nil && status.Code() == framework.Skip {
			continue
		}
		if !status.IsSuccess() {
			err := status.AsError()
			status.SetFailedPlugin(pl.Name())
			klog.ErrorS(err, "Failed running PreBindExtension plugin", "plugin", pl.Name(), "pod", klog.KObj(originalObj))
			return framework.AsStatus(fmt.Errorf("running PreBindExtension plugin %q: %w", pl.Name(), err))
		}
		return status
	}
	return nil
}

func (ext *frameworkExtenderImpl) RunPostBindPlugins(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	if ext.monitor != nil {
		defer ext.monitor.Complete(pod, nil)
	}
	ext.Framework.RunPostBindPlugins(ctx, state, pod, nodeName)
}

func (ext *frameworkExtenderImpl) RunReservationExtensionPreRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	for _, pl := range ext.reservationRestorePlugins {
		status := pl.PreRestoreReservation(ctx, cycleState, pod)
		if !status.IsSuccess() {
			klog.ErrorS(status.AsError(), "Failed running PreRestoreReservation on plugin", "plugin", pl.Name(), "pod", klog.KObj(pod))
			return status
		}
	}
	return nil
}

// RunReservationExtensionRestoreReservation restores the Reservation during PreFilter phase
func (ext *frameworkExtenderImpl) RunReservationExtensionRestoreReservation(ctx context.Context, cycleState *framework.CycleState, podToSchedule *corev1.Pod, matched []*ReservationInfo, unmatched []*ReservationInfo, nodeInfo *framework.NodeInfo) (PluginToReservationRestoreStates, *framework.Status) {
	var pluginToRestoreState PluginToReservationRestoreStates
	for _, pl := range ext.reservationRestorePlugins {
		state, status := pl.RestoreReservation(ctx, cycleState, podToSchedule, matched, unmatched, nodeInfo)
		if !status.IsSuccess() {
			klog.ErrorS(status.AsError(), "Failed running RestoreReservation on plugin", "plugin", pl.Name(), "pod", klog.KObj(podToSchedule))
			return nil, status
		}
		if pluginToRestoreState == nil {
			pluginToRestoreState = PluginToReservationRestoreStates{}
		}
		pluginToRestoreState[pl.Name()] = state
	}
	return pluginToRestoreState, nil
}

func (ext *frameworkExtenderImpl) RunReservationExtensionFinalRestoreReservation(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, states PluginToNodeReservationRestoreStates) *framework.Status {
	for _, pl := range ext.reservationRestorePlugins {
		s, ok := states[pl.Name()]
		if !ok {
			continue
		}
		status := pl.FinalRestoreReservation(ctx, cycleState, pod, s)
		if !status.IsSuccess() {
			klog.ErrorS(status.AsError(), "Failed running FinalRestoreReservation on plugin", "plugin", pl.Name(), "pod", klog.KObj(pod))
			return status
		}
	}
	return nil
}

// RunReservationFilterPlugins determines whether the Reservation can participate in the Reserve
func (ext *frameworkExtenderImpl) RunReservationFilterPlugins(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeInfo *framework.NodeInfo) *framework.Status {
	for _, pl := range ext.reservationFilterPlugins {
		status := pl.FilterReservation(ctx, cycleState, pod, reservationInfo, nodeInfo)
		if !status.IsSuccess() {
			if debugFilterFailure {
				klog.Infof("Failed to FilterWithReservation for Pod %q with Reservation %q on Node %q, failedPlugin: %s, reason: %s", klog.KObj(pod), klog.KObj(reservationInfo), nodeInfo.Node().Name, pl.Name(), status.Message())
			}
			return status
		}
	}
	return nil
}

// RunNominateReservationFilterPlugins determines whether the Reservation can participate in the Reserve.
func (ext *frameworkExtenderImpl) RunNominateReservationFilterPlugins(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfo *ReservationInfo, nodeName string) *framework.Status {
	for _, pl := range ext.reservationFilterPlugins {
		status := pl.FilterNominateReservation(ctx, cycleState, pod, reservationInfo, nodeName)
		if !status.IsSuccess() {
			if debugFilterFailure {
				klog.Infof("Failed to FilterReservation for Pod %q with Reservation %q on Node %q, failedPlugin: %s, reason: %s", klog.KObj(pod), klog.KObj(reservationInfo), nodeName, pl.Name(), status.Message())
			}
			return status
		}
	}
	return nil
}

// RunReservationScorePlugins ranks the Reservations
func (ext *frameworkExtenderImpl) RunReservationScorePlugins(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, reservationInfos []*ReservationInfo, nodeName string) (ps PluginToReservationScores, status *framework.Status) {
	if len(reservationInfos) == 0 {
		return
	}
	pluginToReservationScores := make(PluginToReservationScores, len(ext.reservationScorePlugins))
	for _, pl := range ext.reservationScorePlugins {
		pluginToReservationScores[pl.Name()] = make(ReservationScoreList, len(reservationInfos))
	}

	for _, pl := range ext.reservationScorePlugins {
		for index, rInfo := range reservationInfos {
			s, status := pl.ScoreReservation(ctx, cycleState, pod, rInfo, nodeName)
			if !status.IsSuccess() {
				err := fmt.Errorf("plugin %q failed with: %w", pl.Name(), status.AsError())
				return nil, framework.AsStatus(err)
			}
			pluginToReservationScores[pl.Name()][index] = ReservationScore{
				Name:      rInfo.GetName(),
				Namespace: rInfo.GetNamespace(),
				UID:       rInfo.UID(),
				Score:     s,
			}
		}
	}

	for _, pl := range ext.reservationScorePlugins {
		scoreExtensions := pl.ReservationScoreExtensions()
		if scoreExtensions == nil {
			continue
		}
		reservationScoreList := pluginToReservationScores[pl.Name()]
		status := scoreExtensions.NormalizeReservationScore(ctx, cycleState, pod, reservationScoreList)
		if !status.IsSuccess() {
			return nil, framework.AsStatus(fmt.Errorf("running Normalize on Score plugins: %w", status.AsError()))
		}
	}

	// TODO: Should support configure weight
	for _, pl := range ext.reservationScorePlugins {
		weight := 1
		reservationScoreList := pluginToReservationScores[pl.Name()]

		for i, reservationScore := range reservationScoreList {
			// return error if score plugin returns invalid score.
			if reservationScore.Score > MaxReservationScore || reservationScore.Score < MinReservationScore {
				err := fmt.Errorf("plugin %q returns an invalid score %v, it should in the range of [%v, %v]", pl.Name(), reservationScore.Score, MinReservationScore, MaxReservationScore)
				return nil, framework.AsStatus(err)
			}
			reservationScoreList[i].Score = reservationScore.Score * int64(weight)
		}
	}

	return pluginToReservationScores, nil
}

func (ext *frameworkExtenderImpl) RegisterForgetPodHandler(handler ForgetPodHandler) {
	ext.forgetPodHandlers = append(ext.forgetPodHandlers, handler)
}

func (ext *frameworkExtenderImpl) ForgetPod(logger klog.Logger, pod *corev1.Pod) error {
	if err := ext.Scheduler().GetCache().ForgetPod(logger, pod); err != nil {
		return err
	}
	for _, handler := range ext.forgetPodHandlers {
		handler(pod)
	}
	return nil
}

func (ext *frameworkExtenderImpl) RunNUMATopologyManagerAdmit(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string, numaNodes []int, policyType apiext.NUMATopologyPolicy, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) *framework.Status {
	return ext.topologyManager.Admit(ctx, cycleState, pod, nodeName, numaNodes, policyType, exclusivePolicy, allNUMANodeStatus)
}

func (ext *frameworkExtenderImpl) GetNUMATopologyHintProvider() []topologymanager.NUMATopologyHintProvider {
	return ext.numaTopologyHintProviders
}

func (ext *frameworkExtenderImpl) RunReservePluginsReserve(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
		if isPodAssumed(cycleState) {
			return nil
		}
	}
	schedulingphase.RecordPhase(cycleState, schedulingphase.Reserve)
	defer func() { schedulingphase.RecordPhase(cycleState, "") }()
	status := ext.Framework.RunReservePluginsReserve(ctx, cycleState, pod, nodeName)
	ext.GetReservationNominator().RemoveNominatedReservations(pod)
	ext.GetReservationNominator().DeleteNominatedReservePod(pod)
	return status
}

func (ext *frameworkExtenderImpl) RunResizePod(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	for _, pl := range ext.resizePodPlugins {
		startTime := time.Now()
		status := pl.ResizePod(ctx, cycleState, pod, nodeName)
		ext.metricsRecorder.ObservePluginDurationAsync("ResizePod", pl.Name(), status.Code().String(), metrics.SinceInSeconds(startTime))
		if !status.IsSuccess() {
			return status
		}
	}
	return nil
}

const podAssumedStateKey = "koordinator.sh/assumed"

type assumedState struct{}

func (s *assumedState) Clone() framework.StateData {
	return s
}

func markPodAssumed(cycleState *framework.CycleState) {
	cycleState.Write(podAssumedStateKey, &assumedState{})
}

func isPodAssumed(cycleState *framework.CycleState) bool {
	assumed, _ := cycleState.Read(podAssumedStateKey)
	return assumed != nil
}
