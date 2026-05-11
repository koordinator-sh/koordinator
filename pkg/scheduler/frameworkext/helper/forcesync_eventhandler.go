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
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
)

var (
	registrationsMu sync.Mutex
	registrations   []cache.ResourceEventHandlerRegistration
)

func addRegistration(reg cache.ResourceEventHandlerRegistration) {
	registrationsMu.Lock()
	defer registrationsMu.Unlock()
	registrations = append(registrations, reg)
}

// GetRegistrations returns a snapshot of all collected registrations.
func GetRegistrations() []cache.ResourceEventHandlerRegistration {
	registrationsMu.Lock()
	defer registrationsMu.Unlock()
	return append([]cache.ResourceEventHandlerRegistration{}, registrations...)
}

// ResetRegistrations clears all collected registrations and any startup hooks
// registered alongside them. Only for testing.
func ResetRegistrations() {
	registrationsMu.Lock()
	registrations = nil
	registrationsMu.Unlock()
	ResetStartupHooks()
}

// forceSyncEventHandler holds configuration for event handler registration.
type forceSyncEventHandler struct {
	resyncPeriod time.Duration
}

// CacheSyncer is the interface for starting informers and waiting for cache sync.
type CacheSyncer interface {
	Start(stopCh <-chan struct{})
	WaitForCacheSync(stopCh <-chan struct{}) map[reflect.Type]bool
}

// Option is a functional option for forceSyncEventHandler.
type Option func(*forceSyncEventHandler)

// WithResyncPeriod sets the resync period for the event handler registration.
func WithResyncPeriod(resyncPeriod time.Duration) Option {
	return func(handler *forceSyncEventHandler) {
		handler.resyncPeriod = resyncPeriod
	}
}

// ForceSyncFromInformer registers handler via standard AddEventHandler and collects
// the registration for WaitForHandlersSync.
// NOTE: The informer is NOT started here; it will be started later in startInformersAndWaitForSync.
// Function signature is kept unchanged for backward compatibility.
func ForceSyncFromInformer(stopCh <-chan struct{}, cacheSyncer CacheSyncer,
	informer cache.SharedInformer, handler cache.ResourceEventHandler,
	options ...Option) (cache.ResourceEventHandlerRegistration, error) {
	cfg := &forceSyncEventHandler{}
	for _, fn := range options {
		fn(cfg)
	}
	// Replace nil handler with a no-op handler to avoid panics when events are delivered.
	if handler == nil {
		handler = cache.ResourceEventHandlerFuncs{}
	}
	reg, err := informer.AddEventHandlerWithResyncPeriod(handler, cfg.resyncPeriod)
	if err != nil {
		return nil, err
	}
	// Avoid double-registration: forceSyncsharedIndexInformer.AddEventHandlerWithResyncPeriod
	// already calls addRegistration internally, so skip it here to prevent duplicates
	// in GetRegistrations() and redundant work in waitForKoordinatorHandlersSync.
	if _, isWrapper := informer.(*forceSyncsharedIndexInformer); !isWrapper {
		addRegistration(reg)
	}
	return reg, nil
}

// ForceSyncFromInformerWithReplace registers handler via ForceSyncFromInformer and, when
// replaceHandler is non-nil, registers an AfterPluginInformersSynced startup hook that
// invokes replaceHandler exactly once with a full snapshot of the informer's store. The
// scheduler startup pipeline runs these hooks AFTER the plugin informer factories have
// started and synced, but BEFORE the main informer factories (pods/nodes/etc.) deliver
// events; this gives downstream event handlers a clean happens-before anchor on the
// rebuilt plugin state. A non-nil error from replaceHandler aborts startup so the
// scheduler can shut down gracefully.
//
// IMPORTANT (discouraged for new call sites): the whole-snapshot "replace" semantics is
// a legacy shape kept for the ElasticQuota plugin. New plugins that need to initialize
// from an initial list should either (a) register an AfterPluginInformersSynced hook
// directly and pull the snapshot from a lister, or (b) use the plain ForceSyncFromInformer
// path with idempotent OnAdd/OnUpdate handling, which does not require any cross-factory
// startup ordering.
func ForceSyncFromInformerWithReplace(stopCh <-chan struct{}, cacheSyncer CacheSyncer,
	informer cache.SharedInformer, handler cache.ResourceEventHandler,
	replaceHandler func([]interface{}) error,
	options ...Option) (cache.ResourceEventHandlerRegistration, error) {
	reg, err := ForceSyncFromInformer(stopCh, cacheSyncer, informer, handler, options...)
	if err != nil || replaceHandler == nil {
		return reg, err
	}
	// Register a startup hook that fires AFTER the hosting plugin informer factory has
	// started and synced. At that point the registered handler has already consumed the
	// initial list, so informer.GetStore().List() returns a complete snapshot. The hook
	// runs synchronously relative to the startup pipeline, so its completion is totally
	// ordered before any main informer begins delivering events.
	RegisterAfterPluginInformersSynced(func(ctx context.Context) error {
		if !cache.WaitForCacheSync(ctx.Done(), reg.HasSynced) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("ForceSyncFromInformerWithReplace: handler registration never synced")
		}
		return replaceHandler(informer.GetStore().List())
	})
	return reg, nil
}

// WaitForHandlersSync waits until all collected handler registrations have synced.
// It is intended for use in tests: call it after starting informer factories so that
// all OnAdd events delivered by the initial list have been processed before assertions.
func WaitForHandlersSync(ctx context.Context) error {
	return wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		for _, reg := range GetRegistrations() {
			if !reg.HasSynced() {
				return false, nil
			}
		}
		return true, nil
	})
}
