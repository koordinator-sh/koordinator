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

// Package reservation implements the Reservation benchmark scenario.
//
// Design:
//   - Setup creates N = cfg.ReservationCount Reservation objects
//     (scheduling.koordinator.sh/v1alpha1), one per pod that will bind to it,
//     sized to cfg.ResourceRequests so capacity exactly covers one pod.
//     Reservation names: bench-rsv-<shortID(runID)>-<index> — per-run prefix
//     keeps them distinct across runs without a fixed-name collision hazard.
//   - Owner selector: each Reservation's spec.owners matches on a per-reservation
//     label (benchmark.koordinator.sh/reservation-index: "<index>") so each pod
//     binds to exactly one Reservation. The RunIDLabel is NOT added to the owner
//     selector — a pod only carries ONE reservation-index label and that alone
//     uniquely identifies its Reservation within this run.
//   - The first cfg.ReservationCount pods carry reservation-index labels and bind
//     to a Reservation. The remaining cfg.PodCount - cfg.ReservationCount pods
//     carry no such label and schedule normally against raw node capacity.
//   - Leftover guard: if any Reservation with the run-independent app label
//     (app=kwok-bench-reservation) already exists from a crashed prior run,
//     Setup fails loudly with a cleanup instruction. The guard uses the
//     cluster-scoped API (no namespace) since Reservation is cluster-scoped.
//   - Augment (ResultAugmenter): the engine calls Augment before Teardown
//     (Teardown runs via defer after WriteReport). Reservations are still live
//     at Augment time, so Augment lists them and counts status.phase == "Succeeded"
//     directly. dynClient and runID are stored on the struct during Setup.
//   - Teardown deletes all Reservations by run-id label, then all pods by
//     run-independent app label. Both are best-effort and independent.
//   - Reservation is cluster-scoped: all dynamic client calls use no namespace.
package reservation

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

const (
	// podAppLabelSelector is the run-independent selector for all pods created
	// by this scenario, used in Teardown to clean up across run boundaries.
	podAppLabelSelector = "app=kwok-bench-reservation"

	// reservationIndexLabel is the per-pod label key whose value identifies which
	// Reservation that pod is the owner of. Set on the first ReservationCount pods.
	reservationIndexLabel = "benchmark.koordinator.sh/reservation-index"

	// reservationAppLabel is a run-independent label added to every Reservation
	// object. Used by the leftover guard to detect stale objects from crashed runs.
	reservationAppLabel = "app=kwok-bench-reservation"
)

// reservationGVR identifies the koordinator Reservation CRD.
// Reservation is cluster-scoped (scheduling.koordinator.sh/v1alpha1).
var reservationGVR = schema.GroupVersionResource{
	Group:    "scheduling.koordinator.sh",
	Version:  "v1alpha1",
	Resource: "reservations",
}

func init() {
	scenarios.Register(func() scenarios.Scenario { return &ReservationScenario{} })
}

// ReservationScenario benchmarks koord-scheduler's Reservation plugin by
// pre-creating N Reservation objects and binding the first N pods to them via
// spec.owners label-selector matching. The remaining pods schedule normally,
// so the metric captures "scheduling with Reservations present" overhead.
type ReservationScenario struct {
	namespace string
	dynClient dynamic.Interface // stored in Setup, used by Augment
	runID     string            // stored in Setup, used by Augment
}

func (s *ReservationScenario) Name() string { return "reservation" }

// Setup creates the dedicated namespace and cfg.ReservationCount Reservation
// objects, each sized to accept exactly one pod.
func (s *ReservationScenario) Setup(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	cfg types.ScenarioConfig,
	runID string,
) error {
	if cfg.Namespace == "" {
		return fmt.Errorf("reservation scenario requires namespace to be set explicitly in config " +
			"(recommended: reservation-benchmark) to avoid disagreeing with engine.go's defaultNamespace")
	}
	ns := cfg.Namespace
	s.namespace = ns
	s.dynClient = dynClient
	s.runID = runID

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

	// Leftover guard: Reservation is cluster-scoped, so we use a run-independent
	// app label to detect stale objects from a prior crashed run. Without this,
	// the stale Reservations would match this run's pods (same reservationIndexLabel
	// values) or exhaust node capacity before the new run's Reservations are placed.
	leftover, err := dynClient.Resource(reservationGVR).List(ctx, metav1.ListOptions{
		LabelSelector: reservationAppLabel,
	})
	if err != nil {
		return fmt.Errorf("failed to check for leftover Reservations: %w", err)
	}
	if len(leftover.Items) > 0 {
		return fmt.Errorf(
			"found %d Reservation(s) left over from a previous run "+
				"(Teardown likely did not complete) — delete them first: "+
				"kubectl delete reservations -l %s",
			len(leftover.Items), reservationAppLabel)
	}

	if cfg.ReservationCount <= 0 {
		return nil // no Reservations to pre-create; all pods schedule normally
	}

	var resources map[string]interface{}
	if len(cfg.ResourceRequests) > 0 {
		rl := map[string]interface{}{}
		for k, v := range cfg.ResourceRequests {
			rl[k] = v
		}
		resources = map[string]interface{}{"requests": rl, "limits": rl}
	}

	shortID := types.ShortID(runID)
	for i := 0; i < cfg.ReservationCount; i++ {
		name := fmt.Sprintf("bench-rsv-%s-%04d", shortID, i)
		rsv := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "scheduling.koordinator.sh/v1alpha1",
			"kind":       "Reservation",
			"metadata": map[string]interface{}{
				"name": name,
				"labels": map[string]interface{}{
					types.RunIDLabel: runID,
					"app":            "kwok-bench-reservation",
				},
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":      "pause",
								"image":     "registry.k8s.io/pause:3.9",
								"resources": resources,
							},
						},
						// Scope Reservation scheduling to kwok-simulated nodes so
						// the Reservation itself lands where pods will land.
						"nodeSelector": map[string]interface{}{"type": "kwok"},
						"tolerations": []interface{}{
							map[string]interface{}{
								"key":      "kwok.x-k8s.io/node",
								"operator": "Exists",
								"effect":   "NoSchedule",
							},
						},
					},
				},
				// Owner: pods whose reservation-index label matches this index.
				// One Reservation per pod — when the pod is scheduled the
				// Reservation transitions to Succeeded (phase gate for Augment).
				"owners": []interface{}{
					map[string]interface{}{
						"labelSelector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								reservationIndexLabel: fmt.Sprintf("%d", i),
							},
						},
					},
				},
			},
		}}
		if _, createErr := dynClient.Resource(reservationGVR).Create(ctx, rsv, metav1.CreateOptions{}); createErr != nil {
			return fmt.Errorf("failed to create Reservation %q: %w", name, createErr)
		}
	}
	return nil
}

// Pods returns cfg.PodCount pods. The first cfg.ReservationCount carry the
// reservation-index label so they bind to a pre-created Reservation; the rest
// schedule normally against raw node capacity.
func (s *ReservationScenario) Pods(cfg types.ScenarioConfig, runID string) ([]*corev1.Pod, error) {
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
		// A clobbered RunIDLabel would make Watcher/FailureWatcher select nothing.
		labels := make(map[string]string, len(cfg.Labels)+3)
		for k, v := range cfg.Labels {
			labels[k] = v
		}
		labels[types.RunIDLabel] = runID
		labels["app"] = "kwok-bench-reservation"
		if cfg.QoSClass != "" {
			labels["koordinator.sh/qosClass"] = cfg.QoSClass
		}
		// Only the first ReservationCount pods carry the binding label.
		if i < cfg.ReservationCount {
			labels[reservationIndexLabel] = fmt.Sprintf("%d", i)
		}

		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("bench-rsv-pod-%s-%04d", runIDPrefix, i),
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

// Teardown deletes all Reservations by run-id label and all pods by the
// run-independent app label. Both deletes are best-effort and independent —
// a failure in one must not skip the other.
//
// Note: ReservationBindCount is counted in Augment (not here) because the
// engine calls Augment before Teardown (defer runs after WriteReport).
func (s *ReservationScenario) Teardown(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
	runID string,
) error {
	ns := s.namespace
	labelSel := fmt.Sprintf("%s=%s", types.RunIDLabel, runID)
	policy := metav1.DeletePropagationBackground
	var errs []string

	if err := dynClient.Resource(reservationGVR).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: labelSel},
	); err != nil {
		klog.ErrorS(err, "failed to delete Reservations during Teardown")
		errs = append(errs, fmt.Sprintf("delete reservations: %v", err))
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

// Augment implements scenarios.ResultAugmenter, populating ReservationBindCount.
// Called by the engine before Teardown (Teardown runs via defer after WriteReport),
// so Reservations are still present in the cluster — list and count here.
func (s *ReservationScenario) Augment(stats types.FailureStats, result *types.BenchmarkResult) {
	n := 0
	if s.dynClient != nil && s.runID != "" {
		labelSel := fmt.Sprintf("%s=%s", types.RunIDLabel, s.runID)
		rsvList, err := s.dynClient.Resource(reservationGVR).List(
			context.Background(),
			metav1.ListOptions{LabelSelector: labelSel},
		)
		if err == nil {
			for i := range rsvList.Items {
				phase, _, _ := unstructured.NestedString(rsvList.Items[i].Object, "status", "phase")
				if phase == "Succeeded" {
					n++
				}
			}
		} else {
			klog.ErrorS(err, "Augment: failed to list Reservations for bind-count")
		}
	}
	result.ReservationBindCount = &n
}
