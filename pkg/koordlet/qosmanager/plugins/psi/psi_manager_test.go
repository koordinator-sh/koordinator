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

package psi

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/operator"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
)

type testOperator struct {
	name    string
	deleted int
}

func (t *testOperator) Init() error { return nil }

func (t *testOperator) Name() string { return t.name }

func (t *testOperator) Update(op operator.Operator) error {
	next, ok := op.(*testOperator)
	if !ok {
		return fmt.Errorf("not a testOperator")
	}
	t.name = next.name
	return nil
}

func (t *testOperator) Exec(map[types.UID]*podcgroup.PodCgroup, *corev1.Node) error {
	return nil
}

func (t *testOperator) AddPod(*podcgroup.PodCgroup) error { return nil }

func (t *testOperator) DeletePod(*podcgroup.PodCgroup) error {
	t.deleted++
	return nil
}

func TestSyncOperatorsRemovesDisabledOperators(t *testing.T) {
	enabled := &testOperator{name: "enabled"}
	removed := &testOperator{name: "removed"}
	manager := NewManager(0, enabled, removed)
	manager.pods[types.UID("pod")] = &podcgroup.PodCgroup{
		Pod: &corev1.Pod{},
	}

	err := manager.SyncOperators(enabled)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := manager.operators["removed"]; ok {
		t.Fatalf("disabled operator was not removed")
	}
	if removed.deleted != 1 {
		t.Fatalf("expected disabled operator to delete existing pod once, got %d", removed.deleted)
	}
}

type overlapOperator struct {
	name       string
	active     *int32
	overlapped *atomic.Bool
}

func (o *overlapOperator) Init() error { return nil }

func (o *overlapOperator) Name() string { return o.name }

func (o *overlapOperator) Update(op operator.Operator) error { return nil }

func (o *overlapOperator) Exec(map[types.UID]*podcgroup.PodCgroup, *corev1.Node) error {
	if atomic.AddInt32(o.active, 1) > 1 {
		o.overlapped.Store(true)
	}
	time.Sleep(time.Millisecond)
	atomic.AddInt32(o.active, -1)
	return nil
}

func (o *overlapOperator) AddPod(*podcgroup.PodCgroup) error { return nil }

func (o *overlapOperator) DeletePod(*podcgroup.PodCgroup) error { return nil }

func TestReconcileExecutesOperatorsSerially(t *testing.T) {
	var active int32
	overlapped := &atomic.Bool{}
	manager := NewManager(0,
		&overlapOperator{name: "b", active: &active, overlapped: overlapped},
		&overlapOperator{name: "a", active: &active, overlapped: overlapped},
	)

	err := manager.Reconcile(&corev1.Node{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if overlapped.Load() {
		t.Fatalf("operators executed concurrently")
	}
}
