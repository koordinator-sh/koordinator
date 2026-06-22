/*
Copyright 2022 The Koordinator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-;

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scaledownbinpack

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
)

type RankedPod struct {
	Pod             *corev1.Pod
	Rank            int
	NodeName        string
	EvacuationScore float64
}

type NodeRankInfo struct {
	Node               *corev1.Node
	EligibleTargetPods []*corev1.Pod
	SkippedTargetPods  []*corev1.Pod
	NonTargetPods      []*corev1.Pod
	EvacuationScore    float64
}

// RankPods computes evacuation scores for nodes and assigns a global scale-down rank to target pods.
func RankPods(nodes []*corev1.Node, eligibleTargetPods []*corev1.Pod, skippedTargetPods []*corev1.Pod, nonTargetPods []*corev1.Pod, resourceWeights map[corev1.ResourceName]float64) []RankedPod {
	eligibleByNode := make(map[string][]*corev1.Pod)
	skippedByNode := make(map[string][]*corev1.Pod)
	nonTargetByNode := make(map[string][]*corev1.Pod)

	for _, p := range eligibleTargetPods {
		eligibleByNode[p.Spec.NodeName] = append(eligibleByNode[p.Spec.NodeName], p)
	}
	for _, p := range skippedTargetPods {
		skippedByNode[p.Spec.NodeName] = append(skippedByNode[p.Spec.NodeName], p)
	}
	for _, p := range nonTargetPods {
		nonTargetByNode[p.Spec.NodeName] = append(nonTargetByNode[p.Spec.NodeName], p)
	}

	if len(resourceWeights) == 0 {
		resourceWeights = map[corev1.ResourceName]float64{
			corev1.ResourceCPU:    1.0,
			corev1.ResourceMemory: 1.0,
		}
	}

	var nodeInfos []*NodeRankInfo

	// 1. Compute non-target tax score
	for _, node := range nodes {
		eligiblePods := eligibleByNode[node.Name]
		if len(eligiblePods) == 0 {
			// Skip nodes with zero eligible target pods
			continue
		}

		skippedPods := skippedByNode[node.Name]
		ntPods := nonTargetByNode[node.Name]

		var num, den float64
		for resName, weight := range resourceWeights {
			cap := node.Status.Capacity[resName]
			capMilli := cap.MilliValue()
			if capMilli <= 0 {
				continue
			}

			var tReq, ntReq int64
			for _, p := range eligiblePods {
				tReq += getPodResourceRequest(p, resName)
			}
			for _, p := range ntPods {
				ntReq += getPodResourceRequest(p, resName)
			}
			for _, p := range skippedPods {
				ntReq += getPodResourceRequest(p, resName)
			}

			uNt := float64(ntReq) / float64(capMilli)
			uT := float64(tReq) / float64(capMilli)

			num += weight * uNt
			den += weight * uT
		}

		score := 0.0
		if den > 0 {
			score = num / den
		}

		nodeInfos = append(nodeInfos, &NodeRankInfo{
			Node:               node,
			EligibleTargetPods: eligiblePods,
			SkippedTargetPods:  skippedPods,
			NonTargetPods:      ntPods,
			EvacuationScore:    score,
		})
	}

	// 2. Sort nodes
	sort.Slice(nodeInfos, func(i, j int) bool {
		if nodeInfos[i].EvacuationScore != nodeInfos[j].EvacuationScore {
			return nodeInfos[i].EvacuationScore < nodeInfos[j].EvacuationScore
		}
		lenI := len(nodeInfos[i].EligibleTargetPods)
		lenJ := len(nodeInfos[j].EligibleTargetPods)
		if lenI != lenJ {
			return lenI < lenJ
		}
		return nodeInfos[i].Node.Name < nodeInfos[j].Node.Name
	})

	var globalRanked []RankedPod
	globalRank := 0

	// 3. Sort pods within node and assign global ranks
	for _, info := range nodeInfos {
		sort.Slice(info.EligibleTargetPods, func(i, j int) bool {
			pi, pj := info.EligibleTargetPods[i], info.EligibleTargetPods[j]
			sizeI := getPodSize(pi, resourceWeights)
			sizeJ := getPodSize(pj, resourceWeights)

			if sizeI != sizeJ {
				return sizeI > sizeJ // descending
			}
			if !pi.CreationTimestamp.Equal(&pj.CreationTimestamp) {
				return pj.CreationTimestamp.Before(&pi.CreationTimestamp) // descending (newer first)
			}

			if pi.Namespace != pj.Namespace {
				return pi.Namespace < pj.Namespace // ascending
			}
			return pi.Name < pj.Name // ascending
		})

		for _, p := range info.EligibleTargetPods {
			globalRanked = append(globalRanked, RankedPod{
				Pod:             p,
				Rank:            globalRank,
				NodeName:        info.Node.Name,
				EvacuationScore: info.EvacuationScore,
			})
			globalRank++
		}
	}

	return globalRanked
}

func getPodResourceRequest(pod *corev1.Pod, resourceName corev1.ResourceName) int64 {
	var req int64
	for _, c := range pod.Spec.Containers {
		if val, ok := c.Resources.Requests[resourceName]; ok {
			req += val.MilliValue()
		}
	}
	return req
}

func getPodSize(pod *corev1.Pod, weights map[corev1.ResourceName]float64) float64 {
	var size float64
	for resName, weight := range weights {
		size += weight * float64(getPodResourceRequest(pod, resName))
	}
	return size
}
