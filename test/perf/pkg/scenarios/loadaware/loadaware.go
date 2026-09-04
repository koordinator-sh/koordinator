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

// Package loadaware implements the LoadAware benchmark scenario.
//
// Design:
//   - Setup lists the benchmark nodes (already created by the engine before
//     Setup is called — engine.go order: CreateNodes → WaitReady → Setup) and
//     creates one NodeMetric object (slo.koordinator.sh/v1alpha1) per node.
//   - Nodes are split into two tiers controlled by cfg.HighUtilNodeCount:
//     the first cfg.HighUtilNodeCount nodes are "high-utilization" (seeded at
//     cfg.HighUtilCPUPct% of each node's allocatable CPU, defaulting to 80%),
//     and the remaining nodes are "low-utilization" (fixed 10% of allocatable).
//   - The LoadAware plugin reads NodeMetric.status.nodeMetric.nodeUsage.resources.cpu
//     and adjusts node scores so pods prefer low-utilization nodes. Setting 70 of
//     100 nodes to 80% and 30 to 10% gives the plugin a clear routing signal:
//     most pods should land on the 30 low-utilization nodes.
//   - NodeMetric is cluster-scoped and has a status subresource. Create() drops
//     the status field, so a separate UpdateStatus() call is required. If
//     UpdateStatus fails, Setup logs a warning and continues — the node is then
//     treated as having zero utilization, which degrades signal but does not
//     abort the run.
//   - Augment (ResultAugmenter): the engine calls Augment before Teardown
//     (Teardown runs via defer after WriteReport). Pods are still live at
//     Augment time, so Augment lists them and counts those whose spec.nodeName
//     is in lowUtilNodes directly. client is stored on the struct during Setup.
//   - Teardown deletes NodeMetrics by run-id label and pods by the run-
//     independent app label. Both are best-effort and independent.
//   - NodeMetric is cluster-scoped: dynamic client calls use no namespace.
//
// Static seeding only: dynamic periodic NodeMetric updates are a Phase 3
// stretch goal per the execution plan and mentor confirmation.
package loadaware

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// podAppLabelSelector is the run-independent selector for all pods created
// by this scenario, used in Teardown to clean up across run boundaries.
const podAppLabelSelector = "app=kwok-bench-loadaware"

// nodeMetricGVR identifies the koordinator NodeMetric CRD written by
// koord-manager and read by the LoadAware scheduler plugin.
// NodeMetric is cluster-scoped (slo.koordinator.sh/v1alpha1).
var nodeMetricGVR = schema.GroupVersionResource{
	Group:    "slo.koordinator.sh",
	Version:  "v1alpha1",
	Resource: "nodemetrics",
}

func init() {
	scenarios.Register(func() scenarios.Scenario { return &LoadAwareScenario{} })
}

// LoadAwareScenario benchmarks koord-scheduler's LoadAwareScheduling plugin by
// statically seeding one NodeMetric per node: cfg.HighUtilNodeCount nodes get a
// high CPU utilization value, the rest get a low value, so the plugin has a
// clear signal to route pods toward the low-utilization nodes.
type LoadAwareScenario struct {
	namespace    string
	lowUtilNodes map[string]bool     // populated in Setup, used in Augment
	client       kubernetes.Interface // stored in Setup, used by Augment
}

func (s *LoadAwareScenario) Name() string { return "loadaware" }

// Setup creates the dedicated namespace, then one NodeMetric per kwok node.
// The first cfg.HighUtilNodeCount nodes are seeded at cfg.HighUtilCPUPct% of
// their allocatable CPU; the remaining nodes are seeded at 10%.
func (s *LoadAwareScenario) Setup(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	cfg types.ScenarioConfig,
	runID string,
) error {
	if cfg.Namespace == "" {
		return fmt.Errorf("loadaware scenario requires namespace to be set explicitly in config " +
			"(recommended: loadaware-benchmark) to avoid disagreeing with engine.go's defaultNamespace")
	}
	ns := cfg.Namespace
	s.namespace = ns
	s.client = client

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

	// List only the nodes for this run by run-id label.
	// Nodes exist by the time Setup runs — engine.go creates and waits for them first.
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", types.RunIDLabel, runID),
	})
	if err != nil {
		return fmt.Errorf("failed to list nodes for NodeMetric seeding: %w", err)
	}
	if len(nodes.Items) == 0 {
		return fmt.Errorf("no nodes found with label %s=%s — Setup must run after CreateNodes", types.RunIDLabel, runID)
	}

	highPct := cfg.HighUtilCPUPct
	if highPct == 0 {
		highPct = 80 // default when HighUtilNodeCount > 0 and HighUtilCPUPct is unset
	}

	s.lowUtilNodes = make(map[string]bool, len(nodes.Items))
	var setupErrs []string

	for i, n := range nodes.Items {
		cpuAllocatable := n.Status.Allocatable.Cpu()

		var cpuUsageMilli int64
		isHigh := i < cfg.HighUtilNodeCount
		if isHigh {
			cpuUsageMilli = cpuAllocatable.MilliValue() * int64(highPct) / 100
		} else {
			cpuUsageMilli = cpuAllocatable.MilliValue() * 10 / 100 // fixed 10%
			s.lowUtilNodes[n.Name] = true
		}

		created, createErr := createNodeMetric(ctx, dynClient, n.Name, runID)
		if createErr != nil {
			setupErrs = append(setupErrs, createErr.Error())
			continue
		}

		if _, statusErr := setNodeMetricStatus(ctx, dynClient, created, cpuUsageMilli); statusErr != nil {
			// Log and continue: a missing NodeMetric status means the LoadAware plugin
			// treats the node as zero utilization (no meaningful score difference from
			// other unset nodes), degrading signal without aborting the run.
			klog.ErrorS(statusErr, "failed to set NodeMetric status — node will score as zero utilization",
				"node", n.Name)
		}
	}

	if len(setupErrs) > 0 {
		return fmt.Errorf("setup had %d NodeMetric create error(s): %s", len(setupErrs), strings.Join(setupErrs, "; "))
	}
	klog.InfoS("LoadAware setup complete",
		"totalNodes", len(nodes.Items),
		"highUtilNodes", cfg.HighUtilNodeCount,
		"lowUtilNodes", len(nodes.Items)-cfg.HighUtilNodeCount,
		"highUtilCPUPct", highPct,
	)
	return nil
}

// createNodeMetric creates the NodeMetric object (spec/metadata only).
// Name MUST match the node name exactly — the LoadAware plugin looks up
// NodeMetric by node name, not by label selector.
func createNodeMetric(
	ctx context.Context,
	dynClient dynamic.Interface,
	nodeName, runID string,
) (*unstructured.Unstructured, error) {
	nm := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "slo.koordinator.sh/v1alpha1",
		"kind":       "NodeMetric",
		"metadata": map[string]interface{}{
			"name": nodeName,
			"labels": map[string]interface{}{
				types.RunIDLabel: runID,
			},
		},
		"spec": map[string]interface{}{},
	}}

	existing, err := dynClient.Resource(nodeMetricGVR).Get(ctx, nodeName, metav1.GetOptions{})
	if err == nil {
		// Already exists (crashed prior run). Update in place with new run-id label.
		nm.SetResourceVersion(existing.GetResourceVersion())
		updated, updateErr := dynClient.Resource(nodeMetricGVR).Update(ctx, nm, metav1.UpdateOptions{})
		if updateErr != nil {
			return nil, fmt.Errorf("failed to update existing NodeMetric %q: %w", nodeName, updateErr)
		}
		return updated, nil
	}
	if !errors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get NodeMetric %q: %w", nodeName, err)
	}
	created, createErr := dynClient.Resource(nodeMetricGVR).Create(ctx, nm, metav1.CreateOptions{})
	if createErr != nil {
		return nil, fmt.Errorf("failed to create NodeMetric %q: %w", nodeName, createErr)
	}
	return created, nil
}

// setNodeMetricStatus writes the simulated CPU usage into the NodeMetric's
// status subresource. NodeMetric.status is a subresource — Create() drops the
// status field, so an explicit UpdateStatus() is required.
//
// The JSON path for the LoadAware plugin to read:
//
//	status.nodeMetric.nodeUsage.resources.cpu
//
// where "resources" is the JSON tag for ResourceMap.ResourceList (not
// "resourceList" — see apis/slo/v1alpha1/resources.go).
func setNodeMetricStatus(
	ctx context.Context,
	dynClient dynamic.Interface,
	nm *unstructured.Unstructured,
	cpuUsageMilli int64,
) (*unstructured.Unstructured, error) {
	cpuQty := resource.NewMilliQuantity(cpuUsageMilli, resource.DecimalSI)
	status := map[string]interface{}{
		"nodeMetric": map[string]interface{}{
			"nodeUsage": map[string]interface{}{
				// "resources" is the JSON tag for ResourceMap.ResourceList —
				// apis/slo/v1alpha1/resources.go: `corev1.ResourceList \`json:"resources,omitempty"\``
				"resources": map[string]interface{}{
					"cpu":    cpuQty.String(),
					"memory": "10Gi",
				},
			},
		},
	}
	if err := unstructured.SetNestedField(nm.Object, status, "status"); err != nil {
		return nil, fmt.Errorf("failed to build NodeMetric status for %q: %w", nm.GetName(), err)
	}
	return dynClient.Resource(nodeMetricGVR).UpdateStatus(ctx, nm, metav1.UpdateOptions{})
}

// Pods returns cfg.PodCount plain pods — no reservation or quota labels needed
// since the LoadAware plugin operates at the node-scoring level, transparently
// to the pod spec.
func (s *LoadAwareScenario) Pods(cfg types.ScenarioConfig, runID string) ([]*corev1.Pod, error) {
	ns := cfg.Namespace
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

	runIDPrefix := types.ShortID(runID)
	pods := make([]*corev1.Pod, 0, cfg.PodCount)
	for i := 0; i < cfg.PodCount; i++ {
		// Apply cfg.Labels first so built-in labels below cannot be overwritten.
		labels := make(map[string]string, len(cfg.Labels)+2)
		for k, v := range cfg.Labels {
			labels[k] = v
		}
		labels[types.RunIDLabel] = runID
		labels["app"] = "kwok-bench-loadaware"
		if cfg.QoSClass != "" {
			labels["koordinator.sh/qosClass"] = cfg.QoSClass
		}

		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("bench-la-pod-%s-%04d", runIDPrefix, i),
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

// Teardown deletes NodeMetrics by run-id label and pods by the run-independent
// app label. Both deletes are best-effort and independent.
//
// Note: LoadAwareRoutedPodCount is counted in Augment (not here) because the
// engine calls Augment before Teardown (defer runs after WriteReport).
func (s *LoadAwareScenario) Teardown(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	runID string,
) error {
	ns := s.namespace
	labelSel := fmt.Sprintf("%s=%s", types.RunIDLabel, runID)
	policy := metav1.DeletePropagationBackground
	var errs []string

	if err := dynClient.Resource(nodeMetricGVR).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: labelSel},
	); err != nil {
		klog.ErrorS(err, "failed to delete NodeMetric objects during Teardown")
		errs = append(errs, fmt.Sprintf("delete nodemetrics: %v", err))
	}

	if err := client.CoreV1().Pods(ns).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: podAppLabelSelector},
	); err != nil {
		klog.ErrorS(err, "failed to delete pods during Teardown", "namespace", ns)
		errs = append(errs, fmt.Sprintf("delete pods in %q: %v", ns, err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("teardown had %d error(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// Augment implements scenarios.ResultAugmenter, populating LoadAwareRoutedPodCount.
// Called by the engine before Teardown (Teardown runs via defer after WriteReport),
// so pods are still present in the cluster — list and count here.
func (s *LoadAwareScenario) Augment(stats types.FailureStats, result *types.BenchmarkResult) {
	n := 0
	if s.client != nil && len(s.lowUtilNodes) > 0 {
		podList, err := s.client.CoreV1().Pods(s.namespace).List(
			context.Background(),
			metav1.ListOptions{LabelSelector: podAppLabelSelector},
		)
		if err == nil {
			for i := range podList.Items {
				if s.lowUtilNodes[podList.Items[i].Spec.NodeName] {
					n++
				}
			}
		} else {
			klog.ErrorS(err, "Augment: failed to list pods for routed-count")
		}
	}
	result.LoadAwareRoutedPodCount = &n
}
