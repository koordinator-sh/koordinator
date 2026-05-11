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

package helper

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/informers/core"
	"k8s.io/client-go/informers/internalinterfaces"
	"k8s.io/client-go/informers/storage"
	"k8s.io/client-go/tools/cache"
)

// forceSyncSharedInformerFactory wraps SharedInformerFactory so that every informer
// obtained via InformerFor is wrapped with forceSyncsharedIndexInformer, which
// intercepts AddEventHandler / AddEventHandlerWithResyncPeriod calls and collects
// the returned ResourceEventHandlerRegistration for WaitForHandlersSync.

var _ informers.SharedInformerFactory = &forceSyncSharedInformerFactory{}

type forceSyncSharedInformerFactory struct {
	informers.SharedInformerFactory
	namespace        string
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	defaultResync    time.Duration
}

func NewForceSyncSharedInformerFactory(factory informers.SharedInformerFactory) informers.SharedInformerFactory {
	return &forceSyncSharedInformerFactory{
		SharedInformerFactory: factory,
		namespace:             corev1.NamespaceAll,
		defaultResync:         0,
	}
}

func (f *forceSyncSharedInformerFactory) InformerFor(obj runtime.Object, newFunc internalinterfaces.NewInformerFunc) cache.SharedIndexInformer {
	informer := f.SharedInformerFactory.InformerFor(obj, newFunc)
	return newForceSyncSharedIndexInformer(informer, f.defaultResync)
}

func (f *forceSyncSharedInformerFactory) Core() core.Interface {
	return core.New(f, f.namespace, f.tweakListOptions)
}

func (f *forceSyncSharedInformerFactory) Storage() storage.Interface {
	return storage.New(f, f.namespace, f.tweakListOptions)
}
