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

package basic

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

const runIDLabel = "benchmark.koordinator.sh/run-id"

func init() {
	scenarios.Register(func() scenarios.Scenario { return &BasicScenario{} })
}

// BasicScenario is plain pod creation with no plugin-specific setup.
// It serves as the baseline all other scenarios are compared against.
type BasicScenario struct {
	// namespace is resolved during Setup and reused in Pods and Teardown
	// so all three methods always target the same namespace.
	namespace string
}

func (s *BasicScenario) Name() string { return "basic" }

// Setup ensures the benchmark namespace exists.
// Only creates the namespace on NotFound; other errors are returned
// immediately so they are not masked by a misleading create error.
func (s *BasicScenario) Setup(
	ctx context.Context,
	client kubernetes.Interface,
	_ dynamic.Interface,
	cfg types.ScenarioConfig,
	_ string,
) error {
	ns := cfg.Namespace
	if ns == "" {
		ns = "benchmark"
	}
	s.namespace = ns

	_, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get namespace %q: %w", ns, err)
	}
	_, createErr := client.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
		metav1.CreateOptions{},
	)
	if createErr != nil {
		return fmt.Errorf("failed to create namespace %q: %w", ns, createErr)
	}
	return nil
}

// Pods returns cfg.PodCount pod specs targeting kwok nodes.
// Returns an error for invalid resource quantity strings rather than
// panicking via resource.MustParse on user-supplied input.
func (s *BasicScenario) Pods(cfg types.ScenarioConfig, runID string) ([]*corev1.Pod, error) {
	ns := s.namespace
	if ns == "" {
		ns = "benchmark"
	}
	schedulerName := cfg.SchedulerName
	if schedulerName == "" {
		schedulerName = "koord-scheduler"
	}

	var podResources corev1.ResourceRequirements
	if len(cfg.ResourceRequests) > 0 {
		rl := corev1.ResourceList{}
		for k, v := range cfg.ResourceRequests {
			qty, err := resource.ParseQuantity(v)
			if err != nil {
				return nil, fmt.Errorf("invalid resource quantity %q=%q: %w", k, v, err)
			}
			rl[corev1.ResourceName(k)] = qty
		}
		podResources = corev1.ResourceRequirements{Requests: rl, Limits: rl}
	}

	labels := map[string]string{
		runIDLabel: runID,
		"app":      "kwok-bench",
	}
	for k, v := range cfg.Labels {
		labels[k] = v
	}
	if cfg.QoSClass != "" {
		labels["koordinator.sh/qosClass"] = cfg.QoSClass
	}

	runIDPrefix := runID
	if len(runIDPrefix) > 8 {
		runIDPrefix = runIDPrefix[:8]
	}
	pods := make([]*corev1.Pod, 0, cfg.PodCount)
	for i := 0; i < cfg.PodCount; i++ {
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("bench-pod-%s-%04d", runIDPrefix, i),
				Namespace:   ns,
				Labels:      labels,
				Annotations: cfg.Annotations,
			},
			Spec: corev1.PodSpec{
				SchedulerName: schedulerName,
				Containers: []corev1.Container{{
					Name:      "pause",
					Image:     "registry.k8s.io/pause:3.9",
					Resources: podResources,
				}},
				// NodeSelector and toleration restrict pods to kwok-simulated nodes.
				NodeSelector: map[string]string{"type": "kwok"},
				Tolerations: []corev1.Toleration{{
					Key:      "kwok.x-k8s.io/node",
					Operator: corev1.TolerationOpExists,
					Effect:   corev1.TaintEffectNoSchedule,
				}},
			},
		})
	}
	return pods, nil
}

// Teardown deletes all pods created during this run.
// Uses s.namespace set during Setup so pods are deleted from the correct
// namespace even when cfg.Namespace is overridden in the scenario YAML.
func (s *BasicScenario) Teardown(
	ctx context.Context,
	client kubernetes.Interface,
	_ dynamic.Interface,
	runID string,
) error {
	ns := s.namespace
	if ns == "" {
		ns = "benchmark"
	}
	policy := metav1.DeletePropagationBackground
	return client.CoreV1().Pods(ns).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", runIDLabel, runID)},
	)
}
