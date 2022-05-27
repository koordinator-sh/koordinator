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
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
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
}

type frameworkExtendedHandleImpl struct {
	once sync.Once
	framework.Handle
	koordinatorClientSet             koordinatorclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory
}

func NewExtendedHandle(
	koordinatorClientSet koordinatorclientset.Interface,
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory,
) ExtendedHandle {
	return &frameworkExtendedHandleImpl{
		koordinatorClientSet:             koordinatorClientSet,
		koordinatorSharedInformerFactory: koordinatorSharedInformerFactory,
	}
}

func (ext *frameworkExtendedHandleImpl) KoordinatorClientSet() koordinatorclientset.Interface {
	return ext.koordinatorClientSet
}

func (ext *frameworkExtendedHandleImpl) KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory {
	return ext.koordinatorSharedInformerFactory
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
