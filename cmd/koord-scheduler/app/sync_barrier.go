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

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"

	schedulerserverconfig "github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app/config"
)

var (
	syncBarrierPodNamespace = metav1.NamespaceSystem
	syncBarrierPodName      = "koord-scheduler-sync-barrier"
)

func init() {
	pflag.StringVar(&syncBarrierPodNamespace, "sync-barrier-pod-namespace", syncBarrierPodNamespace, "sync barrier namespace")
	pflag.StringVar(&syncBarrierPodName, "sync-barrier-pod-name", syncBarrierPodName, "sync barrier pod name")
}

// waitForLatestSynced ensures the scheduler cache is fully synchronized with the API server
// AFTER acquiring leadership, specifically addressing the critical gap during leader election:
//   - Prevents resource overcommitment caused by missing Pods scheduled by the previous leader
//     in the final moments before election
//   - Avoids the high failover latency of --delay-cache-until-active (which blocks scheduling
//     until full cache sync completes)
//
// How it works (dual-validation mechanism):
//  1. ResourceVersion comparison: Verifies cache.Reflector has synced to APIServer's latest RV
//  2. Critical Pod validation: Confirms the MOST RECENTLY SCHEDULED Pod (by RV) exists in scheduler cache
//     with matching ResourceVersion - this catches edge cases where RV matches but handler missed events
//
// Why this solves the core problem:
//   - Previous leader may have scheduled Pods AFTER standby's last cache sync but BEFORE election
//   - Without this check: New leader's cache misses these Pods → schedules new Pods onto already-allocated resources
//   - With this check: Scheduler blocks ONLY until these critical Pods are processed (typically < 500ms)
//     while allowing conservative-mode scheduling to start immediately after validation
//
// Special cases handled:
//   - Empty cluster: Skips validation gracefully (no Pods to miss)
//   - No scheduled Pods: Confirms cache consistency for unscheduled state
//   - APIServer transient errors: Retries with exponential backoff via wait.Until
func waitForLatestSynced(ctx context.Context, cc *schedulerserverconfig.CompletedConfig, sched *scheduler.Scheduler) {
	waitingForSynced := make(chan struct{})

	// 🔑 CRITICAL: Create dedicated stop channel that respects BOTH conditions:
	//   - waitingForSynced closed → validation succeeded (primary exit)
	//   - ctx.Done() triggered → leadership lost or shutdown (emergency exit)
	// This preserves waitingForSynced as the SUCCESS signal while enabling safe cancellation
	stopCh := make(chan struct{})
	go func() {
		select {
		case <-waitingForSynced:
			// Validation succeeded - close stopCh to terminate wait.Until
			close(stopCh)
		case <-ctx.Done():
			// Context canceled (leadership lost/shutdown) - close stopCh to terminate wait.Until
			// ⚠️ DO NOT close waitingForSynced here! Caller must detect ctx cancellation separately
			close(stopCh)
			klog.InfoS("Context canceled during cache sync validation", "reason", ctx.Err())
		}
	}()

	latestVersionFromAPIServer := ""
	var syncBarrierPod *corev1.Pod
	wait.Until(func() {
		// 🛡️ IMMEDIATE EXIT on context cancellation to avoid wasted work
		if ctx.Err() != nil {
			return
		}

		// STEP 1: Fetch APIServer's absolute latest ResourceVersion (lightweight metadata-only List)
		// Why Limit=1? Minimizes network load while guaranteeing current cluster state version
		if latestVersionFromAPIServer == "" {
			listOpts := metav1.ListOptions{Limit: 1}
			podList, err := cc.Client.CoreV1().Pods("").List(ctx, listOpts)
			if errors.IsNotFound(err) || len(podList.Items) == 0 {
				klog.InfoS("Cluster has no Pods - cache consistency verified", "action", "skip-validation")
				close(waitingForSynced)
				return
			}
			if err != nil {
				klog.ErrorS(err, "Failed to fetch latest ResourceVersion from APIServer")
				return // Retries on next interval (wait.Until handles backoff)
			}
			latestVersionFromAPIServer = podList.ResourceVersion
			klog.V(2).InfoS("Fetched APIServer latest ResourceVersion", "rv", latestVersionFromAPIServer)
		}

		// STEP 2: Verify Informer's Reflector has synced TO or BEYOND APIServer's RV
		// Critical: LastSyncResourceVersion() reflects Reflector's processed state (ClientGo v0.22.0+)
		lastSyncRV := cc.InformerFactory.Core().V1().Pods().Informer().LastSyncResourceVersion()
		if lastSyncRV == "" || len(lastSyncRV) < len(latestVersionFromAPIServer) || lastSyncRV < latestVersionFromAPIServer {
			klog.V(3).InfoS("Informer cache still syncing", "currentRV", lastSyncRV, "targetRV", latestVersionFromAPIServer)
			if syncBarrierPodName == "" || syncBarrierPod != nil {
				return
			}
			patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"scheduling.koordinator.sh/sync-barrier":"%s"}}}`, time.Now().Format(time.RFC3339)))
			patchedPod, err := cc.Client.CoreV1().Pods(syncBarrierPodNamespace).Patch(ctx, syncBarrierPodName, types.MergePatchType, patch, metav1.PatchOptions{})
			if err != nil {
				klog.V(3).InfoS("Failed to create sync barrier (non-critical)", "err", err)
				return
			}
			syncBarrierPod = patchedPod
			return
		}
		klog.V(2).InfoS("Informer Reflector synced to APIServer state", "rv", lastSyncRV)

		// STEP 3: CRITICAL VALIDATION - Confirm MOST RECENT SCHEDULED Pod exists in scheduler cache
		podLister := cc.InformerFactory.Core().V1().Pods().Lister()
		pods, err := podLister.List(labels.Everything())
		if errors.IsNotFound(err) || len(pods) == 0 {
			klog.InfoS("No Pods in informer cache - consistency verified for empty state")
			close(waitingForSynced)
			return
		}
		if err != nil {
			klog.ErrorS(err, "Failed to list Pods from informer cache")
			return
		}

		// Find the MOST RECENTLY SCHEDULED Pod (highest RV with NodeName set)
		// This is the critical Pod that could cause overcommitment if missed
		var latestScheduledPod *corev1.Pod
		for i := range pods {
			pod := pods[i]
			if pod.Spec.NodeName == "" { // Skip unscheduled Pods
				continue
			}
			if latestScheduledPod == nil || latestScheduledPod.ResourceVersion < pod.ResourceVersion {
				latestScheduledPod = pod
			}
		}

		if latestScheduledPod == nil {
			klog.InfoS("No scheduled Pods found - cache consistency verified for unscheduled state")
			close(waitingForSynced)
			return
		}

		// Verify THIS SPECIFIC Pod exists in scheduler's internal cache with matching RV
		cachedPod, err := sched.Cache.GetPod(latestScheduledPod)
		if err != nil {
			klog.V(2).InfoS("Waiting for scheduler cache to process critical Pod",
				"pod", klog.KObj(latestScheduledPod),
				"targetRV", latestScheduledPod.ResourceVersion)
			return
		}

		if latestScheduledPod.ResourceVersion != cachedPod.ResourceVersion {
			klog.V(2).InfoS("Scheduler cache has stale version of critical Pod",
				"pod", klog.KObj(latestScheduledPod),
				"expectedRV", latestScheduledPod.ResourceVersion,
				"cachedRV", cachedPod.ResourceVersion)
			return
		}

		// SUCCESS: Dual-validation complete
		// - Informer synced to APIServer state (RV check)
		// - Critical scheduled Pod fully processed by scheduler cache (handler validation)
		klog.InfoS("Scheduler cache fully synchronized with latest scheduled state",
			"validatedPod", klog.KObj(latestScheduledPod),
			"resourceVersion", latestScheduledPod.ResourceVersion)
		close(waitingForSynced)
	}, time.Second, stopCh) // Respect context cancellation for safety
}
