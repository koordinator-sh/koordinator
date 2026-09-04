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

package loadaware

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// newFakeDynClient builds a fake dynamic client with the NodeMetric GVR
// registered so List/DeleteCollection calls succeed.
func newFakeDynClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		nodeMetricGVR: "NodeMetricList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objs...)
}

// fakeNodeList builds a k8sfake.Clientset pre-populated with N kwok nodes
// carrying the given runID label. Each node has 32 CPU and 256Gi allocatable
// (matching kwok-bench defaults).
func fakeNodeList(t *testing.T, runID string, count int) *k8sfake.Clientset {
	t.Helper()
	nodes := make([]runtime.Object, 0, count)
	cpu := resource.MustParse("32")
	mem := resource.MustParse("256Gi")
	for i := 0; i < count; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("kwok-bench-node-abc12345-%04d", i),
				Labels: map[string]string{
					types.RunIDLabel: runID,
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    cpu,
					corev1.ResourceMemory: mem,
				},
			},
		})
	}
	return k8sfake.NewSimpleClientset(nodes...)
}

func validCfg() types.ScenarioConfig {
	return types.ScenarioConfig{
		Name:              "loadaware",
		Namespace:         "loadaware-benchmark",
		NodeCount:         10,
		PodCount:          100,
		Concurrency:       5,
		ClientQPS:         100,
		ClientBurst:       200,
		HighUtilNodeCount: 7,
		HighUtilCPUPct:    80,
		ResourceRequests:  map[string]string{"cpu": "500m", "memory": "512Mi"},
	}
}

// TestSetup_EmptyNamespace verifies Setup returns an error when Namespace is empty.
func TestSetup_EmptyNamespace(t *testing.T) {
	cfg := validCfg()
	cfg.Namespace = ""
	s := &LoadAwareScenario{}
	err := s.Setup(context.Background(), k8sfake.NewSimpleClientset(), newFakeDynClient(), cfg, "run-1")
	if err == nil {
		t.Fatal("Setup() with empty namespace should return error")
	}
}

// TestSetup_NodeMetricFieldShape verifies each created NodeMetric has the
// correct apiVersion, RunIDLabel, and a "resources.cpu" field in its status
// (via UpdateStatus call).
//
// The fake dynamic client does not actually execute UpdateStatus (it records it
// but the object state after Create has no status). We verify:
//   - the correct number of NodeMetric objects exist post-Setup
//   - each carries the RunIDLabel label
//   - each has apiVersion == "slo.koordinator.sh/v1alpha1"
func TestSetup_NodeMetricFieldShape(t *testing.T) {
	cfg := validCfg()
	fakeK8s := fakeNodeList(t, "run-abc123", cfg.NodeCount)
	fakeDyn := newFakeDynClient()

	s := &LoadAwareScenario{}
	if err := s.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-abc123"); err != nil {
		t.Fatalf("Setup() returned unexpected error: %v", err)
	}

	list, err := fakeDyn.Resource(nodeMetricGVR).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("List(NodeMetric) error: %v", err)
	}
	if len(list.Items) != cfg.NodeCount {
		t.Fatalf("len(NodeMetrics) = %d, want %d", len(list.Items), cfg.NodeCount)
	}

	for i, nm := range list.Items {
		if nm.GetAPIVersion() != "slo.koordinator.sh/v1alpha1" {
			t.Errorf("NodeMetric %d: apiVersion = %q, want slo.koordinator.sh/v1alpha1", i, nm.GetAPIVersion())
		}
		if got := nm.GetLabels()[types.RunIDLabel]; got != "run-abc123" {
			t.Errorf("NodeMetric %d: RunIDLabel = %q, want run-abc123", i, got)
		}
	}
}

// TestSetup_HighLowSplit verifies that the first cfg.HighUtilNodeCount nodes end
// up classified as high-utilization (NOT in lowUtilNodes) and the remainder are
// classified as low-utilization (in lowUtilNodes).
func TestSetup_HighLowSplit(t *testing.T) {
	cfg := validCfg()
	fakeK8s := fakeNodeList(t, "run-1", cfg.NodeCount)
	fakeDyn := newFakeDynClient()

	s := &LoadAwareScenario{}
	if err := s.Setup(context.Background(), fakeK8s, fakeDyn, cfg, "run-1"); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	expectedLow := cfg.NodeCount - cfg.HighUtilNodeCount
	if len(s.lowUtilNodes) != expectedLow {
		t.Errorf("len(lowUtilNodes) = %d, want %d", len(s.lowUtilNodes), expectedLow)
	}

	// Node names are deterministic (fakeNodeList names them 0000..N-1).
	// The first HighUtilNodeCount nodes should NOT be in lowUtilNodes.
	for i := 0; i < cfg.NodeCount; i++ {
		name := fmt.Sprintf("kwok-bench-node-abc12345-%04d", i)
		_, isLow := s.lowUtilNodes[name]
		if i < cfg.HighUtilNodeCount && isLow {
			t.Errorf("node %d (%q) should be high-util, but is in lowUtilNodes", i, name)
		}
		if i >= cfg.HighUtilNodeCount && !isLow {
			t.Errorf("node %d (%q) should be low-util, but is NOT in lowUtilNodes", i, name)
		}
	}
}

// TestPods_FieldShape verifies pod count, namespace, labels, scheduler name,
// kwok node selector, and kwok toleration.
func TestPods_FieldShape(t *testing.T) {
	cfg := validCfg()
	s := &LoadAwareScenario{}
	pods, err := s.Pods(cfg, "run-abc123")
	if err != nil {
		t.Fatalf("Pods() error: %v", err)
	}
	if len(pods) != cfg.PodCount {
		t.Fatalf("len(pods) = %d, want %d", len(pods), cfg.PodCount)
	}
	for i, p := range pods {
		if p.Namespace != cfg.Namespace {
			t.Errorf("pod %d: namespace = %q, want %q", i, p.Namespace, cfg.Namespace)
		}
		if got := p.Labels[types.RunIDLabel]; got != "run-abc123" {
			t.Errorf("pod %d: RunIDLabel = %q, want run-abc123", i, got)
		}
		if got := p.Labels["app"]; got != "kwok-bench-loadaware" {
			t.Errorf("pod %d: app = %q, want kwok-bench-loadaware", i, got)
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

// TestPods_LabelMergeOrder is the regression guard for the cfg.Labels-first
// convention: a config-supplied RunIDLabel must be overwritten by the
// authoritative runID so Watcher/FailureWatcher select correctly.
func TestPods_LabelMergeOrder(t *testing.T) {
	cfg := validCfg()
	cfg.Labels = map[string]string{
		types.RunIDLabel: "attacker-supplied-run-id",
		"custom-label":   "preserved",
	}
	s := &LoadAwareScenario{}
	pods, err := s.Pods(cfg, "real-run-id")
	if err != nil {
		t.Fatalf("Pods() error: %v", err)
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
	s := &LoadAwareScenario{}
	if _, err := s.Pods(cfg, "run-1"); err == nil {
		t.Error("Pods() with invalid resource quantity should return error")
	}
}
