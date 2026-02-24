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

package extension

import (
	"encoding/json"
	"sort"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (

	// AnnotationGangPodNetworkTopologyIndex defines the index of a specific pod in the gang group which should typically
	// match the underlying biz semantics (such as PyTorch rank).
	// This would currently be considered in network topology aware scheduling.
	AnnotationGangPodNetworkTopologyIndex = AnnotationGangPrefix + "/network-topology-index"

	// AnnotationGangNetworkTopologySpec defines the network topology aware requirements of the gang group.
	AnnotationGangNetworkTopologySpec = AnnotationGangPrefix + "/network-topology-spec"

	AnnotationPodNetworkTopologySelector = SchedulingDomainPrefix + "/network-topology-selector"
)

type NetworkTopologySpec struct {
	GatherStrategy []NetworkTopologyGatherRule `json:"gatherStrategy,omitempty"`
}

type NetworkTopologyGatherRule struct {
	Layer    schedulingv1alpha1.TopologyLayer `json:"layer"`
	Strategy NetworkTopologyGatherStrategy    `json:"strategy"`
}

type NetworkTopologyGatherStrategy string

const (
	NetworkTopologyGatherStrategyMustGather   NetworkTopologyGatherStrategy = "MustGather"
	NetworkTopologyGatherStrategyPreferGather NetworkTopologyGatherStrategy = "PreferGather"
)

func GetNetworkTopologySpec(obj metav1.Object) (*NetworkTopologySpec, error) {
	spec := obj.GetAnnotations()[AnnotationGangNetworkTopologySpec]
	if spec == "" {
		return nil, nil
	}
	var networkTopologySpec NetworkTopologySpec
	if err := json.Unmarshal([]byte(spec), &networkTopologySpec); err != nil {
		return nil, err
	}
	return &networkTopologySpec, nil
}

func GetPodNetworkTopologyIndex(pod *corev1.Pod) (int, error) {
	if pod == nil {
		return -1, nil
	}
	indexStr, ok := pod.Annotations[AnnotationGangPodNetworkTopologyIndex]
	if !ok {
		return -1, nil
	}
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		return -1, err
	}
	return index, nil
}

// SortPodsByIndex sort pods by index than by name,
// pods without valid index would be ordered after pods with index.
func SortPodsByIndex(pods []*corev1.Pod) {
	sort.Slice(pods, func(a, b int) bool {
		podA, podB := pods[a], pods[b]
		indexA, _ := GetPodNetworkTopologyIndex(podA)
		indexB, _ := GetPodNetworkTopologyIndex(podB)
		if indexA >= 0 && indexB >= 0 {
			if indexA != indexB {
				return indexA < indexB
			}
		} else if indexA >= 0 || indexB >= 0 {
			return indexA >= 0
		}
		return podA.Name < podB.Name
	})
}

func GetPodNetworkTopologySelector(obj metav1.Object) string {
	return obj.GetAnnotations()[AnnotationPodNetworkTopologySelector]
}
