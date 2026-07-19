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

package batch

import (
	"sync"

	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/backend/cache"
)

// snapshotListerFramework is the subset of the scheduler framework used to swap in a per-node
// snapshot lister. The per-node snapshot injection relies on the koordinator scheduler fork's
// SetSnapshotLister/ClearSnapshotLister; on this upstream build there is no such override, so the
// default hooks below are no-ops and the interface is intentionally empty (both
// schedulerframework.Framework and frameworkext.FrameworkExtender satisfy it).
type snapshotListerFramework interface{}

// prepareNodeSnapshot optionally injects a per-node snapshot lister for the batch scheduling
// cycle and stores it in nodeSnapshot for the later permit/binding phases. The returned func restores
// the previous lister and must be deferred by the caller.
//
// This is an overridable package-level hook: the default implementation is a no-op (the framework's
// default shared snapshot lister is used). An optional build may replace it, gated by the
// EnableBatchScheduleNodeSnapshot feature, to inject a per-node snapshot; the callers work either way.
var prepareNodeSnapshot = func(logger klog.Logger, c cache.Cache, fwk snapshotListerFramework, nodeSnapshot *sync.Map, nodeName string) func() {
	return func() {}
}

// applyNodeSnapshotLister swaps in the per-node snapshot stored by prepareNodeSnapshot (when present)
// as the framework's snapshot lister. The returned func restores the previous lister and must be
// deferred by the caller.
//
// This is an overridable package-level hook: the default implementation is a no-op, and an optional
// build may replace it (gated by EnableBatchScheduleNodeSnapshot).
var applyNodeSnapshotLister = func(fwk snapshotListerFramework, nodeSnapshot *sync.Map, nodeName string) func() {
	return func() {}
}
