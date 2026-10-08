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
//   - Nodes are sorted by name before the high/low split so the tier
//     classification is deterministic regardless of API server list ordering.
//   - Nodes are split into two tiers controlled by cfg.HighUtilNodeCount:
//     the first cfg.HighUtilNodeCount nodes (alphabetically lowest) are
//     "high-utilization" (seeded at cfg.HighUtilCPUPct% of each node's
//     allocatable CPU and memory, defaulting to 80%), and the remaining nodes
//     are "low-utilization" (fixed 10% of allocatable CPU and memory).
//     Both dimensions are seeded so the LoadAware plugin's combined score
//     (average of CPU and memory leastUsedScore) preserves the full tier gap.
//   - The LoadAware plugin reads NodeMetric.status.nodeMetric.nodeUsage.resources.cpu
//     and adjusts node scores so pods prefer low-utilization nodes (leastUsedScore).
//     Setting 70 of 100 nodes to 80% and 30 to 10% gives the plugin a clear
//     routing signal: most pods should land on the 30 low-utilization nodes.
//   - Single-pass NodeMetric seeding: for each node, Create and UpdateStatus are
//     called immediately adjacent to each other (no stale ResourceVersion gap).
//     Low-util nodes are processed BEFORE high-util nodes so the informer caches
//     their high scores (~90) before the high-util scores (~20) arrive — pods
//     started during the seeding window are routed correctly even if the informer
//     lags behind the last few events.
//   - NodeMetric is cluster-scoped and has a status subresource. Create() drops
//     the status field, so a separate UpdateStatus() call is required. The
//     status payload MUST include updateTime — isNodeMetricExpired() in the
//     LoadAware plugin returns true (causing Score to return 0) whenever
//     UpdateTime is nil. UpdateStatus failure logs a warning and continues —
//     the node is treated as having zero utilization, degrading signal only.
//   - Expiry ceiling: the scheduler-config sets nodeMetricExpirationSeconds=300.
//     Runs that take longer than 5 minutes will see metrics go stale and
//     LoadAware scoring degrade to no-op mid-run. At the current 1k-pod scale
//     (~38s) this is not a concern, but it becomes a ceiling if the scenario is
//     scaled to 10k pods. Static seeding with no re-stamp is deliberate for now
//     (dynamic periodic updates are a stretch goal per the execution plan).
//   - Post-seeding verification: after all patches, Setup polls the API server
//     until every NodeMetric for this run has a non-empty status.updateTime,
//     retrying patches for any that are missing. Once all are confirmed in
//     etcd, it sleeps 15 s for the scheduler's watch-based informer to process
//     the MODIFIED events before the pod burst begins.
//   - Augment (ResultAugmenter): the engine calls Augment before Teardown
//     (Teardown runs via defer after WriteReport). Pods are still live at
//     Augment time, so Augment lists them and counts those whose spec.nodeName
//     is in lowUtilNodes directly. client is stored on the struct during Setup.
//   - Teardown deletes NodeMetrics by run-id label and pods by the run-
//     independent app label. Both are best-effort and independent.
//   - NodeMetric is cluster-scoped: dynamic client calls use no namespace.
package loadaware

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
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
	lowUtilNodes map[string]bool      // populated in Setup, used in Augment
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

	// Sort by name so the high/low split is deterministic regardless of API
	// server list ordering, which is not guaranteed to be stable across runs.
	sort.Slice(nodes.Items, func(i, j int) bool {
		return nodes.Items[i].Name < nodes.Items[j].Name
	})

	highPct := cfg.HighUtilCPUPct
	if highPct == 0 && cfg.HighUtilNodeCount > 0 {
		highPct = 80 // 0 means "unset"; default to 80 only when high-util nodes are requested
	}

	s.lowUtilNodes = make(map[string]bool, len(nodes.Items))
	var setupErrs []string

	// Single-pass NodeMetric seeding: for each node, create the object and
	// immediately UpdateStatus in the same iteration so the informer processes
	// create + status as adjacent events (no stale ResourceVersion gap).
	//
	// Low-util nodes are processed FIRST (indices HighUtilNodeCount..N-1, then
	// 0..HighUtilNodeCount-1). The scheduler's NodeMetric informer processes
	// events in arrival order; nodes whose NodeMetric is not yet in the cache
	// score 0. By flushing low-util status (score ~90) before high-util status
	// (score ~20), the low-util nodes are cached first and attract pods even
	// if the informer lags behind the last few high-util updates.
	//
	// Both CPU and memory are seeded at the same utilization percentage so the
	// LoadAware plugin's combined score (average of CPU and memory leastUsedScore)
	// reflects the intended tier split. Seeding only CPU while leaving memory
	// identical across tiers halves the score gap, moving the crossover point
	// (where accumulating NRF scores overtake LoadAware) inside the 1 000-pod run.
	type nodePass struct {
		node     corev1.Node
		isLow    bool
		cpuMilli int64
		memBytes int64
	}
	passes := make([]nodePass, 0, len(nodes.Items))
	// First collect low-util nodes (indices >= HighUtilNodeCount).
	for i, n := range nodes.Items {
		if i < cfg.HighUtilNodeCount {
			continue
		}
		passes = append(passes, nodePass{
			node:     n,
			isLow:    true,
			cpuMilli: n.Status.Allocatable.Cpu().MilliValue() * 10 / 100,
			memBytes: n.Status.Allocatable.Memory().Value() * 10 / 100,
		})
	}
	// Then collect high-util nodes (indices < HighUtilNodeCount).
	for i, n := range nodes.Items {
		if i >= cfg.HighUtilNodeCount {
			continue
		}
		passes = append(passes, nodePass{
			node:     n,
			isLow:    false,
			cpuMilli: n.Status.Allocatable.Cpu().MilliValue() * int64(highPct) / 100,
			memBytes: n.Status.Allocatable.Memory().Value() * int64(highPct) / 100,
		})
	}

	for _, p := range passes {
		if p.isLow {
			s.lowUtilNodes[p.node.Name] = true
		}
		if _, createErr := createNodeMetric(ctx, dynClient, p.node.Name, runID); createErr != nil {
			setupErrs = append(setupErrs, createErr.Error())
			continue
		}
		if _, statusErr := patchNodeMetricStatus(ctx, dynClient, p.node.Name, p.cpuMilli, p.memBytes); statusErr != nil {
			klog.ErrorS(statusErr, "failed to patch NodeMetric status — node will score as zero utilization",
				"node", p.node.Name)
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

	// Build node→seeding map for status-patch retries below.
	type nodeSeeding struct {
		cpuMilli int64
		memBytes int64
	}
	nodeUsages := make(map[string]nodeSeeding, len(passes))
	for _, p := range passes {
		nodeUsages[p.node.Name] = nodeSeeding{p.cpuMilli, p.memBytes}
	}

	// Wait until every NodeMetric for this run has status.updateTime set in the
	// API server.  The patchNodeMetricStatus call is best-effort in the loop
	// above; if a patch failed silently (transient error, object recreated by
	// another controller, etc.) the metric would have nil UpdateTime, causing
	// isNodeMetricExpired to return true and Score to return 0 for that node.
	// Retrying here guarantees the data reaches etcd before we hand off to the
	// pod burst, at which point the scheduler's watch-based informer will have
	// already (or very shortly) reflected the change.
	klog.InfoS("Verifying NodeMetric statuses in API server (with retry)")
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		nms, listErr := dynClient.Resource(nodeMetricGVR).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", types.RunIDLabel, runID),
		})
		if listErr != nil {
			klog.ErrorS(listErr, "NodeMetric status verification: list failed")
			time.Sleep(2 * time.Second)
			continue
		}
		notReady := 0
		for i := range nms.Items {
			updateTime, _, _ := unstructured.NestedString(nms.Items[i].Object, "status", "updateTime")
			if updateTime != "" {
				continue
			}
			notReady++
			name := nms.Items[i].GetName()
			if seed, ok := nodeUsages[name]; ok {
				if _, retryErr := patchNodeMetricStatus(ctx, dynClient, name, seed.cpuMilli, seed.memBytes); retryErr != nil {
					klog.ErrorS(retryErr, "NodeMetric status retry patch failed", "node", name)
				}
			}
		}
		if notReady == 0 {
			klog.InfoS("All NodeMetric statuses confirmed in API server", "count", len(nms.Items))
			break
		}
		klog.InfoS("NodeMetric statuses not yet ready, retrying patches",
			"notReady", notReady, "total", len(nms.Items))
		time.Sleep(2 * time.Second)
	}
	if time.Now().After(deadline) {
		klog.Warningf("NodeMetric status verification deadline exceeded; some nodes may score as zero utilization")
	}

	// Extra sleep for the scheduler's NodeMetric informer to process all
	// MODIFIED watch events.  The status is now confirmed in etcd; watch
	// events propagate within milliseconds, but a 15 s buffer handles loaded
	// CI runners where the scheduler goroutine may be delayed.
	// Skip the sleep when the context is already cancelled (e.g. unit tests)
	// so that test packages don't each pay an unnecessary 15 s penalty.
	if ctx.Err() == nil {
		klog.InfoS("Waiting for informer propagation before pod burst")
		time.Sleep(15 * time.Second)
	}
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

// buildNodeMetricStatus returns the status payload for a NodeMetric object.
//
// updateTime is stamped with the current UTC time — isNodeMetricExpired() in
// pkg/scheduler/plugins/loadaware/helper.go returns true (and Score short-
// circuits to 0) whenever Status.UpdateTime is nil, making the scenario a
// no-op without it.
//
// Both cpu and memory are required to give the LoadAware plugin a full signal.
// The plugin averages leastUsedScore across all resourceWeights dimensions
// (cpu and memory, each weight=1 in the default config); seeding only CPU while
// leaving memory identical across tiers halves the effective score gap between
// tiers and moves the NRF crossover point inside the 1 000-pod run.
//
// The JSON paths read by the LoadAware plugin:
//
//	status.nodeMetric.nodeUsage.resources.cpu
//	status.nodeMetric.nodeUsage.resources.memory
//
// where "resources" is the JSON tag for ResourceMap.ResourceList (not
// "resourceList" — see apis/slo/v1alpha1/resources.go).
func buildNodeMetricStatus(cpuUsageMilli, memUsageBytes int64) map[string]interface{} {
	cpuQty := resource.NewMilliQuantity(cpuUsageMilli, resource.DecimalSI)
	memQty := resource.NewQuantity(memUsageBytes, resource.BinarySI)
	return map[string]interface{}{
		// updateTime is required — isNodeMetricExpired returns true when nil,
		// causing Score to short-circuit and return 0 for every node.
		"updateTime": time.Now().UTC().Format(time.RFC3339),
		"nodeMetric": map[string]interface{}{
			"nodeUsage": map[string]interface{}{
				// "resources" is the JSON tag for ResourceMap.ResourceList —
				// apis/slo/v1alpha1/resources.go: `corev1.ResourceList \`json:"resources,omitempty"\``
				"resources": map[string]interface{}{
					"cpu":    cpuQty.String(),
					"memory": memQty.String(),
				},
			},
		},
	}
}

// patchNodeMetricStatus writes the simulated CPU usage into the NodeMetric's
// status subresource using a merge patch. Unlike UpdateStatus, a merge patch
// does NOT require a matching ResourceVersion, so it succeeds even when
// koord-manager's NodeMetricReconciler has concurrently bumped the object
// version (which would cause a 409 Conflict from UpdateStatus).
func patchNodeMetricStatus(
	ctx context.Context,
	dynClient dynamic.Interface,
	nodeName string,
	cpuUsageMilli, memUsageBytes int64,
) (*unstructured.Unstructured, error) {
	status := buildNodeMetricStatus(cpuUsageMilli, memUsageBytes)
	patch := map[string]interface{}{"status": status}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal NodeMetric status patch for %q: %w", nodeName, err)
	}
	return dynClient.Resource(nodeMetricGVR).Patch(ctx, nodeName, k8stypes.MergePatchType, patchBytes, metav1.PatchOptions{}, "status")
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
