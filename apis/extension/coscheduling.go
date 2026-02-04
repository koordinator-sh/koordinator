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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

const (
	AnnotationGangPrefix = "gang.scheduling.koordinator.sh"
	// AnnotationGangName specifies the name of the gang
	AnnotationGangName = AnnotationGangPrefix + "/name"

	// AnnotationGangMinNum specifies the minimum number of the gang that can be executed
	AnnotationGangMinNum = AnnotationGangPrefix + "/min-available"

	// AnnotationGangWaitTime specifies gang's max wait time in Permit Stage
	AnnotationGangWaitTime = AnnotationGangPrefix + "/waiting-time"

	// AnnotationGangTotalNum specifies the total children number of the gang
	// If not specified,it will be set with the AnnotationGangMinNum
	AnnotationGangTotalNum = AnnotationGangPrefix + "/total-number"

	// AnnotationGangMode defines the Gang Scheduling operation when failed scheduling
	// Support GangModeStrict and GangModeNonStrict, default is GangModeStrict
	AnnotationGangMode = AnnotationGangPrefix + "/mode"

	// AnnotationGangGroups defines which gangs are bundled as a group
	// The gang will go to bind only all gangs in one group meet the conditions
	AnnotationGangGroups = AnnotationGangPrefix + "/groups"

	// AnnotationGangTimeout means that the entire gang cannot be scheduled due to timeout
	// The annotation is added by the scheduler when the gang times out
	AnnotationGangTimeout = AnnotationGangPrefix + "/timeout"

	GangModeStrict    = "Strict"
	GangModeNonStrict = "NonStrict"

	// AnnotationGangMatchPolicy defines the Gang Scheduling operation of taking which status pod into account
	// Support GangMatchPolicyOnlyWaiting, GangMatchPolicyWaitingAndRunning, GangMatchPolicyOnceSatisfied, default is GangMatchPolicyOnceSatisfied
	AnnotationGangMatchPolicy        = AnnotationGangPrefix + "/match-policy"
	GangMatchPolicyOnlyWaiting       = "only-waiting"
	GangMatchPolicyWaitingAndRunning = "waiting-and-running"
	GangMatchPolicyOnceSatisfied     = "once-satisfied"

	// AnnotationAliasGangMatchPolicy defines same match policy but different prefix.
	// Duplicate definitions here are only for compatibility considerations
	AnnotationAliasGangMatchPolicy = "pod-group.scheduling.sigs.k8s.io/match-policy"

	// AnnotationGangPodNetworkTopologyIndex defines the index of a specific pod in the gang group which should typically
	// match the underlying biz semantics (such as PyTorch rank).
	// This would currently be considered in network topology aware scheduling.
	AnnotationGangPodNetworkTopologyIndex = AnnotationGangPrefix + "/network-topology-index"

	// AnnotationGangNetworkTopologySpec defines the network topology aware requirements of the gang group.
	AnnotationGangNetworkTopologySpec = AnnotationGangPrefix + "/network-topology-spec"
)

const (
	// Deprecated: kubernetes-sigs/scheduler-plugins/lightweight-coscheduling
	LabelLightweightCoschedulingPodGroupName = "pod-group.scheduling.sigs.k8s.io/name"
	// Deprecated: kubernetes-sigs/scheduler-plugins/lightweight-coscheduling
	LabelLightweightCoschedulingPodGroupMinAvailable = "pod-group.scheduling.sigs.k8s.io/min-available"
)

func GetGangMinNum(pod *corev1.Pod) (int, error) {
	minRequiredNum, err := strconv.ParseInt(pod.Annotations[AnnotationGangMinNum], 10, 32)
	if err != nil {
		return 0, err
	}
	return int(minRequiredNum), nil
}

func GetGangTotalNum(obj metav1.Object) (int, error) {
	totalNumStr := obj.GetAnnotations()[AnnotationGangTotalNum]
	if totalNumStr == "" {
		return 0, nil
	}
	totalNum, err := strconv.ParseInt(totalNumStr, 10, 32)
	if err != nil {
		return 0, err
	}
	return int(totalNum), nil
}

func GetGangName(pod *corev1.Pod) string {
	return pod.Annotations[AnnotationGangName]
}

func GetGangMatchPolicy(obj metav1.Object) string {
	policy := obj.GetAnnotations()[AnnotationGangMatchPolicy]
	if policy != "" {
		return policy
	}
	return obj.GetAnnotations()[AnnotationAliasGangMatchPolicy]
}

func GetGangWaitTime(pod *corev1.Pod) (time.Duration, error) {
	waitTimeStr := pod.Annotations[AnnotationGangWaitTime]
	if waitTimeStr == "" {
		return 0, nil
	}
	return time.ParseDuration(waitTimeStr)
}

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

func GetPodIndex(pod *corev1.Pod) (int, error) {
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
		indexA, _ := GetPodIndex(podA)
		indexB, _ := GetPodIndex(podB)
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

const (
	// DEPRECATED: This api is marked as internal and will be removed next version.
	// Please use the domain `internal.scheduling.koordinator.sh/` instead.
	// DeprecatedAnnotationBindGangGroupId is the annotation key for the pod's gang group id to bind.
	DeprecatedAnnotationBindGangGroupId = SchedulingDomainPrefix + "/bind-gang-group-id"
	// AnnotationBindGangGroupId is the annotation key for the pod's gang group id to bind.
	AnnotationBindGangGroupId = InternalSchedulingDomainPrefix + "/bind-gang-group-id"
	// DEPRECATED: This api is marked as internal and will be removed next version.
	// Please use the domain `internal.scheduling.koordinator.sh/` instead.
	// DeprecatedAnnotationBindGangMemberCount is the annotation key for the count of the pod's gang group member pods to bind.
	DeprecatedAnnotationBindGangMemberCount = SchedulingDomainPrefix + "/bind-gang-member-count"
	// AnnotationBindGangMemberCount is the annotation key for the count of the pod's gang group member pods to bind.
	AnnotationBindGangMemberCount = InternalSchedulingDomainPrefix + "/bind-gang-member-count"
)
