/*
Copyright 2025 The Koordinator Authors.

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

package reconciler

import (
	"testing"
	"time"
)

// a large interval keeps the timer branch from firing so the test only exercises the stopCh path.
const stopTestInterval = time.Hour

// assertReturnsBeforeTimeout fails if fn does not return once stopCh is closed.
func assertReturnsBeforeTimeout(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconcile loop did not return after stopCh was closed")
	}
}

func Test_hostReconciler_reconcile_exitsOnStop(t *testing.T) {
	r := &hostReconciler{
		appUpdated:        make(chan struct{}, 1),
		reconcileInterval: stopTestInterval,
	}
	stopCh := make(chan struct{})
	close(stopCh)
	assertReturnsBeforeTimeout(t, func() { r.reconcile(stopCh) })
}

func Test_reconciler_reconcileKubeQOSCgroup_exitsOnStop(t *testing.T) {
	c := &reconciler{
		reconcileInterval: stopTestInterval,
	}
	stopCh := make(chan struct{})
	close(stopCh)
	assertReturnsBeforeTimeout(t, func() { c.reconcileKubeQOSCgroup(stopCh) })
}
