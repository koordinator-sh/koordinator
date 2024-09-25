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
	"fmt"

	corev1 "k8s.io/api/core/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	AnnotationPodCPUBurst = apiext.DomainPrefix + "cpuBurst"

	AnnotationPodCPUQoS = apiext.DomainPrefix + "cpuQOS"

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

func GetPodCPUQoSConfigByAttr(labels, annotations map[string]string) (*CPUQOSCfg, error) {
	value, exist := annotations[AnnotationPodCPUQoS]
	if !exist {
		return nil, nil
	}
	cfg := CPUQOSCfg{}
	err := json.Unmarshal([]byte(value), &cfg)
	if err != nil {
		return nil, err
	}

	// check before return
	if cfg.GroupIdentity != nil {
		bvt := *cfg.GroupIdentity
		// bvt value allowed [-1, 2], see https://help.aliyun.com/zh/alinux/user-guide/group-identity-feature
		if bvt < -1 || bvt > 2 {
			return nil, fmt.Errorf("bad group identity value: %v", bvt)
		}
	}
	return &cfg, nil
}

const (
	// LabelCoreSchedGroupID is the label key of the group ID of the Linux Core Scheduling.
	// Value can be a valid UUID or empty. If it is empty, the pod is considered to belong to a core sched group "".
	// Otherwise, the pod is set its core sched group ID according to the value.
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

	// LabelCoreSchedPolicy is the label key that indicates the particular policy of the core scheduling.
	// It is optional and the policy is considered as the default when the label is not set.
	// It supports the following policies:
	// - "" or not set (default): If the core sched is enabled for the node, the pod is set the group ID according to
	//   the value of the LabelCoreSchedGroupID.
	// - "none": The core sched is explicitly disabled for the pod even if the node-level strategy is enabled.
	// - "exclusive": If the core sched is enabled for the node, the pod is set the group ID according to the pod UID,
	//   so that the pod is exclusive to any other pods.
	LabelCoreSchedPolicy = apiext.DomainPrefix + "core-sched-policy"
)

type CoreSchedPolicy string

const (
	// CoreSchedPolicyDefault is the default policy of the core scheduling which indicates the core sched group ID
	// is set according to the value of the LabelCoreSchedGroupID.
	CoreSchedPolicyDefault CoreSchedPolicy = ""
	// CoreSchedPolicyNone is the none policy of the core scheduling which indicates the core sched is disabled for
	// the pod. The pod will be reset to the system-default cookie `0`.
	CoreSchedPolicyNone CoreSchedPolicy = "none"
	// CoreSchedPolicyExclusive is the exclusive policy of the core scheduling which indicates the core sched group ID
	// is set the same as the pod's UID
	CoreSchedPolicyExclusive CoreSchedPolicy = "exclusive"
)

// GetCoreSchedGroupID gets the core sched group ID for the pod according to the labels.
func GetCoreSchedGroupID(labels map[string]string) string {
	if labels != nil {
		return labels[LabelCoreSchedGroupID]
	}
	return ""
}

// GetCoreSchedPolicy gets the core sched policy for the pod according to the labels.
func GetCoreSchedPolicy(labels map[string]string) CoreSchedPolicy {
	if labels == nil {
		return CoreSchedPolicyDefault
	}
	if v := labels[LabelCoreSchedPolicy]; v == string(CoreSchedPolicyNone) {
		return CoreSchedPolicyNone
	} else if v == string(CoreSchedPolicyExclusive) {
		return CoreSchedPolicyExclusive
	}
	return CoreSchedPolicyDefault
}
