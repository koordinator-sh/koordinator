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

package elasticquota

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// largeNode returns a node with enough allocatable CPU and memory that the
// validateQuotaFits check always passes for the test quota (150 CPU / 300Gi).
func largeNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("3200"),
				corev1.ResourceMemory: resource.MustParse("25Ti"),
			},
		},
	}
}

// newFakeDynClient creates a fake dynamic client with the ElasticQuota GVR
// registered so cross-namespace List calls (used by validateQuotaFits) work.
func newFakeDynClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		elasticQuotaGVR: "ElasticQuotaList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objs...)
}

func validCfg() types.ScenarioConfig {
	return types.ScenarioConfig{
		Name:        "elasticquota",
		Namespace:   "elasticquota-benchmark",
		NodeCount:   1,
		PodCount:    10,
		Concurrency: 5,
		ClientQPS:   100,
		ClientBurst: 200,
		QuotaCPU:    "150",
		QuotaMemory: "300Gi",
	}
}

func TestSetup_EmptyNamespace(t *testing.T) {
	s := &ElasticQuotaScenario{}
	cfg := validCfg()
	cfg.Namespace = ""
	err := s.Setup(context.Background(), k8sfake.NewSimpleClientset(), newFakeDynClient(), cfg, "run-1")
	if err == nil {
		t.Fatal("Setup() with empty namespace should return error")
	}
}

func TestSetup_MissingOrInvalidQuotaFields(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutateCfg func(*types.ScenarioConfig)
	}{
		{"empty quotaCPU", func(c *types.ScenarioConfig) { c.QuotaCPU = "" }},
		{"empty quotaMemory", func(c *types.ScenarioConfig) { c.QuotaMemory = "" }},
		{"invalid quotaCPU", func(c *types.ScenarioConfig) { c.QuotaCPU = "bad-value" }},
		{"invalid quotaMemory", func(c *types.ScenarioConfig) { c.QuotaMemory = "bad-value" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ElasticQuotaScenario{}
			cfg := validCfg()
			tc.mutateCfg(&cfg)
			fakeK8s := k8sfake.NewSimpleClientset(largeNode())
			err := s.Setup(context.Background(), fakeK8s, newFakeDynClient(), cfg, "run-1")
			if err == nil {
				t.Fatalf("Setup() with %s should return error", tc.name)
			}
		})
	}
}

// TestSetup_FieldShape verifies the generated ElasticQuota has the correct
// apiVersion, name (== namespace), run-id label, and min == max spec.
func TestSetup_FieldShape(t *testing.T) {
	s := &ElasticQuotaScenario{}
	cfg := validCfg()
	fakeK8s := k8sfake.NewSimpleClientset(largeNode())
	fakeDyn := newFakeDynClient()

	if err := s.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-1"); err != nil {
		t.Fatalf("Setup() returned unexpected error: %v", err)
	}

	got, err := fakeDyn.Resource(elasticQuotaGVR).Namespace(cfg.Namespace).Get(
		context.Background(), cfg.Namespace, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}

	if got.GetAPIVersion() != "scheduling.sigs.k8s.io/v1alpha1" {
		t.Errorf("apiVersion = %q, want %q", got.GetAPIVersion(), "scheduling.sigs.k8s.io/v1alpha1")
	}
	// quota name must equal the namespace name — not a run-id suffix
	if got.GetName() != cfg.Namespace {
		t.Errorf("name = %q, want %q (namespace name)", got.GetName(), cfg.Namespace)
	}
	if got.GetLabels()[types.RunIDLabel] != "run-1" {
		t.Errorf("label %q = %q, want %q", types.RunIDLabel, got.GetLabels()[types.RunIDLabel], "run-1")
	}

	// min == max is the reproducibility invariant (see package doc)
	maxCPU, _, _ := unstructured.NestedString(got.Object, "spec", "max", "cpu")
	minCPU, _, _ := unstructured.NestedString(got.Object, "spec", "min", "cpu")
	maxMem, _, _ := unstructured.NestedString(got.Object, "spec", "max", "memory")
	minMem, _, _ := unstructured.NestedString(got.Object, "spec", "min", "memory")

	if maxCPU != cfg.QuotaCPU {
		t.Errorf("spec.max.cpu = %q, want %q", maxCPU, cfg.QuotaCPU)
	}
	if minCPU != maxCPU {
		t.Errorf("spec.min.cpu = %q, want == spec.max.cpu (%q)", minCPU, maxCPU)
	}
	if maxMem != cfg.QuotaMemory {
		t.Errorf("spec.max.memory = %q, want %q", maxMem, cfg.QuotaMemory)
	}
	if minMem != maxMem {
		t.Errorf("spec.min.memory = %q, want == spec.max.memory (%q)", minMem, maxMem)
	}
}

// TestSetup_AlreadyExists verifies that Setup handles a pre-existing quota
// (left behind by a crashed prior run) with an in-place Update instead of
// returning an AlreadyExists error and failing every subsequent run.
func TestSetup_AlreadyExists(t *testing.T) {
	cfg := validCfg()
	fakeK8s := k8sfake.NewSimpleClientset(largeNode())
	fakeDyn := newFakeDynClient()

	// First run — creates the quota with run-1 label.
	s1 := &ElasticQuotaScenario{}
	if err := s1.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-1"); err != nil {
		t.Fatalf("first Setup() returned error: %v", err)
	}

	// Second run — new scenario instance (simulating process restart after crash).
	// Teardown never ran, so the quota named after the namespace still exists.
	s2 := &ElasticQuotaScenario{}
	if err := s2.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-2"); err != nil {
		t.Fatalf("second Setup() with pre-existing quota returned error: %v", err)
	}

	// Quota must still exist and now carry the new run-id label.
	got, err := fakeDyn.Resource(elasticQuotaGVR).Namespace(cfg.Namespace).Get(
		context.Background(), cfg.Namespace, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("Get() after second Setup returned error: %v", err)
	}
	if got.GetLabels()[types.RunIDLabel] != "run-2" {
		t.Errorf("label %q = %q after update, want %q",
			types.RunIDLabel, got.GetLabels()[types.RunIDLabel], "run-2")
	}
}
