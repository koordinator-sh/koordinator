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
//   - Runs in its own dedicated namespace (required — cfg.Namespace must be set
//     explicitly; there is no implicit default to avoid disagreeing with
//     engine.go's defaultNamespace used by Watcher/FailureWatcher). Separate
//     from the "benchmark" namespace basic/gang use so it can hold a tight quota
//     without throttling the other scenarios.
//   - Setup creates exactly one ElasticQuota object named after the namespace.
//     Naming the quota after the namespace means pod↔quota association works via
//     the namespace-name fallback in Plugin.GetQuotaName even when the label is
//     absent, and avoids a non-deterministic fallback if a previous Teardown
//     leaves a stale object in the same namespace. Setup is idempotent: if the
//     object already exists from a crashed prior run, it is updated in place.
//   - min is set equal to max. With EnableRuntimeQuota defaulting to true,
//     the enforced admission bound is the periodically-refreshed runtime
//     (derived from min + shared-weight distribution, capped by max). Setting
//     min == max pins runtime == max so the blocked-pod count is reproducible
//     across runs regardless of RefreshRuntime timing — but only while the sum
//     of every ElasticQuota's min in the cluster fits inside total node
//     allocatable; see validateQuotaFits in Setup, which fails loudly rather
//     than letting runtime silently drop below max.
package elasticquota

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

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// podAppLabelSelector is a run-independent selector that matches every pod
// created by any elasticquota benchmark run. Used by Teardown so it can clean
// up pods left by a different run (different run-id label) that crashed before
// its own Teardown completed.
const podAppLabelSelector = "app=kwok-bench-elasticquota"

var elasticQuotaGVR = schema.GroupVersionResource{
	Group:    "scheduling.sigs.k8s.io",
	Version:  "v1alpha1",
	Resource: "elasticquotas",
}

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

// Setup creates a dedicated namespace and one ElasticQuota object named after
// the namespace, sized from cfg.QuotaCPU/QuotaMemory with min == max.
// Returns an error when either quota field is unset or unparseable.
func (s *ElasticQuotaScenario) Setup(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	cfg types.ScenarioConfig,
	runID string,
) error {
	if cfg.Namespace == "" {
		return fmt.Errorf("elasticquota scenario requires namespace to be set explicitly " +
			"in config — there is no implicit default, to avoid disagreeing with " +
			"engine.go's defaultNamespace fallback used by Watcher/FailureWatcher")
	}
	ns := cfg.Namespace
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

	// Fail loudly if a previous run's Teardown did not complete and left pods
	// behind. Those pods carry extension.LabelQuotaName for this namespace,
	// so the plugin still associates them with this quota and kwok pause pods
	// never terminate — the quota's used is never released, and no pod from
	// the new run can be admitted. validateQuotaFits cannot catch this because
	// it only compares quota min against node allocatable, not actual pod usage.
	leftover, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: podAppLabelSelector})
	if err != nil {
		return fmt.Errorf("failed to check for leftover pods in namespace %q: %w", ns, err)
	}
	if len(leftover.Items) > 0 {
		return fmt.Errorf("namespace %q already contains %d elasticquota pod(s) left over from a "+
			"previous run (Teardown likely did not complete) — these hold quota a new run cannot "+
			"reclaim; delete them first: kubectl delete pods -n %s -l %s",
			ns, len(leftover.Items), ns, podAppLabelSelector)
	}

	if cfg.QuotaCPU == "" || cfg.QuotaMemory == "" {
		return fmt.Errorf("elasticquota scenario requires both quotaCPU and quotaMemory in config")
	}
	cpuQty, err := resource.ParseQuantity(cfg.QuotaCPU)
	if err != nil {
		return fmt.Errorf("invalid quotaCPU %q: %w", cfg.QuotaCPU, err)
	}
	memQty, err := resource.ParseQuantity(cfg.QuotaMemory)
	if err != nil {
		return fmt.Errorf("invalid quotaMemory %q: %w", cfg.QuotaMemory, err)
	}
	// Name the quota after the namespace so Plugin.GetQuotaName's
	// namespace-name fallback also associates pods with this quota.
	quotaName := ns
	s.quotaName = quotaName

	// Pass quotaName+ns so validateQuotaFits can skip the stale object on
	// retry — otherwise it would be counted twice (once via the initialised
	// reservedCPU/reservedMem, and again as an existing EQ in the cluster).
	if err := validateQuotaFits(ctx, client, dynClient, cpuQty, memQty, quotaName, ns); err != nil {
		return fmt.Errorf("quota-fit check failed: %w", err)
	}

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
			// min == max pins runtime == max (see package doc for why).
			"min": map[string]interface{}{
				"cpu":    cfg.QuotaCPU,
				"memory": cfg.QuotaMemory,
			},
		},
	}}
	if _, err := dynClient.Resource(elasticQuotaGVR).Namespace(ns).Create(
		ctx, eq, metav1.CreateOptions{},
	); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create ElasticQuota %q: %w", quotaName, err)
		}
		// A previous run's Teardown may not have run (process killed, cancelled
		// run). The quota name is fixed (== namespace), so update in place rather
		// than failing every subsequent run after a crash.
		existing, getErr := dynClient.Resource(elasticQuotaGVR).Namespace(ns).Get(ctx, quotaName, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("ElasticQuota %q already exists but could not be read for update: %w", quotaName, getErr)
		}
		eq.SetResourceVersion(existing.GetResourceVersion())
		if _, updateErr := dynClient.Resource(elasticQuotaGVR).Namespace(ns).Update(ctx, eq, metav1.UpdateOptions{}); updateErr != nil {
			return fmt.Errorf("failed to update existing ElasticQuota %q: %w", quotaName, updateErr)
		}
	}
	return nil
}

// Pods returns cfg.PodCount pods whose aggregate resource requests exceed the
// quota's max (the YAML's quotaCPU is sized so this is guaranteed — see
// configs/scenarios/elasticquota-1k.yaml).
func (s *ElasticQuotaScenario) Pods(cfg types.ScenarioConfig, runID string) ([]*corev1.Pod, error) {
	// Derive namespace and quota name directly from cfg so Pods() is a pure
	// function of its inputs and can be unit-tested without calling Setup first.
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
		// Apply cfg.Labels first so the built-in labels set below can never
		// be overwritten by a config-supplied value. A clobbered RunIDLabel
		// would make Watcher/FailureWatcher select nothing and hang the run.
		labels := make(map[string]string, len(cfg.Labels)+3)
		for k, v := range cfg.Labels {
			labels[k] = v
		}
		// extension.LabelQuotaName ("quota.scheduling.koordinator.sh/name") is
		// the key koord-scheduler's ElasticQuota plugin reads via
		// extension.GetQuotaName(pod). Using the imported constant (not a
		// hardcoded string) stays correct if the key ever moves.
		labels[extension.LabelQuotaName] = cfg.Namespace // quota is always named after the namespace
		labels[types.RunIDLabel] = runID
		labels["app"] = "kwok-bench-elasticquota"
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
	_ string, // runID — unused; pod deletion uses the run-independent app label (see below)
) error {
	ns := s.namespace
	policy := metav1.DeletePropagationBackground

	// Both deletes are best-effort and independent — a failure in one must not
	// skip the other, since either object left behind blocks every subsequent
	// run (the quota via a fixed-name collision, the pods by holding quota that
	// kwok pause pods never release).
	var errs []string

	// Delete the quota by name, not by label selector. The quota name is fixed
	// (== namespace), so a stale object left by a *previous* run carries a
	// different run-id label and DeleteCollection by label would miss it.
	if err := dynClient.Resource(elasticQuotaGVR).Namespace(ns).Delete(
		ctx, s.quotaName, metav1.DeleteOptions{PropagationPolicy: &policy},
	); err != nil && !errors.IsNotFound(err) {
		klog.ErrorS(err, "failed to delete ElasticQuota during Teardown — continuing to pod cleanup", "quota", s.quotaName)
		errs = append(errs, fmt.Sprintf("delete quota %q: %v", s.quotaName, err))
	}

	// Delete by the run-independent app label, not the run-id label. A crashed
	// prior run's pods carry a different run-id, so a per-run label selector
	// cannot see them — they hold quota that is never released, making every
	// subsequent run time out. Setup's leftover-pod check guards the same
	// invariant from the other direction.
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

// Augment implements scenarios.ResultAugmenter so the engine does not need to
// inspect elasticquota-specific config fields to populate QuotaBlockedPodCount.
func (s *ElasticQuotaScenario) Augment(stats types.FailureStats, result *types.BenchmarkResult) {
	n := stats.QuotaBlockedPodCount
	result.QuotaBlockedPodCount = &n
}

// validateQuotaFits is a best-effort check that this run's quota min, plus every
// other ElasticQuota's min already in the cluster, does not exceed total node
// allocatable. It does not fully replicate RuntimeQuotaCalculator.redistribution
// — it exists to fail loudly at Setup instead of silently producing a
// non-reproducible runtime when min > total allocatable (see package doc).
// Note: quotas whose spec.min.cpu or spec.min.memory cannot be parsed as a
// quantity are silently skipped — this is intentional for a best-effort check.
func validateQuotaFits(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	quotaCPU, quotaMemory resource.Quantity,
	skipName, skipNamespace string,
) error {
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes for quota-fit check: %w", err)
	}
	var totalCPU, totalMem resource.Quantity
	for _, n := range nodes.Items {
		if c, ok := n.Status.Allocatable[corev1.ResourceCPU]; ok {
			totalCPU.Add(c)
		}
		if m, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
			totalMem.Add(m)
		}
	}

	quotas, err := dynClient.Resource(elasticQuotaGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list existing ElasticQuota objects for quota-fit check: %w", err)
	}
	reservedCPU := quotaCPU.DeepCopy()
	reservedMem := quotaMemory.DeepCopy()
	for _, q := range quotas.Items {
		// Skip the quota this run is about to create/update: it is already
		// accounted for by the initial reservedCPU/reservedMem values above.
		// Without this check a retry (AlreadyExists path) double-counts the
		// stale object and may incorrectly reject an otherwise-valid config.
		if q.GetName() == skipName && q.GetNamespace() == skipNamespace {
			continue
		}
		if s, found, _ := unstructured.NestedString(q.Object, "spec", "min", "cpu"); found {
			if qty, parseErr := resource.ParseQuantity(s); parseErr == nil {
				reservedCPU.Add(qty)
			}
		}
		if s, found, _ := unstructured.NestedString(q.Object, "spec", "min", "memory"); found {
			if qty, parseErr := resource.ParseQuantity(s); parseErr == nil {
				reservedMem.Add(qty)
			}
		}
	}

	if reservedCPU.Cmp(totalCPU) > 0 {
		return fmt.Errorf("sum of all ElasticQuota min.cpu (%s, including this run's %s) exceeds "+
			"total node allocatable cpu (%s) — runtime will silently drop below max; "+
			"lower quotaCPU, raise nodeCount, or lower another quota's min",
			reservedCPU.String(), quotaCPU.String(), totalCPU.String())
	}
	if reservedMem.Cmp(totalMem) > 0 {
		return fmt.Errorf("sum of all ElasticQuota min.memory (%s, including this run's %s) exceeds "+
			"total node allocatable memory (%s) — runtime will silently drop below max; "+
			"lower quotaMemory, raise nodeCount, or lower another quota's min",
			reservedMem.String(), quotaMemory.String(), totalMem.String())
	}
	return nil
}
