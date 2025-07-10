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
	"strconv"
	"time"

	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"k8s.io/kubernetes/pkg/scheduler/metrics"

	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/indexer"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
	koordschedulermetrics "github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

func AddFlags(fs *pflag.FlagSet) {
	fs.IntVarP(&debugTopNScores, "debug-scores", "s", debugTopNScores, "logging topN nodes score and scores for each plugin after running the score extension, disable if set to 0")
	fs.BoolVarP(&debugFilterFailure, "debug-filters", "f", debugFilterFailure, "logging filter failures")
	fs.StringSliceVar(&ControllerPlugins, "controller-plugins", ControllerPlugins, "A list of Controller plugins to enable. "+
		"'-controller-plugins=*' enables all controller plugins. "+
		"'-controller-plugins=Reservation' means only the controller plugin 'Reservation' is enabled. "+
		"'-controller-plugins=*,-Reservation' means all controller plugins except the 'Reservation' plugin are enabled.")
}

type extendedHandleOptions struct {
	servicesEngine                   *services.Engine
	koordinatorClientSet             koordinatorclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory
	reservationNominator             ReservationNominator
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

func WithReservationNominator(nominator ReservationNominator) Option {
	return func(options *extendedHandleOptions) {
		options.reservationNominator = nominator
	}
}

type FrameworkExtenderFactory struct {
	controllerMaps                   *ControllersMap
	servicesEngine                   *services.Engine
	koordinatorClientSet             koordinatorclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory
	reservationNominator             ReservationNominator
	nextPodPlugin                    NextPodPlugin
	profiles                         map[string]FrameworkExtender
	monitor                          *SchedulerMonitor
	scheduler                        Scheduler
	schedulePod                      func(ctx context.Context, fwk framework.Framework, state *framework.CycleState, pod *corev1.Pod) (scheduler.ScheduleResult, error)
	*errorHandlerDispatcher

	metricsRecorder *metrics.MetricAsyncRecorder
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
		controllerMaps:                   NewControllersMap(),
		servicesEngine:                   handleOptions.servicesEngine,
		koordinatorClientSet:             handleOptions.koordinatorClientSet,
		koordinatorSharedInformerFactory: handleOptions.koordinatorSharedInformerFactory,
		reservationNominator:             handleOptions.reservationNominator,
		profiles:                         map[string]FrameworkExtender{},
		monitor:                          NewSchedulerMonitor(schedulerMonitorPeriod, schedulingTimeout),
		errorHandlerDispatcher:           newErrorHandlerDispatcher(),
		metricsRecorder:                  metrics.NewMetricsAsyncRecorder(1000, time.Second, wait.NeverStop),
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
		if k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
			schedulePod := adaptor.Scheduler.SchedulePod
			f.schedulePod = schedulePod
			adaptor.Scheduler.SchedulePod = f.scheduleOne
		}
		nextPod := adaptor.Scheduler.NextPod
		adaptor.Scheduler.NextPod = func() (*framework.QueuedPodInfo, error) {
			podInfo, err := f.runNextPodPlugin()
			if err != nil {
				klog.Errorf("run next pod plugin failed, err: %v", err)
				return podInfo, err
			}
			// NextPodPlugin but has no suggestion for which Pod to dequeue next and falls back to the original nextPod logic
			if podInfo == nil {
				podInfo, err = nextPod()
				if err != nil {
					return podInfo, err
				}
				// just for plugins to get Pod queue information
				RecordPodQueueInfoToPod(podInfo)
			}
			f.monitor.RecordNextPod(podInfo)
			if k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
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
			_ = f.scheduler.GetSchedulingQueue().Delete(&corev1.Pod{
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

func (f *FrameworkExtenderFactory) scheduleOne(ctx context.Context, fwk framework.Framework, cycleState *framework.CycleState, pod *corev1.Pod) (scheduler.ScheduleResult, error) {
	f.monitor.StartMonitoring(pod)

	scheduleResult, err := f.schedulePod(ctx, fwk, cycleState, pod)
	if err != nil {
		return scheduleResult, err
	}

	if k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
		// NOTE(joseph): We can modify the Pod because we have cloned the Pod in the NextPod function.
		pod.Spec.NodeName = scheduleResult.SuggestedHost
		status := fwk.RunReservePluginsReserve(ctx, cycleState, pod, scheduleResult.SuggestedHost)
		if !status.IsSuccess() {
			fwk.RunReservePluginsUnreserve(ctx, cycleState, pod, scheduleResult.SuggestedHost)
			return scheduleResult, status.AsError()
		}
		markPodAssumed(cycleState)

		extender, ok := fwk.(*frameworkExtenderImpl)
		if ok {
			status = extender.RunResizePod(ctx, cycleState, pod, scheduleResult.SuggestedHost)
			if !status.IsSuccess() {
				fwk.RunReservePluginsUnreserve(ctx, cycleState, pod, scheduleResult.SuggestedHost)
				return scheduleResult, status.AsError()
			}
		}
	}

	return scheduleResult, nil
}

func (f *FrameworkExtenderFactory) InterceptSchedulerError(sched *scheduler.Scheduler) {
	f.errorHandlerDispatcher.setDefaultHandler(sched.FailureHandler)
	sched.FailureHandler = func(ctx context.Context, fwk framework.Framework, podInfo *framework.QueuedPodInfo, status *framework.Status, nominatingInfo *framework.NominatingInfo, start time.Time) {
		f.errorHandlerDispatcher.Error(ctx, fwk, podInfo, status, nominatingInfo, start)
		f.monitor.Complete(podInfo.Pod, status)
	}
}

func (f *FrameworkExtenderFactory) Run() {
	f.controllerMaps.Start()
}

func (f *FrameworkExtenderFactory) updatePlugins(pl framework.Plugin) {
	if f.servicesEngine != nil {
		f.servicesEngine.RegisterPluginService(pl)
	}
	if f.controllerMaps != nil {
		f.controllerMaps.RegisterControllers(pl)
	}
}

// PluginFactoryProxy is used to proxy the call to the PluginFactory function and pass in the ExtendedHandle for the custom plugin
func PluginFactoryProxy(extenderFactory *FrameworkExtenderFactory, factoryFn frameworkruntime.PluginFactory) frameworkruntime.PluginFactory {
	return func(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
		fw := handle.(framework.Framework)
		frameworkExtender := extenderFactory.NewFrameworkExtender(fw)
		plugin, err := factoryFn(args, frameworkExtender)
		if err != nil {
			return nil, err
		}
		if nextPodPlugin, ok := plugin.(NextPodPlugin); ok {
			if extenderFactory.nextPodPlugin != nil && extenderFactory.nextPodPlugin.Name() != nextPodPlugin.Name() {
				return nil, fmt.Errorf("duplicate NextPodPlugin: %s, %s", nextPodPlugin.Name(), extenderFactory.nextPodPlugin.Name())
			}
			extenderFactory.nextPodPlugin = nextPodPlugin
		}
		extenderFactory.updatePlugins(plugin)
		frameworkExtender.(*frameworkExtenderImpl).updatePlugins(plugin)
		return plugin, nil
	}
}
