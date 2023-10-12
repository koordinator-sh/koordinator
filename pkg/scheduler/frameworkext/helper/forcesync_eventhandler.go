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
	"reflect"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type forceSyncEventHandler struct {
	handler      cache.ResourceEventHandler
	syncCh       chan struct{}
	objects      map[apimachinerytypes.UID]int64
	resyncPeriod time.Duration
}

func newForceSyncEventHandler(handler cache.ResourceEventHandler, options ...Option) *forceSyncEventHandler {
	h := &forceSyncEventHandler{
		handler: handler,
		syncCh:  make(chan struct{}, 1),
		objects: map[apimachinerytypes.UID]int64{},
	}
	for _, fn := range options {
		fn(h)
	}
	return h
}

func (h *forceSyncEventHandler) syncDone() {
	close(h.syncCh)
}

func (h *forceSyncEventHandler) waitForSyncDone() {
	<-h.syncCh
}

func (h *forceSyncEventHandler) OnAdd(obj interface{}) {
	h.waitForSyncDone()
	if metaAccessor, ok := obj.(metav1.ObjectMetaAccessor); ok {
		objectMeta := metaAccessor.GetObjectMeta()
		objectUID := objectMeta.GetUID()
		if oldResourceVersion, ok := h.objects[objectUID]; ok && oldResourceVersion != 0 {
			resourceVersion, err := strconv.ParseInt(objectMeta.GetResourceVersion(), 10, 64)
			if err == nil && resourceVersion <= oldResourceVersion {
				return
			}
			delete(h.objects, objectUID)
			klog.Warningf("Object %q has been updated multiple times in a short period of time", klog.KObj(objectMeta).String())
		}
	}
	if h.handler != nil {
		h.handler.OnAdd(obj)
	}
}

func (h *forceSyncEventHandler) OnUpdate(oldObj, newObj interface{}) {
	h.waitForSyncDone()
	if h.objects != nil {
		// Release objects map to reduce memory usage and reduce GC pressure
		h.objects = nil
	}
	if h.handler != nil {
		h.handler.OnUpdate(oldObj, newObj)
	}
}

func (h *forceSyncEventHandler) OnDelete(obj interface{}) {
	h.waitForSyncDone()
	if h.objects != nil {
		// Release objects map to reduce memory usage and reduce GC pressure
		h.objects = nil
	}
	if h.handler != nil {
		h.handler.OnDelete(obj)
	}
}

func (h *forceSyncEventHandler) addDirectly(obj interface{}) {
	if h.handler == nil {
		return
	}
	h.handler.OnAdd(obj)
	if metaAccessor, ok := obj.(metav1.ObjectMetaAccessor); ok {
		objectMeta := metaAccessor.GetObjectMeta()
		resourceVersion, err := strconv.ParseInt(objectMeta.GetResourceVersion(), 10, 64)
		if err == nil {
			h.objects[objectMeta.GetUID()] = resourceVersion
		}
	}
}

type CacheSyncer interface {
	Start(stopCh <-chan struct{})
	WaitForCacheSync(stopCh <-chan struct{}) map[reflect.Type]bool
}

type Option func(*forceSyncEventHandler)

func WithResyncPeriod(resyncPeriod time.Duration) Option {
	return func(handler *forceSyncEventHandler) {
		handler.resyncPeriod = resyncPeriod
	}
}

// ForceSyncFromInformer ensures that the EventHandler will synchronize data immediately after registration,
// helping those plugins that need to build memory status through EventHandler to correctly synchronize data
func ForceSyncFromInformer(stopCh <-chan struct{}, cacheSyncer CacheSyncer, informer cache.SharedInformer, handler cache.ResourceEventHandler, options ...Option) {
	syncEventHandler := newForceSyncEventHandler(handler, options...)
	informer.AddEventHandlerWithResyncPeriod(syncEventHandler, syncEventHandler.resyncPeriod)
	if cacheSyncer != nil {
		cacheSyncer.Start(stopCh)
		cacheSyncer.WaitForCacheSync(stopCh)
	}
	allObjects := informer.GetStore().List()
	for _, obj := range allObjects {
		syncEventHandler.addDirectly(obj)
	}
	syncEventHandler.syncDone()
	return
}
