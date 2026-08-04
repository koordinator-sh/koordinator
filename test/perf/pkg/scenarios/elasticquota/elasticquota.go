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

// Package elasticquota implements the ElasticQuota benchmark scenario.
//
// Design:
//   - Runs in its own dedicated namespace (default "elasticquota-benchmark"),
//     separate from the "benchmark" namespace basic/gang use — this scenario's
//     whole point is to be quota-throttled, and must not share a parent quota
//     tree with scenarios that setup-cluster.sh deliberately made unthrottled.
//   - Setup creates exactly one ElasticQuota object per run, sized from
//     cfg.QuotaCPU/QuotaMemory. min is fixed at 0 so the configured max is
//     the only enforced bound.
//   - Pods deliberately request more in aggregate than the quota's max allows,
//     so some fraction are transiently Pending on "Insufficient quotas".
package elasticquota

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

var elasticQuotaGVR = schema.GroupVersionResource{
	Group:    "scheduling.sigs.k8s.io",
	Version:  "v1alpha1",
	Resource: "elasticquotas",
}

// quotaNameLabel is applied to every pod this scenario creates so the
// ElasticQuota plugin can associate the pod with the right quota object.
// If koordinator scopes purely by namespace this label is inert; if it
// scopes by label it is required. Confirm against
// pkg/scheduler/plugins/elasticquota before merging (see PR questions).
const quotaNameLabel = "quota.scheduling.sigs.k8s.io/name"

func init() {
	scenarios.Register(func() scenarios.Scenario { return &ElasticQuotaScenario{} })
}

// ElasticQuotaScenario benchmarks koord-scheduler's ElasticQuota plugin by
// creating a single tight quota object and bursting more pods than the quota
// allows, so some fraction are transiently quota-throttled.
type ElasticQuotaScenario struct {
	namespace string
	quotaName string
}

func (s *ElasticQuotaScenario) Name() string { return "elasticquota" }

// Setup creates a dedicated namespace and one ElasticQuota object sized from
// cfg.QuotaCPU/QuotaMemory. Returns an error when either quota field is unset.
func (s *ElasticQuotaScenario) Setup(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	cfg types.ScenarioConfig,
	runID string,
) error {
	ns := cfg.Namespace
	if ns == "" {
		ns = "elasticquota-benchmark"
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

	if cfg.QuotaCPU == "" || cfg.QuotaMemory == "" {
		return fmt.Errorf("elasticquota scenario requires both quotaCPU and quotaMemory in config")
	}
	if _, err := resource.ParseQuantity(cfg.QuotaCPU); err != nil {
		return fmt.Errorf("invalid quotaCPU %q: %w", cfg.QuotaCPU, err)
	}
	if _, err := resource.ParseQuantity(cfg.QuotaMemory); err != nil {
		return fmt.Errorf("invalid quotaMemory %q: %w", cfg.QuotaMemory, err)
	}

	quotaName := fmt.Sprintf("bench-elasticquota-%s", shortID(runID))
	s.quotaName = quotaName

	eq := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "scheduling.sigs.k8s.io/v1alpha1",
		"kind":       "ElasticQuota",
		"metadata": map[string]interface{}{
			"name":      quotaName,
			"namespace": ns,
			"labels": map[string]interface{}{
				types.RunIDLabel: runID,
			},
		},
		"spec": map[string]interface{}{
			"max": map[string]interface{}{
				"cpu":    cfg.QuotaCPU,
				"memory": cfg.QuotaMemory,
			},
			"min": map[string]interface{}{
				"cpu":    "0",
				"memory": "0",
			},
		},
	}}
	if _, err := dynClient.Resource(elasticQuotaGVR).Namespace(ns).Create(
		ctx, eq, metav1.CreateOptions{},
	); err != nil {
		return fmt.Errorf("failed to create ElasticQuota %q: %w", quotaName, err)
	}
	return nil
}

// Pods returns cfg.PodCount pods whose aggregate resource requests exceed the
// quota's max (the YAML's quotaCPU is sized so this is guaranteed — see
// configs/scenarios/elasticquota-1k.yaml).
func (s *ElasticQuotaScenario) Pods(cfg types.ScenarioConfig, runID string) ([]*corev1.Pod, error) {
	ns := s.namespace
	if ns == "" {
		ns = "elasticquota-benchmark"
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

	runIDPrefix := shortID(runID)
	pods := make([]*corev1.Pod, 0, cfg.PodCount)
	for i := 0; i < cfg.PodCount; i++ {
		labels := map[string]string{
			types.RunIDLabel: runID,
			quotaNameLabel:   s.quotaName,
			"app":            "kwok-bench-elasticquota",
		}
		for k, v := range cfg.Labels {
			labels[k] = v
		}
		if cfg.QoSClass != "" {
			labels["koordinator.sh/qosClass"] = cfg.QoSClass
		}

		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("bench-eq-pod-%s-%04d", runIDPrefix, i),
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

// Teardown deletes the ElasticQuota object and all pods for this run.
// The namespace is left in place (same pattern as gang.go) — kind cluster
// teardown removes it anyway.
func (s *ElasticQuotaScenario) Teardown(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	runID string,
) error {
	ns := s.namespace
	if ns == "" {
		ns = "elasticquota-benchmark"
	}
	labelSel := fmt.Sprintf("%s=%s", types.RunIDLabel, runID)
	policy := metav1.DeletePropagationBackground

	if err := dynClient.Resource(elasticQuotaGVR).Namespace(ns).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: labelSel},
	); err != nil {
		return fmt.Errorf("failed to delete ElasticQuota objects for run %q: %w", runID, err)
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
