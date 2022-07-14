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
	"sync"

	nrtinformers "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"

	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

// ExtendedHandle extends the k8s scheduling framework Handle interface
// to facilitate plugins to access Koordinator's resources and states.
type ExtendedHandle interface {
	framework.Handle
	KoordinatorClientSet() koordinatorclientset.Interface
	KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory
	NodeResourceTopologySharedInformerFactory() nrtinformers.SharedInformerFactory
}

type extendedHandleOptions struct {
	koordinatorClientSet             koordinatorclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory
	nrtSharedInformerFactory         nrtinformers.SharedInformerFactory
}

type Option func(*extendedHandleOptions)

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
		options.nrtSharedInformerFactory = informerFactory
	}
}

type frameworkExtendedHandleImpl struct {
	once sync.Once
	framework.Handle
	koordinatorClientSet             koordinatorclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory
	nrtSharedInformerFactory         nrtinformers.SharedInformerFactory
}

func NewExtendedHandle(options ...Option) ExtendedHandle {
	handleOptions := &extendedHandleOptions{}
	for _, opt := range options {
		opt(handleOptions)
	}

	return &frameworkExtendedHandleImpl{
		koordinatorClientSet:             handleOptions.koordinatorClientSet,
		koordinatorSharedInformerFactory: handleOptions.koordinatorSharedInformerFactory,
		nrtSharedInformerFactory:         handleOptions.nrtSharedInformerFactory,
	}
}

func (ext *frameworkExtendedHandleImpl) KoordinatorClientSet() koordinatorclientset.Interface {
	return ext.koordinatorClientSet
}

func (ext *frameworkExtendedHandleImpl) KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory {
	return ext.koordinatorSharedInformerFactory
}

func (ext *frameworkExtendedHandleImpl) NodeResourceTopologySharedInformerFactory() nrtinformers.SharedInformerFactory {
	return ext.nrtSharedInformerFactory
}

type FrameworkExtender interface {
	framework.Framework
}

type FrameworkExtenderFactory interface {
	New(f framework.Framework) FrameworkExtender
}

type SchedulingPhaseHook interface {
	Name() string
}

type PreFilterPhaseHook interface {
	SchedulingPhaseHook
	PreFilterHook(handle ExtendedHandle, state *framework.CycleState, pod *corev1.Pod) (*corev1.Pod, bool)
}

type FilterPhaseHook interface {
	SchedulingPhaseHook
	FilterHook(handle ExtendedHandle, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) (*corev1.Pod, *framework.NodeInfo, bool)
}

type frameworkExtenderFactoryImpl struct {
	handle ExtendedHandle

	// extend framework with SchedulingPhaseHook
	preFilterHooks []PreFilterPhaseHook
	filterHooks    []FilterPhaseHook
}

func NewFrameworkExtenderFactory(handle ExtendedHandle, hooks ...SchedulingPhaseHook) FrameworkExtenderFactory {
	i := &frameworkExtenderFactoryImpl{
		handle: handle,
	}
	for _, h := range hooks {
		// a hook may register in multiple phases
		preFilter, ok := h.(PreFilterPhaseHook)
		if ok {
			i.preFilterHooks = append(i.preFilterHooks, preFilter)
		}
		filter, ok := h.(FilterPhaseHook)
		if ok {
			i.filterHooks = append(i.filterHooks, filter)
		}
	}
	return i
}

func (i *frameworkExtenderFactoryImpl) New(f framework.Framework) FrameworkExtender {
	return &frameworkExtenderImpl{
		Framework:      f,
		handle:         i.handle,
		preFilterHooks: i.preFilterHooks,
		filterHooks:    i.filterHooks,
	}
}

var _ framework.Framework = &frameworkExtenderImpl{}

type frameworkExtenderImpl struct {
	framework.Framework
	handle ExtendedHandle

	preFilterHooks []PreFilterPhaseHook
	filterHooks    []FilterPhaseHook
}

func (ext *frameworkExtenderImpl) RunPreFilterPlugins(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod) *framework.Status {
	for _, hook := range ext.preFilterHooks {
		newPod, hooked := hook.PreFilterHook(ext.handle, cycleState, pod)
		if hooked {
			klog.V(5).InfoS("RunPreFilterPlugins hooked", "meet PreFilterPhaseHook", "hook", hook.Name(), "pod", klog.KObj(pod))
			return ext.Framework.RunPreFilterPlugins(ctx, cycleState, newPod)
		}
	}
	return ext.Framework.RunPreFilterPlugins(ctx, cycleState, pod)
}

func (ext *frameworkExtenderImpl) RunFilterPlugins(ctx context.Context, cycleState *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) framework.PluginToStatus {
	for _, hook := range ext.filterHooks {
		// hook can change the args (cycleState, pod, nodeInfo) for filter plugins
		newPod, newNodeInfo, hooked := hook.FilterHook(ext.handle, cycleState, pod, nodeInfo)
		if hooked {
			klog.V(5).InfoS("RunFilterPlugins hooked", "meet FilterPhaseHook", "hook", hook.Name(), "pod", klog.KObj(pod))
			return ext.Framework.RunFilterPlugins(ctx, cycleState, newPod, newNodeInfo)
		}
	}
	return ext.Framework.RunFilterPlugins(ctx, cycleState, pod, nodeInfo)
}

// PluginFactoryProxy is used to proxy the call to the PluginFactory function and pass in the ExtendedHandle for the custom plugin
func PluginFactoryProxy(extendHandle ExtendedHandle, factoryFn frameworkruntime.PluginFactory) frameworkruntime.PluginFactory {
	return func(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
		impl := extendHandle.(*frameworkExtendedHandleImpl)
		impl.once.Do(func() {
			impl.Handle = handle
		})
		return factoryFn(args, extendHandle)
	}
}
