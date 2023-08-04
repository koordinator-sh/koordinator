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
	"strconv"

	corev1 "k8s.io/api/core/v1"
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
)

const (
	// Deprecated: kubernetes-sigs/scheduler-plugins/lightweight-coscheduling
	LabelLightweightCoschedulingPodGroupName = "pod-group.scheduling.sigs.k8s.io/name"
	// Deprecated: kubernetes-sigs/scheduler-plugins/lightweight-coscheduling
	LabelLightweightCoschedulingPodGroupMinAvailable = "pod-group.scheduling.sigs.k8s.io/min-available"
)

func GetMinNum(pod *corev1.Pod) (int, error) {
	minRequiredNum, err := strconv.ParseInt(pod.Annotations[AnnotationGangMinNum], 10, 32)
	if err != nil {
		return 0, err
	}
	return int(minRequiredNum), nil
}

func GetGangName(pod *corev1.Pod) string {
	return pod.Annotations[AnnotationGangName]
}

func GetGangMatchPolicy(pod *corev1.Pod) string {
	policy := pod.Annotations[AnnotationGangMatchPolicy]
	if policy != "" {
		return policy
	}
	return pod.Annotations[AnnotationAliasGangMatchPolicy]
}
