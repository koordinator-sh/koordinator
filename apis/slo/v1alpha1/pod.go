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

package v1alpha1

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	AnnotationPodCPUBurst = apiext.DomainPrefix + "cpuBurst"

	AnnotationPodMemoryQoS = apiext.DomainPrefix + "memoryQOS"

	AnnotationPodBlkioQoS = apiext.DomainPrefix + "blkioQOS"
)

func GetPodCPUBurstConfig(pod *corev1.Pod) (*CPUBurstConfig, error) {
	if pod == nil || pod.Annotations == nil {
		return nil, nil
	}
	annotation, exist := pod.Annotations[AnnotationPodCPUBurst]
	if !exist {
		return nil, nil
	}
	cpuBurst := CPUBurstConfig{}

	err := json.Unmarshal([]byte(annotation), &cpuBurst)
	if err != nil {
		return nil, err
	}
	return &cpuBurst, nil
}

func GetPodMemoryQoSConfig(pod *corev1.Pod) (*PodMemoryQOSConfig, error) {
	if pod == nil || pod.Annotations == nil {
		return nil, nil
	}
	value, exist := pod.Annotations[AnnotationPodMemoryQoS]
	if !exist {
		return nil, nil
	}
	cfg := PodMemoryQOSConfig{}
	err := json.Unmarshal([]byte(value), &cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

const (
	// LabelCoreSchedGroupID is the label key of the group ID of the Linux Core Scheduling.
	// Value should be a valid UUID or the none value "0".
	// When the value is a valid UUID, pods with that group ID and the equal CoreExpelled status on the node will be
	// assigned to the same core sched cookie.
	// When the value is the none value "0", pod will be reset to the default core sched cookie `0`.
	// When the k-v pair is missing but the node-level strategy enables the core sched, the pod will be assigned an
	// internal group according to the pod's UID.
	//
	// Core Sched: https://docs.kernel.org/admin-guide/hw-vuln/core-scheduling.html
	// When the Core Sched is enabled, pods with the different core sched group IDs will not be running at the same SMT
	// core at the same time, which means they will take different core sched cookies. If a pod sets the core sched
	// disabled, it will take the default core sched cookie (0) and will also be force-idled to run on the same SMT core
	// concurrently with the core-sched-enabled pods. In addition, the CoreExpelled configured in ResourceQOS also
	// enables the individual cookie from pods of other QoS classes via adding a suffix for the group ID. So the pods
	// of different QoS will take different cookies when their CoreExpelled status are diverse even if their group ID
	// are the same.
	LabelCoreSchedGroupID = apiext.DomainPrefix + "core-sched-group-id"

	// CoreSchedGroupIDNone is the none value of the core sched group ID which indicates the core sched is disabled for
	// the pod. The pod will be reset to the system-default cookie `0`.
	CoreSchedGroupIDNone = "0"
)

// GetCoreSchedGroupID gets the core sched group ID from the pod labels.
// It returns the core sched group ID and whether the pod explicitly disables the core sched.
func GetCoreSchedGroupID(labels map[string]string) (string, *bool) {
	if labels == nil {
		return "", nil
	}
	value, ok := labels[LabelCoreSchedGroupID]
	if !ok {
		return "", nil
	}
	return value, pointer.Bool(value == CoreSchedGroupIDNone)
}
