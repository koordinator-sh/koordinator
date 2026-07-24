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
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	nrtinformers "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/informers/externalversions"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"

	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/indexer"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/networktopology"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/workloadauditor"
	koordschedulermetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

var (
	EnableNetworkTopologyManager = false
)

// errBatchScheduled is a sentinel error returned from scheduleOne when a pod's whole job has been
// batch-scheduled (assumed and bound) inline by the FindOneNode success path. It is intentionally not
// a FitError so that PostFilter/preemption is skipped, and it is suppressed by the batch-scheduled
// error handler filter so no requeue or failure event is emitted.
var errBatchScheduled = fmt.Errorf("pod handled by inline batch schedule: %s", BatchScheduledReason)

func AddFlags(fs *pflag.FlagSet) {
	fs.IntVarP(&debugTopNScores, "debug-scores", "s", debugTopNScores, "logging topN nodes score and scores for each plugin after running the score extension, disable if set to 0")
	fs.BoolVarP(&debugFilterFailure, "debug-filters", "f", debugFilterFailure, "logging filter failures")
	fs.BoolVarP(&dumpDiagnosis, "debug-diagnosis", "d", dumpDiagnosis, "logging scheduling diagnosis info for debugging")
	fs.BoolVarP(&dumpDiagnosisBlocking, "debug-diagnosis-blocking", "", dumpDiagnosisBlocking, "logging diagnosis info for debugging, including blocking next scheduling cycle")
	fs.IntVarP(&diagnosisQueueSize, "debug-diagnosis-queue-size", "", diagnosisQueueSize, "queue size for diagnosis info")
	fs.IntVarP(&diagnosisWorkerCount, "debug-diagnosis-worker-count", "", diagnosisWorkerCount, "worker count for diagnosis info")
	fs.StringSliceVar(&ControllerPlugins, "controller-plugins", ControllerPlugins, "A list of Controller plugins to enable. "+
		"'-controller-plugins=*' enables all controller plugins. "+
		"'-controller-plugins=Reservation' means only the controller plugin 'Reservation' is enabled. "+
		"'-controller-plugins=*,-Reservation' means all controller plugins except the 'Reservation' plugin are enabled.")
	fs.BoolVarP(&EnableNetworkTopologyManager, "enable-network-topology-manager", "", EnableNetworkTopologyManager, "enable network topology manager")
}

type extendedHandleOptions struct {
	servicesEngine                      *services.Engine
	koordinatorClientSet                koordinatorclientset.Interface
	koordinatorSharedInformerFactory    koordinatorinformers.SharedInformerFactory
	nodeResourceTopologyInformerFactory nrtinformers.SharedInformerFactory
	reservationCache                    ReservationCache
	reservationNominator                ReservationNominator
	networkTopologyManager              networktopology.TreeManager
	crossSchedulerNominator             *CrossSchedulerPodNominator
	workloadAuditor                     workloadauditor.WorkloadAuditor
}

type Option func(*extendedHandleOptions)

func WithServicesEngine(engine *services.Engine) Option {
	return func(options *extendedHandleOptions) {
		options.servicesEngine = engine
	}
}

func WithKoordinatorClientSet(koordinatorClientSet koordinatorclientset.Interface) Option {
	return func(options *extendedHandleOptions) {
		options.koordinatorClientSet = koordinatorClientSet
	}
}

func WithKoordinatorSharedInformerFactory(informerFactory koordinatorinformers.SharedInformerFactory) Option {
	return func(options *extendedHandleOptions) {
		options.koordinatorSharedInformerFactory = informerFactory
	}
}

func WithNodeResourceTopologySharedInformerFactory(informerFactory nrtinformers.SharedInformerFactory) Option {
	return func(options *extendedHandleOptions) {
		options.nodeResourceTopologyInformerFactory = informerFactory
	}
}

func WithReservationCache(cache ReservationCache) Option {
	return func(options *extendedHandleOptions) {
		options.reservationCache = cache
	}
}

func WithReservationNominator(nominator ReservationNominator) Option {
	return func(options *extendedHandleOptions) {
		options.reservationNominator = nominator
	}
}

func WithNetworkTopologyManager(manager networktopology.TreeManager) Option {
	return func(options *extendedHandleOptions) {
		options.networkTopologyManager = manager
	}
}

func WithCrossSchedulerPodNominator(nominator *CrossSchedulerPodNominator) Option {
	return func(options *extendedHandleOptions) {
		options.crossSchedulerNominator = nominator
	}
}

func WithWorkloadAuditor(auditor workloadauditor.WorkloadAuditor) Option {
	return func(options *extendedHandleOptions) {
		options.workloadAuditor = auditor
	}
}

// FrameworkExtenderFactory is a factory for creating a FrameworkExtender.
// NOTE: DO NOT put framework-level data here.
type FrameworkExtenderFactory struct {
	controllerMaps                      *ControllersMap
	servicesEngine                      *services.Engine
	koordinatorClientSet                koordinatorclientset.Interface
	koordinatorSharedInformerFactory    koordinatorinformers.SharedInformerFactory
	nodeResourceTopologyInformerFactory nrtinformers.SharedInformerFactory
	reservationCache                    ReservationCache     // for testing
	reservationNominator                ReservationNominator // for testing
	nextPodPlugin                       NextPodPlugin
	profiles                            map[string]FrameworkExtender
	monitor                             *SchedulerMonitor
	scheduler                           Scheduler
	schedulePod                         func(ctx context.Context, fwk framework.Framework, state fwktype.CycleState, pod *corev1.Pod) (scheduler.ScheduleResult, error)
	*errorHandlerDispatcher

	networkTopologyTreeManager networktopology.TreeManager
	crossSchedulerNominator    *CrossSchedulerPodNominator

	workloadAuditor workloadauditor.WorkloadAuditor

	pluginInformerFactories []SharedInformerFactory

	metricsRecorder *metrics.MetricAsyncRecorder

	sharedCachesMu      sync.Mutex
	sharedCaches        map[string]SharedPluginCache
	sharedCachesOrder   []string
	sharedCachesStarted bool
	// startedCaches is the immutable, registration-ordered snapshot of shared caches
	// published once by StartSharedCaches. The unified dispatcher reads it lock-free on
	// every pod/node event, avoiding a mutex acquisition and slice allocation per event.
	startedCaches atomic.Pointer[[]SharedPluginCache]
}

func NewFrameworkExtenderFactory(options ...Option) (*FrameworkExtenderFactory, error) {
	handleOptions := &extendedHandleOptions{}
	for _, opt := range options {
		opt(handleOptions)
	}

	if err := indexer.AddIndexers(handleOptions.koordinatorSharedInformerFactory); err != nil {
		return nil, err
	}

	return &FrameworkExtenderFactory{
		controllerMaps:                      NewControllersMap(),
		servicesEngine:                      handleOptions.servicesEngine,
		koordinatorClientSet:                handleOptions.koordinatorClientSet,
		koordinatorSharedInformerFactory:    handleOptions.koordinatorSharedInformerFactory,
		nodeResourceTopologyInformerFactory: handleOptions.nodeResourceTopologyInformerFactory,
		reservationCache:                    handleOptions.reservationCache,
		reservationNominator:                handleOptions.reservationNominator,
		profiles:                            map[string]FrameworkExtender{},
		monitor:                             NewSchedulerMonitor(schedulerMonitorPeriod, schedulingTimeout),
		errorHandlerDispatcher:              newErrorHandlerDispatcher(),
		networkTopologyTreeManager:          handleOptions.networkTopologyManager,
		crossSchedulerNominator:             handleOptions.crossSchedulerNominator,
		workloadAuditor:                     handleOptions.workloadAuditor,
		metricsRecorder:                     metrics.NewMetricsAsyncRecorder(1000, time.Second, wait.NeverStop),
		sharedCaches:                        map[string]SharedPluginCache{},
	}, nil
}

func (f *FrameworkExtenderFactory) NewFrameworkExtender(fw framework.Framework) FrameworkExtender {
	frameworkExtender := f.profiles[fw.ProfileName()]
	if frameworkExtender == nil {
		frameworkExtender = NewFrameworkExtender(f, fw)
		f.profiles[fw.ProfileName()] = frameworkExtender
	}
	return frameworkExtender
}

func (f *FrameworkExtenderFactory) GetExtender(profileName string) FrameworkExtender {
	extender := f.profiles[profileName]
	if extender != nil {
		return extender
	}
	return nil
}

func (f *FrameworkExtenderFactory) KoordinatorClientSet() koordinatorclientset.Interface {
	return f.koordinatorClientSet
}

// SetBatchScheduler registers the inline BatchScheduler on all framework extenders.
// It must be called after all profiles have been created (i.e. after the scheduler is constructed).
func (f *FrameworkExtenderFactory) SetBatchScheduler(bs BatchScheduler) {
	for _, extender := range f.profiles {
		if impl, ok := extender.(*frameworkExtenderImpl); ok {
			impl.batchScheduler = bs
		}
	}
}

// NewBatchScheduledErrorHandlerFilter returns a PreErrorHandlerFilter that suppresses the default
// scheduling failure handling for pods that have been batch-scheduled (assumed and bound) inline by
// the FindOneNode success path. Such pods surface as a scheduling "failure" only to short-circuit the
// scheduling cycle, so no requeue or failure event should be emitted.
func NewBatchScheduledErrorHandlerFilter() PreErrorHandlerFilter {
	return func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) bool {
		if status == nil {
			return false
		}
		return status.Code() == fwktype.Unschedulable && status.Message() == BatchScheduledReason
	}
}

func (f *FrameworkExtenderFactory) KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory {
	return f.koordinatorSharedInformerFactory
}

// Scheduler return the scheduler adapter to support operating with cache and schedulingQueue.
// NOTE: Plugins do not acquire a dispatcher instance during plugin initialization,
// nor are they allowed to hold the object within the plugin object.
func (f *FrameworkExtenderFactory) Scheduler() Scheduler {
	return f.scheduler
}

func (f *FrameworkExtenderFactory) InitScheduler(sched Scheduler) {
	f.scheduler = sched
	adaptor, ok := sched.(*SchedulerAdapter)
	if ok {
		schedulePod := adaptor.Scheduler.SchedulePod
		f.schedulePod = schedulePod
		adaptor.Scheduler.SchedulePod = f.scheduleOne
		f.CollectSchedulePodResult(adaptor.Scheduler)
		nextPod := adaptor.Scheduler.NextPod
		adaptor.Scheduler.NextPod = func(logger klog.Logger) (*framework.QueuedPodInfo, error) {
			podInfo, err := f.runNextPodPlugin()
			if err != nil {
				klog.Errorf("run next pod plugin failed, err: %v", err)
				return podInfo, err
			}
			// NextPodPlugin but has no suggestion for which Pod to dequeue next and falls back to the original nextPod logic
			if podInfo == nil {
				podInfo, err = nextPod(logger)
				if err != nil {
					return podInfo, err
				}
				// just for plugins to get Pod queue information
				RecordPodQueueInfoToPod(podInfo)
			}
			f.monitor.RecordNextPod(podInfo)
			if podInfo != nil && k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
				// The podInfo can be nil when the queue is closing.
				// Deep copy podInfo to allow pod modification during scheduling
				podInfo = podInfo.DeepCopy()
			}
			return podInfo, nil
		}
	}
}

func (f *FrameworkExtenderFactory) runNextPodPlugin() (*framework.QueuedPodInfo, error) {
	if f.nextPodPlugin != nil {
		startTime := time.Now()
		pod := f.nextPodPlugin.NextPod()
		f.metricsRecorder.ObservePluginDurationAsync("NextPod", f.nextPodPlugin.Name(), strconv.FormatBool(pod != nil), metrics.SinceInSeconds(startTime))
		if pod != nil {
			klog.Infof("run next pod plugin, pod: %s/%s", pod.Namespace, pod.Name)
			startTime = time.Now()
			f.scheduler.GetSchedulingQueue().Delete(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					// should not delete it from nominator, so use a fake UID
					UID:       uuid.NewUUID(),
					Namespace: pod.Namespace,
					Name:      pod.Name,
				},
			})
			koordschedulermetrics.RecordNextPodPluginsDeletePodFromQueue(time.Since(startTime))
			return makePodInfoFromPod(pod)
		}
	}
	return nil, nil
}

const (
	initialTimestampManager = "scheduler.scheduling.koordinator.sh/initialTimestamp"
	attemptsManager         = "scheduler.scheduling.koordinator.sh/attempts"
)

func CopyQueueInfoToPod(podHasQueueInfo, podNeedQueueInfo *corev1.Pod) *corev1.Pod {
	resultPod := &corev1.Pod{
		TypeMeta:   podNeedQueueInfo.TypeMeta,
		ObjectMeta: podNeedQueueInfo.ObjectMeta,
		Spec:       podNeedQueueInfo.Spec,
		Status:     podNeedQueueInfo.Status,
	}
	// avoid directly modifying the original Pod and only copy the fields that are needed
	resultPod.ManagedFields = podHasQueueInfo.ManagedFields
	return resultPod
}

func RecordPodQueueInfoToPod(podInfo *framework.QueuedPodInfo) {
	if podInfo == nil || podInfo.Pod == nil { // the podInfo can be nil when the queue is closing
		return
	}
	var initialTimestamp *metav1.Time
	if podInfo.InitialAttemptTimestamp != nil {
		initialTimestamp = &metav1.Time{Time: *podInfo.InitialAttemptTimestamp}
	}
	podInfo.Pod = &corev1.Pod{
		TypeMeta:   podInfo.Pod.TypeMeta,
		ObjectMeta: podInfo.Pod.ObjectMeta,
		Spec:       podInfo.Pod.Spec,
		Status:     podInfo.Pod.Status,
	}
	// avoid directly modifying the original Pod and only copy the fields that are needed
	podInfo.Pod.ManagedFields = []metav1.ManagedFieldsEntry{
		{Manager: initialTimestampManager, Time: initialTimestamp},
		{Manager: attemptsManager, Subresource: strconv.Itoa(podInfo.Attempts)},
	}
}

func makePodInfoFromPod(pod *corev1.Pod) (*framework.QueuedPodInfo, error) {
	if len(pod.ManagedFields) == 0 {
		now := time.Now()
		return &framework.QueuedPodInfo{PodInfo: &framework.PodInfo{Pod: pod}, InitialAttemptTimestamp: &now, Attempts: 0}, fmt.Errorf("pod %s/%s has no podQueueInfo in pod.managedFields", pod.Namespace, pod.Name)
	}
	var initialTimestamp *time.Time
	if pod.ManagedFields[0].Time != nil {
		initialTimestamp = &pod.ManagedFields[0].Time.Time
	}
	var attempts int
	if len(pod.ManagedFields) > 1 {
		attempts, _ = strconv.Atoi(pod.ManagedFields[1].Subresource)
	}
	return &framework.QueuedPodInfo{
		PodInfo:                 &framework.PodInfo{Pod: pod},
		InitialAttemptTimestamp: initialTimestamp,
		Attempts:                attempts,
	}, nil
}

// PodScheduleAttemptInfo returns the scheduling attempt count and the first-attempt timestamp that
// were stashed into the pod's managed fields when it was popped from the scheduling queue (see
// RecordPodQueueInfoToPod). ok is false when the pod carries no such queue info, e.g. a pod that was
// never popped from the queue (such as a sibling pod scheduled as part of an inline batch job).
func PodScheduleAttemptInfo(pod *corev1.Pod) (attempts int, initialAttemptTimestamp *time.Time, ok bool) {
	podInfo, err := makePodInfoFromPod(pod)
	if err != nil || podInfo == nil {
		return 0, nil, false
	}
	return podInfo.Attempts, podInfo.InitialAttemptTimestamp, true
}

func (f *FrameworkExtenderFactory) scheduleOne(ctx context.Context, fwk framework.Framework, cycleState fwktype.CycleState, pod *corev1.Pod) (scheduler.ScheduleResult, error) {
	InitDiagnosis(cycleState, pod)
	f.monitor.StartMonitoring(pod)
	if f.workloadAuditor != nil {
		f.workloadAuditor.RecordAttemptPod(pod)
	}
	scheduleResult, err := f.schedulePod(ctx, fwk, cycleState, pod)
	if err != nil {
		if st := getBatchScheduleState(cycleState); st != nil && st.handled && st.success {
			// The whole job (including this pod) has already been assumed and bound by the inline
			// batch scheduler. Return a sentinel error to skip PostFilter/preemption; the registered
			// error handler filter suppresses the default failure handling.
			return scheduleResult, errBatchScheduled
		}
		recordScheduleDiagnosis(cycleState, err)
		return scheduleResult, err
	}

	extender, ok := fwk.(*frameworkExtenderImpl)
	if ok {
		// Due to some ResizePod plugins need the reservation nomination before the real Reserve phase,
		// and the PreScore phase might be skipped, we force to nominate reservation here.
		// https://github.com/koordinator-sh/koordinator/issues/2753
		if reservationNominator := extender.GetReservationNominator(); reservationNominator != nil {
			status := reservationNominator.ReservationNominate(ctx, cycleState, pod, scheduleResult.SuggestedHost)
			if !status.IsSuccess() {
				return scheduleResult, status.AsError()
			}
		}

		if k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
			// NOTE(joseph): We can modify the Pod because we have cloned the Pod in the NextPod function.
			// Make sure to modify the Pod only related to AssumePod, and do not modify the plugins' cache, since
			// when the assume failure would not do Unreserve the plugins' cache.
			// Other resizing logic about the plugins' cache can be convergent to its Reserve phase.
			status := extender.RunResizePod(ctx, cycleState, pod, scheduleResult.SuggestedHost)
			if !status.IsSuccess() {
				fwk.RunReservePluginsUnreserve(ctx, cycleState, pod, scheduleResult.SuggestedHost)
				return scheduleResult, status.AsError()
			}
		}
	}

	return scheduleResult, nil
}

func recordScheduleDiagnosis(cycleState fwktype.CycleState, err error) {
	var fitError *framework.FitError
	if errors.As(err, &fitError) {
		diagnosis := GetDiagnosis(cycleState)
		diagnosis.PreFilterMessage = fitError.Diagnosis.PreFilterMsg
		if diagnosis.ScheduleDiagnosis == nil {
			diagnosis.ScheduleDiagnosis = &ScheduleDiagnosis{
				SchedulingMode: PodSchedulingMode,
			}
		}
		if diagnosis.PreFilterMessage == "" && diagnosis.ScheduleDiagnosis.NodeToStatusMap == nil {
			if fitError.Diagnosis.NodeToStatus != nil {
				nodeToStatusMap := make(map[string]*fwktype.Status)
				fitError.Diagnosis.NodeToStatus.ForEachExplicitNode(func(nodeName string, status *fwktype.Status) {
					nodeToStatusMap[nodeName] = status
				})
				diagnosis.ScheduleDiagnosis.NodeToStatusMap = nodeToStatusMap
			}
		}
	}
}

func (f *FrameworkExtenderFactory) CollectSchedulePodResult(sched *scheduler.Scheduler) {
	schedulePod := sched.SchedulePod
	sched.SchedulePod = func(ctx context.Context, fwk framework.Framework, state fwktype.CycleState, pod *corev1.Pod) (scheduler.ScheduleResult, error) {
		scheduleResult, err := schedulePod(ctx, fwk, state, pod)
		// avoid recording metrics when there is no feasible node or internal error in scheduling
		if scheduleResult.SuggestedHost != "" {
			koordschedulermetrics.PodSchedulingEvaluatedNodes.Observe(float64(scheduleResult.EvaluatedNodes))
			koordschedulermetrics.PodSchedulingFeasibleNodes.Observe(float64(scheduleResult.FeasibleNodes))
		}
		return scheduleResult, err
	}
}

func (f *FrameworkExtenderFactory) InterceptSchedulerError(sched *scheduler.Scheduler) {
	f.errorHandlerDispatcher.setDefaultHandler(sched.FailureHandler)
	sched.FailureHandler = func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *fwktype.Status, nominatingInfo *fwktype.NominatingInfo, start time.Time) {
		f.errorHandlerDispatcher.Error(ctx, fwk, podInfo, status, nominatingInfo, start)
		f.monitor.Complete(podInfo.Pod, status)
		if f.workloadAuditor != nil {
			f.workloadAuditor.RecordPodScheduleResult(podInfo.Pod, workloadauditor.RecordTypeScheduleFailure, "")
		}
	}
}

func (f *FrameworkExtenderFactory) Run(ctx context.Context) {
	f.controllerMaps.Start()
	if EnableNetworkTopologyManager {
		f.networkTopologyTreeManager.Run(ctx)
	}
}

func (f *FrameworkExtenderFactory) updatePlugins(pl fwktype.Plugin, profileName string) {
	if nextPodPlugin, ok := pl.(NextPodPlugin); ok {
		pluginName := pl.Name()
		if f.nextPodPlugin != nil && f.nextPodPlugin.Name() != pluginName {
			klog.InfoS("NextPodPlugin already registered, skipped", "plugin", pluginName, "profile", profileName, "existing", f.nextPodPlugin.Name())
		} else {
			f.nextPodPlugin = nextPodPlugin
			klog.V(4).InfoS("NextPodPlugin successfully registered", "plugin", pluginName, "profile", profileName)
		}
	}
	if f.servicesEngine != nil {
		f.servicesEngine.RegisterPluginService(pl, profileName)
	}
	if f.controllerMaps != nil {
		f.controllerMaps.RegisterControllers(pl, profileName)
	}
	if provider, ok := pl.(InformerFactoryProvider); ok {
		f.pluginInformerFactories = append(f.pluginInformerFactories, provider.GetInformerFactories()...)
	}
}

// GetPluginInformerFactories returns all informer factories registered by plugins
// via the InformerFactoryProvider interface.
func (f *FrameworkExtenderFactory) GetPluginInformerFactories() []SharedInformerFactory {
	return f.pluginInformerFactories
}

// PluginFactoryProxy is used to proxy the call to the PluginFactory function and pass in the ExtendedHandle for the custom plugin
func PluginFactoryProxy(extenderFactory *FrameworkExtenderFactory, factoryFn frameworkruntime.PluginFactory) frameworkruntime.PluginFactory {
	return func(ctx context.Context, args runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
		fw := handle.(framework.Framework)
		frameworkExtender := extenderFactory.NewFrameworkExtender(fw)
		plugin, err := factoryFn(ctx, args, frameworkExtender)
		if err != nil {
			return nil, err
		}
		extenderFactory.updatePlugins(plugin, fw.ProfileName())
		frameworkExtender.(*frameworkExtenderImpl).updatePlugins(plugin)
		return plugin, nil
	}
}

// getOrRegisterSharedCache is the factory-side implementation behind
// ExtendedHandle.GetOrRegisterSharedCache. First call for a given key invokes create(handle)
// and stores the result; subsequent calls return the stored instance and do not call
// create. Registration order is preserved for deterministic event dispatch.
func (f *FrameworkExtenderFactory) getOrRegisterSharedCache(key string, handle ExtendedHandle, create func(ExtendedHandle) SharedPluginCache) SharedPluginCache {
	f.sharedCachesMu.Lock()
	defer f.sharedCachesMu.Unlock()
	if c, ok := f.sharedCaches[key]; ok {
		return c
	}
	c := create(handle)
	f.sharedCaches[key] = c
	f.sharedCachesOrder = append(f.sharedCachesOrder, key)
	return c
}

// StartSharedCaches wires up the unified pod/node event dispatcher on informerFactory and
// invokes Start(ctx) on every registered SharedPluginCache exactly once. Must be called
// after all profiles are built (all Plugin.New() calls completed) and before
// informerFactory.Start() so no event is delivered before its handler is registered.
// Idempotent — subsequent calls after the first are no-ops.
func (f *FrameworkExtenderFactory) StartSharedCaches(ctx context.Context, informerFactory informers.SharedInformerFactory) error {
	f.sharedCachesMu.Lock()
	if f.sharedCachesStarted {
		f.sharedCachesMu.Unlock()
		return nil
	}
	caches := make([]SharedPluginCache, 0, len(f.sharedCachesOrder))
	for _, key := range f.sharedCachesOrder {
		caches = append(caches, f.sharedCaches[key])
	}
	f.sharedCachesMu.Unlock()

	if len(caches) > 0 {
		// Register the unified pod/node dispatchers before committing any state, so a
		// registration failure leaves the factory un-started (sharedCachesStarted stays
		// false, no snapshot published) and returns an error for the caller to fail fast
		// on, rather than a partially-started state that later calls would skip.
		//
		// Use ForceSyncFromInformer (not a bare AddEventHandler) so the dispatcher's
		// registrations are collected into WaitForHandlersSync: the scheduler then waits
		// for these handlers to drain their initial pod/node list into the shared caches
		// before the first scheduling cycle, so a shared cache is never read while still
		// unpopulated. This preserves the sync guarantee the per-plugin handlers had before
		// they were centralized here.
		if _, err := frameworkexthelper.ForceSyncFromInformer(ctx.Done(), informerFactory,
			informerFactory.Core().V1().Pods().Informer(), cache.ResourceEventHandlerFuncs{
				AddFunc:    f.dispatchPodAdd,
				UpdateFunc: f.dispatchPodUpdate,
				DeleteFunc: f.dispatchPodDelete,
			}); err != nil {
			return fmt.Errorf("failed to register shared cache pod event handler, err: %w", err)
		}
		if _, err := frameworkexthelper.ForceSyncFromInformer(ctx.Done(), informerFactory,
			informerFactory.Core().V1().Nodes().Informer(), cache.ResourceEventHandlerFuncs{
				AddFunc:    f.dispatchNodeAdd,
				UpdateFunc: f.dispatchNodeUpdate,
				DeleteFunc: f.dispatchNodeDelete,
			}); err != nil {
			return fmt.Errorf("failed to register shared cache node event handler, err: %w", err)
		}
		// Publish the immutable snapshot for lock-free dispatch. Events cannot arrive until
		// the informer factory is started (after this returns), so it is safe to publish
		// after the handlers are registered.
		f.startedCaches.Store(&caches)
	}

	f.sharedCachesMu.Lock()
	f.sharedCachesStarted = true
	f.sharedCachesMu.Unlock()

	for _, c := range caches {
		c.Start(ctx)
	}
	return nil
}

// dispatchCaches returns the immutable snapshot of registered shared caches published by
// StartSharedCaches. Read lock-free on every event — no mutex, no allocation.
//
// TODO: the dispatch* methods below invoke each cache's handler sequentially. The end-state
// goal is parallel-by-default dispatch with an opt-in serial mode, so the shared dispatcher
// does not regress event-handling latency compared to the original per-profile handler
// pattern. Deferred to the unified event-dispatcher phase (PR-3).
func (f *FrameworkExtenderFactory) dispatchCaches() []SharedPluginCache {
	if p := f.startedCaches.Load(); p != nil {
		return *p
	}
	return nil
}

func (f *FrameworkExtenderFactory) dispatchPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	for _, c := range f.dispatchCaches() {
		c.OnPodAdd(pod)
	}
}

func (f *FrameworkExtenderFactory) dispatchPodUpdate(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		return
	}
	newPod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}
	for _, c := range f.dispatchCaches() {
		c.OnPodUpdate(oldPod, newPod)
	}
}

func (f *FrameworkExtenderFactory) dispatchPodDelete(obj interface{}) {
	var pod *corev1.Pod
	switch t := obj.(type) {
	case *corev1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		pod, _ = t.Obj.(*corev1.Pod)
	}
	if pod == nil {
		return
	}
	for _, c := range f.dispatchCaches() {
		c.OnPodDelete(pod)
	}
}

func (f *FrameworkExtenderFactory) dispatchNodeAdd(obj interface{}) {
	node, ok := obj.(*corev1.Node)
	if !ok {
		return
	}
	for _, c := range f.dispatchCaches() {
		c.OnNodeAdd(node)
	}
}

func (f *FrameworkExtenderFactory) dispatchNodeUpdate(oldObj, newObj interface{}) {
	oldNode, ok := oldObj.(*corev1.Node)
	if !ok {
		return
	}
	newNode, ok := newObj.(*corev1.Node)
	if !ok {
		return
	}
	for _, c := range f.dispatchCaches() {
		c.OnNodeUpdate(oldNode, newNode)
	}
}

func (f *FrameworkExtenderFactory) dispatchNodeDelete(obj interface{}) {
	var node *corev1.Node
	switch t := obj.(type) {
	case *corev1.Node:
		node = t
	case cache.DeletedFinalStateUnknown:
		node, _ = t.Obj.(*corev1.Node)
	}
	if node == nil {
		return
	}
	for _, c := range f.dispatchCaches() {
		c.OnNodeDelete(node)
	}
}
