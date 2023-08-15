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

// NOTE(joseph): When the K8s Scheduler Framework starts, the thread that constructs NodeInfo
// and the scheduling thread are not synchronized. In this way, when the Pod on a Node is not
// filled in the NodeInfo, the Node is scheduled for a new Pod. This behavior is not expected.
// The K8s community itself has also noticed this issue https://github.com/kubernetes/kubernetes/issues/116717,
// but it was only fixed in the K8s v1.28 version https://github.com/kubernetes/kubernetes/pull /116729.
// So we need to fix it ourselves.

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
	return newForceSyncSharedIndexInformer(informer, f.defaultResync, f.SharedInformerFactory)
}

func (f *forceSyncSharedInformerFactory) Core() core.Interface {
	return core.New(f, f.namespace, f.tweakListOptions)
}
func (f *forceSyncSharedInformerFactory) Storage() storage.Interface {
	return storage.New(f, f.namespace, f.tweakListOptions)
}
