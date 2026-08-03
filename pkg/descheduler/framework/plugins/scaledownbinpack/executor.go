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

package scaledownbinpack

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
)

const (
	podDeletionCostAnnotation = "controller.kubernetes.io/pod-deletion-cost"
)

func (pl *ScaleDownBinPack) patchDeletionCosts(ctx context.Context, ranked []RankedPod, skipped []*corev1.Pod) *framework.Status {
	if pl.handle.IsDryRun() {
		klog.V(4).InfoS("DryRun: would patch deletion costs", "rankedPods", len(ranked), "skippedPods", len(skipped))
		return nil
	}

	var errs []error
	client := pl.handle.ClientSet()

	// Patch eligible ranked pods with their computed rank.
	for _, rp := range ranked {
		costStr := fmt.Sprintf("%d", rp.Rank)
		if err := pl.patchPodAnnotation(ctx, client, rp.Pod, podDeletionCostAnnotation, costStr); err != nil {
			errs = append(errs, err)
			klog.ErrorS(err, "Failed to patch pod deletion cost for ranked pod", "pod", klog.KObj(rp.Pod))
		}
	}

	// Skipped target pods (matched by selector but blocked by constraints) should ideally
	// not be scaled down. We can assign them the maximum integer cost to ensure native
	// controllers delete them last if scale-down is forced.
	for _, pod := range skipped {
		costStr := fmt.Sprintf("%d", math.MaxInt32)
		if err := pl.patchPodAnnotation(ctx, client, pod, podDeletionCostAnnotation, costStr); err != nil {
			errs = append(errs, err)
			klog.ErrorS(err, "Failed to patch pod deletion cost for skipped pod", "pod", klog.KObj(pod))
		}
	}

	if len(errs) > 0 {
		return &framework.Status{Err: fmt.Errorf("failed to patch %d pods", len(errs))}
	}
	return nil
}

func (pl *ScaleDownBinPack) evictRankedPods(ctx context.Context, ranked []RankedPod) *framework.Status {
	if pl.handle.IsDryRun() {
		klog.V(4).InfoS("DryRun: would evict ranked pods", "rankedPods", len(ranked))
		return nil
	}

	evictedCount := 0
	var maxPods int
	if pl.args.MaxPodsToEvict != nil && *pl.args.MaxPodsToEvict > 0 {
		maxPods = int(*pl.args.MaxPodsToEvict)
	}

	for _, rp := range ranked {
		if maxPods > 0 && evictedCount >= maxPods {
			klog.V(4).InfoS("Reached maximum number of pods to evict", "maxPods", maxPods)
			break
		}

		success := pl.handle.Evictor().Evict(ctx, rp.Pod, framework.EvictOptions{
			Reason: "scale-down binpack",
		})
		if success {
			evictedCount++
		}
	}

	return nil
}

func (pl *ScaleDownBinPack) patchPodAnnotation(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod, key, value string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				key: value,
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	return err
}
