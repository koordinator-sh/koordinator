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

package equivalence

import (
	"math"

	corev1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-helpers/resource"
	fwktype "k8s.io/kube-scheduler/framework"
	k8sfeatures "k8s.io/kubernetes/pkg/features"
)

// podRequestsForQuota returns the per-pod request aggregate used for equivalence-class quota
// computation. It mirrors upstream NodeResourcesFit's PreFilter accounting (init-container max,
// overhead, extended resources) and respects the PodLevelResources feature gate the same way,
// so the cached quota stays consistent with the Filter that validates it on the fast path.
func podRequestsForQuota(pod *corev1.Pod) corev1.ResourceList {
	return resource.PodRequests(pod, resource.PodResourcesOptions{
		SkipPodLevelResources: !utilfeature.DefaultFeatureGate.Enabled(k8sfeatures.PodLevelResources),
	})
}

// buildQuotaNodes computes the per-node capacity quota for the class at backfill time.
// Pods of one class are template-identical, so the per-node fit check collapses into one division
// per resource dimension: quota = (allocatable - requested) / podRequest. The requested baseline is
// the snapshot's aggregate of every pod already on the node — running and assumed alike — so
// existing occupants of any origin (other classes, other schedulers, daemonsets) are fully
// accounted for. Occupancy changes after the backfill are not; the drift threshold, node-event
// flush, and TTL bound that window, and the per-pod Filter on the fast path rejects any node the
// quota overestimates. Pods of this class placed by either path afterwards are accounted by the
// quota decrements in consume; pods placed via a nominated node skip it and are bounded by the
// same TTL.
func buildQuotaNodes(pod *corev1.Pod, nodeNames []string, lister fwktype.SharedLister) []equivalenceClassNode {
	podReqs := podRequestsForQuota(pod)
	out := make([]equivalenceClassNode, 0, len(nodeNames))
	for _, name := range nodeNames {
		nodeInfo, err := lister.NodeInfos().Get(name)
		if err != nil {
			// The node left the snapshot between the full-path filter and now; skip it rather
			// than caching a name the fast path cannot re-check anyway.
			continue
		}
		quota := nodeQuotaForPod(podReqs, nodeInfo)
		if quota <= 0 {
			continue
		}
		out = append(out, equivalenceClassNode{name: name, quota: quota})
	}
	return out
}

// nodeQuotaForPod returns how many more pods of the class fit the node, as the minimum across
// the pod's requested resource dimensions plus the pod-count dimension. Dimensions the pod does
// not request are skipped (any number of such pods fit along them).
func nodeQuotaForPod(podReqs corev1.ResourceList, nodeInfo fwktype.NodeInfo) int64 {
	requested := nodeInfo.GetRequested()
	allocatable := nodeInfo.GetAllocatable()

	quota := int64(math.MaxInt64)
	addDimension := func(available, perPod int64) {
		if perPod <= 0 {
			return
		}
		if q := available / perPod; q < quota {
			quota = q
		}
	}

	cpuReq := podReqs[corev1.ResourceCPU]
	addDimension(allocatable.GetMilliCPU()-requested.GetMilliCPU(), cpuReq.MilliValue())
	memReq := podReqs[corev1.ResourceMemory]
	addDimension(allocatable.GetMemory()-requested.GetMemory(), memReq.Value())
	ephemeralReq := podReqs[corev1.ResourceEphemeralStorage]
	addDimension(allocatable.GetEphemeralStorage()-requested.GetEphemeralStorage(), ephemeralReq.Value())

	// Every pod consumes one pod slot regardless of its resource requests. This dimension
	// always sets the quota, so the result is never left at MaxInt64.
	addDimension(int64(allocatable.GetAllowedPodNumber()-len(nodeInfo.GetPods())), 1)

	for name, perPod := range podReqs {
		if perPod.IsZero() || perPod.Sign() < 0 {
			continue
		}
		if name == corev1.ResourceCPU || name == corev1.ResourceMemory || name == corev1.ResourceEphemeralStorage || name == corev1.ResourcePods {
			continue
		}
		// Extended resources (e.g. koordinator batch/mid resources, devices).
		available := allocatable.GetScalarResources()[name] - requested.GetScalarResources()[name]
		addDimension(available, perPod.Value())
	}

	return quota
}
