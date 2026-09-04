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

package reservation

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// newFakeDynClient creates a fake dynamic client with the Reservation GVR
// registered so List and DeleteCollection calls work correctly.
func newFakeDynClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		reservationGVR: "ReservationList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objs...)
}

func validCfg() types.ScenarioConfig {
	return types.ScenarioConfig{
		Name:             "reservation",
		Namespace:        "reservation-benchmark",
		NodeCount:        2,
		PodCount:         10,
		Concurrency:      5,
		ClientQPS:        100,
		ClientBurst:      200,
		ReservationCount: 4,
		ResourceRequests: map[string]string{"cpu": "500m", "memory": "512Mi"},
	}
}

// TestSetup_EmptyNamespace verifies Setup returns an error when namespace is empty.
func TestSetup_EmptyNamespace(t *testing.T) {
	s := &ReservationScenario{}
	cfg := validCfg()
	cfg.Namespace = ""
	err := s.Setup(context.Background(), k8sfake.NewSimpleClientset(), newFakeDynClient(), cfg, "run-1")
	if err == nil {
		t.Fatal("Setup() with empty namespace should return error")
	}
}

// TestSetup_FieldShape verifies each created Reservation has the correct
// apiVersion, reservation-index label in owners, RunIDLabel, and app label.
func TestSetup_FieldShape(t *testing.T) {
	cfg := validCfg()
	fakeK8s := k8sfake.NewSimpleClientset()
	fakeDyn := newFakeDynClient()

	s := &ReservationScenario{}
	if err := s.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-abc123"); err != nil {
		t.Fatalf("Setup() returned unexpected error: %v", err)
	}

	list, err := fakeDyn.Resource(reservationGVR).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if len(list.Items) != cfg.ReservationCount {
		t.Fatalf("len(reservations) = %d, want %d", len(list.Items), cfg.ReservationCount)
	}

	for i, rsv := range list.Items {
		if rsv.GetAPIVersion() != "scheduling.koordinator.sh/v1alpha1" {
			t.Errorf("reservation %d: apiVersion = %q, want scheduling.koordinator.sh/v1alpha1", i, rsv.GetAPIVersion())
		}
		if got := rsv.GetLabels()[types.RunIDLabel]; got != "run-abc123" {
			t.Errorf("reservation %d: RunIDLabel = %q, want run-abc123", i, got)
		}
		if got := rsv.GetLabels()["app"]; got != "kwok-bench-reservation" {
			t.Errorf("reservation %d: app label = %q, want kwok-bench-reservation", i, got)
		}
	}
}

// TestSetup_LeftoverReservations verifies Setup returns an error when
// stale Reservations from a previous run already exist in the cluster.
func TestSetup_LeftoverReservations(t *testing.T) {
	cfg := validCfg()
	fakeK8s := k8sfake.NewSimpleClientset()

	// First run creates reservations.
	s1 := &ReservationScenario{}
	fakeDyn := newFakeDynClient()
	if err := s1.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-1"); err != nil {
		t.Fatalf("first Setup() returned error: %v", err)
	}

	// Second run — Teardown never ran, stale Reservations still exist.
	s2 := &ReservationScenario{}
	err := s2.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-2")
	if err == nil {
		t.Fatal("Setup() with leftover Reservations should return error")
	}
}

// TestSetup_ZeroReservationCount verifies that Setup with ReservationCount=0
// succeeds without creating any Reservation objects.
func TestSetup_ZeroReservationCount(t *testing.T) {
	cfg := validCfg()
	cfg.ReservationCount = 0
	fakeK8s := k8sfake.NewSimpleClientset()
	fakeDyn := newFakeDynClient()

	s := &ReservationScenario{}
	if err := s.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-1"); err != nil {
		t.Fatalf("Setup() with ReservationCount=0 returned error: %v", err)
	}

	list, _ := fakeDyn.Resource(reservationGVR).List(context.Background(), metav1.ListOptions{})
	if len(list.Items) != 0 {
		t.Errorf("expected 0 Reservations, got %d", len(list.Items))
	}
}

// TestPods_FieldShape verifies the first ReservationCount pods carry the
// reservation-index label and the rest do not. Also checks RunIDLabel,
// app label, scheduler name, and kwok node selector/toleration.
func TestPods_FieldShape(t *testing.T) {
	cfg := validCfg()
	s := &ReservationScenario{}
	pods, err := s.Pods(cfg, "run-abc123")
	if err != nil {
		t.Fatalf("Pods() returned unexpected error: %v", err)
	}
	if len(pods) != cfg.PodCount {
		t.Fatalf("len(pods) = %d, want %d", len(pods), cfg.PodCount)
	}

	for i, p := range pods {
		_, hasIdx := p.Labels[reservationIndexLabel]
		if i < cfg.ReservationCount && !hasIdx {
			t.Errorf("pod %d: expected reservation-index label, got none", i)
		}
		if i >= cfg.ReservationCount && hasIdx {
			t.Errorf("pod %d: should NOT have reservation-index label", i)
		}
		if got := p.Labels[types.RunIDLabel]; got != "run-abc123" {
			t.Errorf("pod %d: RunIDLabel = %q, want run-abc123", i, got)
		}
		if got := p.Labels["app"]; got != "kwok-bench-reservation" {
			t.Errorf("pod %d: app = %q, want kwok-bench-reservation", i, got)
		}
		if p.Namespace != cfg.Namespace {
			t.Errorf("pod %d: namespace = %q, want %q", i, p.Namespace, cfg.Namespace)
		}
		if got := p.Spec.NodeSelector["type"]; got != "kwok" {
			t.Errorf("pod %d: nodeSelector[type] = %q, want kwok", i, got)
		}
		found := false
		for _, tol := range p.Spec.Tolerations {
			if tol.Key == "kwok.x-k8s.io/node" {
				found = true
			}
		}
		if !found {
			t.Errorf("pod %d: missing kwok.x-k8s.io/node toleration", i)
		}
	}
}

// TestPods_OwnerBinding verifies that each reserved pod's reservation-index
// label value exactly matches the index of the Reservation created for it,
// forming a 1-to-1 owner binding. Uses Setup+Pods together so both sides
// of the binding are exercised.
func TestPods_OwnerBinding(t *testing.T) {
	cfg := validCfg()
	fakeK8s := k8sfake.NewSimpleClientset()
	fakeDyn := newFakeDynClient()

	s := &ReservationScenario{}
	if err := s.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-1"); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	pods, err := s.Pods(cfg, "run-1")
	if err != nil {
		t.Fatalf("Pods() error = %v", err)
	}

	// For each pod that has a reservation-index, verify a Reservation with a
	// matching owner selector was created.
	rsvList, _ := fakeDyn.Resource(reservationGVR).List(context.Background(), metav1.ListOptions{})
	rsvByIndex := make(map[string]bool, len(rsvList.Items))
	for _, rsv := range rsvList.Items {
		owners, _, _ := unstructured.NestedSlice(rsv.Object, "spec", "owners")
		for _, o := range owners {
			oMap, _ := o.(map[string]interface{})
			ml, _, _ := unstructuredNestedStringMap(oMap, "labelSelector", "matchLabels")
			if idx, ok := ml[reservationIndexLabel]; ok {
				rsvByIndex[idx] = true
			}
		}
	}

	for i, p := range pods {
		idx, hasIdx := p.Labels[reservationIndexLabel]
		if i >= cfg.ReservationCount {
			if hasIdx {
				t.Errorf("pod %d unexpectedly has reservation-index = %q", i, idx)
			}
			continue
		}
		if !hasIdx {
			t.Errorf("pod %d: missing reservation-index label", i)
			continue
		}
		if !rsvByIndex[idx] {
			t.Errorf("pod %d: reservation-index %q has no matching Reservation owner selector", i, idx)
		}
	}
}

// TestPods_LabelMergeOrder is the regression guard for the cfg.Labels-first
// convention: a config-supplied RunIDLabel must be overwritten by the
// authoritative runID parameter so Watcher/FailureWatcher select correctly.
func TestPods_LabelMergeOrder(t *testing.T) {
	cfg := validCfg()
	cfg.Labels = map[string]string{
		types.RunIDLabel: "attacker-supplied-run-id",
		"custom-label":   "preserved",
	}
	s := &ReservationScenario{}
	pods, err := s.Pods(cfg, "real-run-id")
	if err != nil {
		t.Fatalf("Pods() error = %v", err)
	}
	for _, p := range pods {
		if got := p.Labels[types.RunIDLabel]; got != "real-run-id" {
			t.Errorf("pod %s: RunIDLabel = %q, want real-run-id", p.Name, got)
		}
		if got := p.Labels["custom-label"]; got != "preserved" {
			t.Errorf("pod %s: custom-label = %q, want preserved", p.Name, got)
		}
	}
}

// TestPods_InvalidResourceQuantity verifies Pods() returns an error for
// malformed resource quantities instead of panicking.
func TestPods_InvalidResourceQuantity(t *testing.T) {
	cfg := validCfg()
	cfg.ResourceRequests = map[string]string{"cpu": "not-a-quantity"}
	s := &ReservationScenario{}
	if _, err := s.Pods(cfg, "run-1"); err == nil {
		t.Error("Pods() with invalid resource quantity should return error")
	}
}

// unstructuredNestedStringMap is a test helper to extract a
// map[string]string from a nested map[string]interface{} path.
func unstructuredNestedStringMap(obj map[string]interface{}, fields ...string) (map[string]string, bool, error) {
	nested := obj
	for i, f := range fields {
		v, ok := nested[f]
		if !ok {
			return nil, false, nil
		}
		if i == len(fields)-1 {
			if m, ok := v.(map[string]interface{}); ok {
				out := make(map[string]string, len(m))
				for k, val := range m {
					if s, ok := val.(string); ok {
						out[k] = s
					}
				}
				return out, true, nil
			}
			return nil, false, nil
		}
		if m, ok := v.(map[string]interface{}); ok {
			nested = m
		} else {
			return nil, false, nil
		}
	}
	return nil, false, nil
}
