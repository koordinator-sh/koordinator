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

type FrameworkExtender struct {
	once sync.Once
	framework.Handle
	koordinatorClientSet             koordinatorclientset.Interface
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory
}

func NewFrameworkExtender(
	koordinatorClientSet koordinatorclientset.Interface,
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory,
) *FrameworkExtender {
	return &FrameworkExtender{
		koordinatorClientSet:             koordinatorClientSet,
		koordinatorSharedInformerFactory: koordinatorSharedInformerFactory,
	}
}

func (ext *FrameworkExtender) KoordinatorClientSet() koordinatorclientset.Interface {
	return ext.koordinatorClientSet
}

func (ext *FrameworkExtender) KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory {
	return ext.koordinatorSharedInformerFactory
}

func PluginFactoryProxy(ext *FrameworkExtender, factoryFn frameworkruntime.PluginFactory) frameworkruntime.PluginFactory {
	return func(args runtime.Object, handle framework.Handle) (framework.Plugin, error) {
		ext.once.Do(func() {
			ext.Handle = handle
		})
		return factoryFn(args, ext)
	}
}
