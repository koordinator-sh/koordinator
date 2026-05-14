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
	"sync"
)

// AfterInformersSyncedHook is a function invoked at a well-defined phase of the
// scheduler startup pipeline, after a set of informer factories has been started
// and their caches have synced. Plugins register hooks to perform initialization
// that must run at a specific point relative to informer readiness (for example,
// rebuilding plugin internal state from an initial-list snapshot obtained from a
// lister). A non-nil return value aborts the startup pipeline so the scheduler
// can shut down gracefully.
type AfterInformersSyncedHook func(ctx context.Context) error

var (
	startupHooksMu                  sync.Mutex
	afterPluginInformersSyncedHooks []AfterInformersSyncedHook
	afterAllInformersSyncedHooks    []AfterInformersSyncedHook
)

// RegisterAfterPluginInformersSynced registers a hook invoked by the scheduler
// startup pipeline AFTER plugin-private informer factories (registered via
// InformerFactoryProvider) have been started and their caches have synced, and
// BEFORE the scheduler's main informer factories (pods/nodes/etc.) are started.
//
// This is the canonical place to rebuild plugin internal state from an
// initial-list snapshot taken via a lister, so that downstream event handlers
// on the main informers observe a fully initialized state when they start firing.
// It supersedes the older "replaceHandler" pattern for plugins that need a
// whole-snapshot initialization anchored to a specific startup phase.
func RegisterAfterPluginInformersSynced(hook AfterInformersSyncedHook) {
	if hook == nil {
		return
	}
	startupHooksMu.Lock()
	defer startupHooksMu.Unlock()
	afterPluginInformersSyncedHooks = append(afterPluginInformersSyncedHooks, hook)
}

// RegisterAfterAllInformersSynced registers a hook invoked by the scheduler
// startup pipeline AFTER all informer factories (plugin + main) have been
// started and their caches and handler registrations have synced, but BEFORE
// the scheduler begins dispatching pods. Typical uses: kicking off background
// reconcilers that rely on fully populated caches.
func RegisterAfterAllInformersSynced(hook AfterInformersSyncedHook) {
	if hook == nil {
		return
	}
	startupHooksMu.Lock()
	defer startupHooksMu.Unlock()
	afterAllInformersSyncedHooks = append(afterAllInformersSyncedHooks, hook)
}

// RunAfterPluginInformersSynced invokes all hooks registered via
// RegisterAfterPluginInformersSynced in registration order. It aborts and
// returns the first non-nil error so callers can fail fast on initialization
// failure. ctx cancellation between hooks is surfaced as ctx.Err().
func RunAfterPluginInformersSynced(ctx context.Context) error {
	return runStartupHooks(ctx, snapshotHooks(&afterPluginInformersSyncedHooks))
}

// RunAfterAllInformersSynced invokes all hooks registered via
// RegisterAfterAllInformersSynced in registration order, aborting on the first
// non-nil error.
func RunAfterAllInformersSynced(ctx context.Context) error {
	return runStartupHooks(ctx, snapshotHooks(&afterAllInformersSyncedHooks))
}

func snapshotHooks(hooks *[]AfterInformersSyncedHook) []AfterInformersSyncedHook {
	startupHooksMu.Lock()
	defer startupHooksMu.Unlock()
	out := make([]AfterInformersSyncedHook, len(*hooks))
	copy(out, *hooks)
	return out
}

func runStartupHooks(ctx context.Context, hooks []AfterInformersSyncedHook) error {
	for _, h := range hooks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := h(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ResetStartupHooks clears all registered startup hooks. Only for testing.
func ResetStartupHooks() {
	startupHooksMu.Lock()
	afterPluginInformersSyncedHooks = nil
	afterAllInformersSyncedHooks = nil
	startupHooksMu.Unlock()
}
