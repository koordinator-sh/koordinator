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

package kwok

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

const RunIDLabel = "benchmark.koordinator.sh/run-id"

// buildKwokNode constructs a fake Node object for the kwok controller to simulate as Ready.
// Direct Node creation via the API is the documented pattern for programmatic kwok usage;
// the kwok Stage configuration (stage-fast.yaml) handles the simulation side automatically.
// Uses stable kubernetes.io/os and kubernetes.io/arch labels instead of the
// deprecated beta.kubernetes.io/* equivalents.
func buildKwokNode(name, runID string, spec types.NodeSpec) (*corev1.Node, error) {
	cpu := spec.CPU
	if cpu == "" {
		cpu = "32"
	}
	memory := spec.Memory
	if memory == "" {
		memory = "256Gi"
	}
	maxPods := spec.MaxPods
	if maxPods == 0 {
		maxPods = 110
	}

	labels := map[string]string{
		"type":                   "kwok",
		"kubernetes.io/hostname": name,
		"kubernetes.io/os":       "linux",
		"kubernetes.io/arch":     "amd64",
		RunIDLabel:               runID,
	}
	for k, v := range spec.Labels {
		labels[k] = v
	}

	cpuQty, err := resource.ParseQuantity(cpu)
	if err != nil {
		return nil, fmt.Errorf("invalid CPU quantity %q: %w", cpu, err)
	}
	memQty, err := resource.ParseQuantity(memory)
	if err != nil {
		return nil, fmt.Errorf("invalid memory quantity %q: %w", memory, err)
	}

	rl := corev1.ResourceList{
		corev1.ResourceCPU:    cpuQty,
		corev1.ResourceMemory: memQty,
		corev1.ResourcePods:   *resource.NewQuantity(int64(maxPods), resource.DecimalSI),
	}

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				"node.alpha.kubernetes.io/ttl": "0",
				"kwok.x-k8s.io/node":          "fake",
			},
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			// kwok requires this taint; pods must carry the matching toleration.
			Taints: []corev1.Taint{{
				Key:    "kwok.x-k8s.io/node",
				Value:  "fake",
				Effect: corev1.TaintEffectNoSchedule,
			}},
		},
		Status: corev1.NodeStatus{
			Allocatable: rl,
			Capacity:    rl,
			Phase:       corev1.NodeRunning,
			Conditions: []corev1.NodeCondition{{
				Type:               corev1.NodeReady,
				Status:             corev1.ConditionTrue,
				LastHeartbeatTime:  metav1.Now(),
				LastTransitionTime: metav1.Now(),
				Reason:             "KwokReady",
				Message:            "kwok node is ready",
			}},
		},
	}, nil
}
