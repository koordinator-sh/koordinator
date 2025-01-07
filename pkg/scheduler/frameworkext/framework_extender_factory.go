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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/scheduler"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"

	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/indexer"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

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
	profiles                         map[string]FrameworkExtender
	monitor                          *SchedulerMonitor
	scheduler                        Scheduler
	schedulePod                      func(ctx context.Context, fwk framework.Framework, state *framework.CycleState, pod *corev1.Pod) (scheduler.ScheduleResult, error)
	*errorHandlerDispatcher
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
	if k8sfeature.DefaultFeatureGate.Enabled(features.ResizePod) {
		adaptor, ok := sched.(*SchedulerAdapter)
		if ok {
			schedulePod := adaptor.Scheduler.SchedulePod
			f.schedulePod = schedulePod
			adaptor.Scheduler.SchedulePod = f.scheduleOne

			nextPod := adaptor.Scheduler.NextPod
			adaptor.Scheduler.NextPod = func() (*framework.QueuedPodInfo, error) {
				podInfo, err := nextPod()
				if err != nil {
					return podInfo, err
				}
				// Deep copy podInfo to allow pod modification during scheduling
				podInfo = podInfo.DeepCopy()
				f.monitor.RecordNextPod(podInfo)
				return podInfo, nil
			}
		}
	}
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
		extenderFactory.updatePlugins(plugin)
		frameworkExtender.(*frameworkExtenderImpl).updatePlugins(plugin)
		return plugin, nil
	}
}
