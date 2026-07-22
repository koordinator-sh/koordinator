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

// Package gang implements the coscheduling (Gang) benchmark scenario.
//
// Design decisions taken here that should be confirmed with mentors
// (see Week 5 plan §3/§5) rather than treated as settled:
//   - cfg.PodCount is split into ceil(PodCount/GangSize) separate PodGroups
//     scheduled concurrently, rather than one PodGroup for the whole run.
//     This is the "many gangs competing for nodes at once" shape and is what
//     makes GangCompletionP50Sec/P99Sec meaningful as percentiles across groups.
//   - The PodGroup label key (types.PodGroupLabel) is
//     "pod-group.scheduling.sigs.k8s.io". NOT YET CONFIRMED against the
//     scheduler-plugins version vendored in this repo.
package gang

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// podGroupGVR identifies the upstream scheduler-plugins PodGroup CRD.
var podGroupGVR = schema.GroupVersionResource{
	Group:    "scheduling.sigs.k8s.io",
	Version:  "v1alpha1",
	Resource: "podgroups",
}

func init() {
	scenarios.Register(func() scenarios.Scenario { return &GangScenario{} })
}

// GangScenario benchmarks koord-scheduler's coscheduling plugin by creating
// ceil(podCount/gangSize) PodGroups and distributing cfg.PodCount pods
// evenly across them.
type GangScenario struct {
	namespace  string
	groupNames []string // set in Setup, reused by Pods and Teardown
}

func (s *GangScenario) Name() string { return "gang" }

// Setup creates the namespace then one PodGroup per gang of cfg.GangSize pods.
func (s *GangScenario) Setup(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	cfg types.ScenarioConfig,
	runID string,
) error {
	ns := cfg.Namespace
	if ns == "" {
		ns = "benchmark"
	}
	s.namespace = ns

	if _, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get namespace %q: %w", ns, err)
		}
		if _, createErr := client.CoreV1().Namespaces().Create(ctx,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
			metav1.CreateOptions{},
		); createErr != nil {
			return fmt.Errorf("failed to create namespace %q: %w", ns, createErr)
		}
	}

	gangSize := cfg.GangSize
	if gangSize <= 0 {
		gangSize = cfg.PodCount
	}
	minMember := cfg.MinMember
	if minMember <= 0 {
		minMember = gangSize
	}

	runIDPrefix := shortID(runID)
	numGroups := (cfg.PodCount + gangSize - 1) / gangSize
	s.groupNames = make([]string, 0, numGroups)

	for i := 0; i < numGroups; i++ {
		name := fmt.Sprintf("bench-gang-%s-%03d", runIDPrefix, i)
		s.groupNames = append(s.groupNames, name)

		pg := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "scheduling.sigs.k8s.io/v1alpha1",
			"kind":       "PodGroup",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": ns,
				"labels": map[string]interface{}{
					types.RunIDLabel: runID,
				},
			},
			"spec": map[string]interface{}{
				"minMember":              int64(minMember),
				"scheduleTimeoutSeconds": int64(300),
			},
		}}
		if _, err := dynClient.Resource(podGroupGVR).Namespace(ns).Create(
			ctx, pg, metav1.CreateOptions{},
		); err != nil {
			return fmt.Errorf("failed to create PodGroup %q: %w", name, err)
		}
	}
	return nil
}

// Pods returns cfg.PodCount pods split evenly across the PodGroups created
// in Setup. Each pod carries types.PodGroupLabel so both koord-scheduler's
// coscheduling plugin and framework.Watcher can associate it with its gang.
func (s *GangScenario) Pods(cfg types.ScenarioConfig, runID string) ([]*corev1.Pod, error) {
	ns := s.namespace
	if ns == "" {
		ns = "benchmark"
	}
	schedulerName := cfg.SchedulerName
	if schedulerName == "" {
		schedulerName = "koord-scheduler"
	}
	gangSize := cfg.GangSize
	if gangSize <= 0 {
		gangSize = cfg.PodCount
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

	runIDPrefix := shortID(runID)
	pods := make([]*corev1.Pod, 0, cfg.PodCount)
	for i := 0; i < cfg.PodCount; i++ {
		groupIdx := i / gangSize
		if groupIdx >= len(s.groupNames) {
			groupIdx = len(s.groupNames) - 1
		}
		groupName := s.groupNames[groupIdx]

		labels := map[string]string{
			types.RunIDLabel:    runID,
			types.PodGroupLabel: groupName,
			"app":               "kwok-bench-gang",
		}
		for k, v := range cfg.Labels {
			labels[k] = v
		}
		if cfg.QoSClass != "" {
			labels["koordinator.sh/qosClass"] = cfg.QoSClass
		}

		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("bench-gang-pod-%s-%04d", runIDPrefix, i),
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

// Teardown deletes all PodGroups and pods created for this run.
func (s *GangScenario) Teardown(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	runID string,
) error {
	ns := s.namespace
	if ns == "" {
		ns = "benchmark"
	}
	labelSel := fmt.Sprintf("%s=%s", types.RunIDLabel, runID)
	policy := metav1.DeletePropagationBackground

	if err := dynClient.Resource(podGroupGVR).Namespace(ns).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: labelSel},
	); err != nil {
		return fmt.Errorf("failed to delete PodGroups for run %q: %w", runID, err)
	}

	return client.CoreV1().Pods(ns).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: labelSel},
	)
}

func shortID(runID string) string {
	if len(runID) > 8 {
		return runID[:8]
	}
	return runID
}
