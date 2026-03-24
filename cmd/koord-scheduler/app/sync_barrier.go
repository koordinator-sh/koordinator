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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler"

	schedulerserverconfig "github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app/config"
	"github.com/koordinator-sh/koordinator/pkg/features"
)

var (
	syncBarrierPodNamespace = metav1.NamespaceSystem
	syncBarrierPodName      = "koord-scheduler-sync-barrier"

	// Default hard timeout for the synchronization process.
	syncHardTimeout = 1 * time.Minute
)

func AddSyncBarrierFlags(fs *pflag.FlagSet) {
	if fs == nil {
		return
	}
	fs.StringVar(&syncBarrierPodNamespace, "sync-barrier-pod-namespace", syncBarrierPodNamespace, "sync barrier namespace")
	fs.StringVar(&syncBarrierPodName, "sync-barrier-pod-name", syncBarrierPodName, "sync barrier pod name")
	fs.DurationVar(&syncHardTimeout, "sync-hard-timeout", syncHardTimeout, "hard timeout for the synchronization process")

}

// waitForLatestSynced ensures the scheduler's internal cache is logically consistent with the
// API server's state immediately AFTER acquiring leadership.
//
// THE PROBLEM:
// During leader election, there is a "blind spot" where the previous leader might have scheduled
// Pods (binding them to nodes) in the final moments before losing leadership. The new standby
// leader's Informer cache might not yet have received these bind events. If the new leader
// starts scheduling immediately, it may overcommit node resources by assigning new Pods to
// nodes whose capacity is already consumed by the "missing" Pods.
//
// THE SOLUTION (Barrier + Anchor Mechanism):
// This function implements a lightweight synchronization barrier to close this gap without the
// high latency of a full cache re-sync (--delay-cache-until-active):
//
//  1. Barrier Flush: It patches a dedicated "SyncBarrierPod". Since the API server processes
//     requests sequentially for a single resource, the resulting ResourceVersion (targetRV)
//     acts as a "watermark". Any scheduling decision made by the previous leader MUST have
//     a ResourceVersion lower than this targetRV.
//
//  2. Informer Synchronization: It waits for the Pod Informer to observe the SyncBarrierPod
//     with at least the targetRV. This ensures the Informer's "pipe" has been flushed and
//     contains all events prior to the current leadership.
//
//  3. Anchor Snapshot: To prevent the "chasing effect" (where high-churn pods keep increasing
//     their RVs in the Informer, making the Cache never catch up), it captures a snapshot of
//     the most recently scheduled Pod (the "Anchor") at the exact moment the barrier is reached.
//
//  4. Cache Reconciliation: It blocks until the Scheduler's internal Cache (which only stores
//     scheduled pods) has processed the Anchor Pod. Once the Cache reaches the Anchor's RV,
//     we are guaranteed that all relevant resource allocations are accounted for.
//
// EDGE CASES & SAFETY:
// - Hard Timeout: A 10s safety limit ensures scheduling is not blocked indefinitely.
// - Resilience: Gracefully handles missing SyncBarrierPods, API timeouts, and deletions.
// - Panic Prevention: Uses sync.Once to ensure channels are closed exactly once.
func waitForLatestSynced(ctx context.Context, cc *schedulerserverconfig.CompletedConfig, sched *scheduler.Scheduler) {
	if !k8sfeature.DefaultFeatureGate.Enabled(features.SyncBarrier) || syncBarrierPodName == "" {
		klog.InfoS("SyncBarrierPod not configured, skipping enhanced cache sync")
		return
	}

	// Initialize Context with hard timeout
	syncCtx, cancel := context.WithTimeout(ctx, syncHardTimeout)
	defer cancel()

	// Use sync.Once to prevent "panic: close of closed channel"
	var once sync.Once
	stopCh := make(chan struct{})
	stopSync := func() {
		once.Do(func() {
			close(stopCh)
		})
	}

	// Monitor context cancellation to stop the loop
	go func() {
		select {
		case <-syncCtx.Done():
			stopSync()
		}
	}()

	var targetRV string
	var anchorPod *corev1.Pod
	barrierAnnotationKey := fmt.Sprintf("scheduling.koordinator.sh/sync-barrier-%s", cc.ComponentConfig.LeaderElection.ResourceName)
	expectedValue := time.Now().Format(time.RFC3339Nano)

	klog.InfoS("Starting scheduler cache synchronization barrier", "barrierPod", syncBarrierPodName)

	wait.Until(func() {
		// Defensive check for context expiration
		if syncCtx.Err() != nil {
			return
		}

		podLister := cc.InformerFactory.Core().V1().Pods().Lister()

		// STEP 1: Barrier Flush - Patch the Barrier Pod to generate a targetRV
		if targetRV == "" {
			patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, barrierAnnotationKey, expectedValue))
			patchedPod, err := cc.Client.CoreV1().Pods(syncBarrierPodNamespace).Patch(syncCtx, syncBarrierPodName, types.MergePatchType, patch, metav1.PatchOptions{})

			if err != nil {
				if apierrors.IsNotFound(err) {
					klog.ErrorS(err, "SyncBarrierPod is missing, aborting enhanced validation", "pod", syncBarrierPodName)
					stopSync()
					return
				}
				klog.V(3).InfoS("Transient error patching barrier pod, retrying", "err", err)
				return
			}
			targetRV = patchedPod.ResourceVersion
			klog.InfoS("Sync barrier watermark established", "targetRV", targetRV)
		}

		// STEP 2: Informer Sync - Wait for Informer to see the targetRV
		if anchorPod == nil {
			barrierPod, err := podLister.Pods(syncBarrierPodNamespace).Get(syncBarrierPodName)
			if err != nil {
				if apierrors.IsNotFound(err) {
					klog.ErrorS(err, "SyncBarrierPod disappeared during sync, aborting validation")
					stopSync()
					return
				}
				return
			}

			if !isRVReached(barrierPod.ResourceVersion, targetRV) {
				klog.V(4).InfoS("Informer still catching up to barrier pod", "currentRV", barrierPod.ResourceVersion, "targetRV", targetRV)
				return
			}

			// STEP 3: Anchor Snapshot - Capture the latest scheduled pod at this moment
			pods, err := podLister.List(labels.Everything())
			if err != nil {
				klog.V(3).InfoS("Failed to list pods from informer", "err", err)
				return
			}

			for _, p := range pods {
				if p.Spec.NodeName != "" {
					if anchorPod == nil || isRVReached(p.ResourceVersion, anchorPod.ResourceVersion) {
						anchorPod = p
					}
				}
			}

			if anchorPod == nil {
				klog.InfoS("No scheduled pods found at barrier point, sync complete")
				stopSync()
				return
			}
			klog.InfoS("Anchor pod captured for reconciliation", "pod", klog.KObj(anchorPod), "rv", anchorPod.ResourceVersion)
		}

		// STEP 4: Cache Reconciliation - Wait for Scheduler Cache to catch up to Anchor
		cachedPod, err := sched.Cache.GetPod(anchorPod)
		if err != nil {
			// If anchor is missing from Cache, verify if it was deleted from API Server
			_, listerErr := podLister.Pods(anchorPod.Namespace).Get(anchorPod.Name)
			if apierrors.IsNotFound(listerErr) {
				klog.InfoS("Anchor pod was deleted from cluster, sync complete", "pod", klog.KObj(anchorPod))
				stopSync()
				return
			}
			klog.V(4).InfoS("Anchor pod not yet processed by scheduler cache", "pod", klog.KObj(anchorPod))
			return
		}

		if isRVReached(cachedPod.ResourceVersion, anchorPod.ResourceVersion) {
			klog.InfoS("Scheduler cache is now synchronized",
				"anchorPod", klog.KObj(anchorPod),
				"cacheRV", cachedPod.ResourceVersion,
				"barrierRV", targetRV)
			stopSync()
		}
	}, 100*time.Millisecond, stopCh)

	// Final status logging
	if errors.Is(syncCtx.Err(), context.DeadlineExceeded) {
		klog.V(3).InfoS("Cache sync validation timed out; starting scheduler with potentially stale cache", "timeout", syncHardTimeout)
	} else if ctx.Err() != nil {
		klog.InfoS("Cache sync aborted due to leadership loss or shutdown")
	} else {
		klog.InfoS("Cache synchronization completed successfully")
	}
}

// isRVReached compares two ResourceVersions safely.
// In Kubernetes (etcd), ResourceVersions are monotonically increasing integers stored as strings.
func isRVReached(current, target string) bool {
	if current == target {
		return true
	}
	// Longer string or lexicographically larger string of same length represents a newer RV.
	if len(current) != len(target) {
		return len(current) > len(target)
	}
	return current > target
}
