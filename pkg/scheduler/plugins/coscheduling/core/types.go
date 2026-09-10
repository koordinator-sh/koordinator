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

package core

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
)

const (
	GangFromPodGroupCrd   string = "GangFromPodGroupCrd"
	GangFromPodAnnotation string = "GangFromPodAnnotation"
)

// GangInfo defines the interface for different gang sources (Pod annotations or PodGroup CRD).
type GangInfo interface {
	GetGangFrom() string
	GetRawAnnotation(key string) string
	GetMinMember() (int32, error)
	GetTotalMember() (int32, error)
	GetMode() (string, error)
	GetWaitTime() (time.Duration, error)
	GetMatchPolicy() (string, error)
	GetGangGroups() ([]string, error)
	GetNetworkTopologySpec() (*extension.NetworkTopologySpec, error)
	GetCreationTimestamp() metav1.Time
}

// AnnotationGangInfo implements GangInfo using Pod annotations.
type AnnotationGangInfo struct {
	pod  *corev1.Pod
	args *config.CoschedulingArgs
}

func NewAnnotationGangInfo(pod *corev1.Pod, args *config.CoschedulingArgs) *AnnotationGangInfo {
	return &AnnotationGangInfo{pod: pod, args: args}
}

func (a *AnnotationGangInfo) GetGangFrom() string {
	return GangFromPodAnnotation
}

func (a *AnnotationGangInfo) GetRawAnnotation(key string) string {
	if a.pod == nil || a.pod.Annotations == nil {
		return ""
	}
	return a.pod.Annotations[key]
}

func (a *AnnotationGangInfo) GetMinMember() (int32, error) {
	min, err := util.GetGangMinNumFromPod(a.pod)
	if err != nil {
		return 0, err
	}
	return int32(min), nil
}

func (a *AnnotationGangInfo) GetTotalMember() (int32, error) {
	total, err := extension.GetGangTotalNum(a.pod)
	if err != nil {
		return 0, err
	}
	return int32(total), nil
}

func (a *AnnotationGangInfo) GetMode() (string, error) {
	mode := a.GetRawAnnotation(extension.AnnotationGangMode)
	if mode == "" {
		return extension.GangModeStrict, nil
	}
	if mode != extension.GangModeStrict && mode != extension.GangModeNonStrict {
		return extension.GangModeStrict, fmt.Errorf("illegal gang mode: %s", mode)
	}
	return mode, nil
}

func (a *AnnotationGangInfo) GetWaitTime() (time.Duration, error) {
	defaultTimeout := 0 * time.Second
	if a.args != nil {
		defaultTimeout = a.args.DefaultTimeout.Duration
	}
	waitTime, err := extension.GetGangWaitTime(a.pod)
	if err != nil {
		return defaultTimeout, err
	}
	if waitTime < 0 {
		return defaultTimeout, fmt.Errorf("gang waitTime cannot be negative: %v", waitTime)
	}
	if waitTime == 0 {
		return defaultTimeout, nil
	}
	return waitTime, nil
}

func (a *AnnotationGangInfo) GetMatchPolicy() (string, error) {
	defaultPolicy := extension.GangMatchPolicyOnceSatisfied
	if a.args != nil && a.args.DefaultMatchPolicy != "" {
		defaultPolicy = a.args.DefaultMatchPolicy
	}
	policy := extension.GetGangMatchPolicy(a.pod)
	if policy == "" {
		return defaultPolicy, nil
	}
	if policy != extension.GangMatchPolicyOnlyWaiting &&
		policy != extension.GangMatchPolicyWaitingAndRunning &&
		policy != extension.GangMatchPolicyOnceSatisfied {
		return defaultPolicy, fmt.Errorf("illegal gang match policy: %s", policy)
	}
	return policy, nil
}

func (a *AnnotationGangInfo) GetGangGroups() ([]string, error) {
	groups, err := util.StringToGangGroupSlice(a.GetRawAnnotation(extension.AnnotationGangGroups))
	if err != nil {
		return nil, err
	}
	return groups, nil
}

func (a *AnnotationGangInfo) GetNetworkTopologySpec() (*extension.NetworkTopologySpec, error) {
	return extension.GetNetworkTopologySpec(a.pod)
}

func (a *AnnotationGangInfo) GetCreationTimestamp() metav1.Time {
	if a.pod == nil {
		return metav1.Time{}
	}
	return a.pod.CreationTimestamp
}

// PodGroupGangInfo implements GangInfo using the PodGroup CRD.
type PodGroupGangInfo struct {
	pg   *v1alpha1.PodGroup
	args *config.CoschedulingArgs
}

func NewPodGroupGangInfo(pg *v1alpha1.PodGroup, args *config.CoschedulingArgs) *PodGroupGangInfo {
	return &PodGroupGangInfo{pg: pg, args: args}
}

func (p *PodGroupGangInfo) GetGangFrom() string {
	return GangFromPodGroupCrd
}

func (p *PodGroupGangInfo) GetRawAnnotation(key string) string {
	if p.pg == nil || p.pg.Annotations == nil {
		return ""
	}
	return p.pg.Annotations[key]
}

func (p *PodGroupGangInfo) GetMinMember() (int32, error) {
	if p.pg == nil {
		return 0, nil
	}
	return p.pg.Spec.MinMember, nil
}

func (p *PodGroupGangInfo) GetTotalMember() (int32, error) {
	total, err := extension.GetGangTotalNum(p.pg)
	if err != nil {
		return 0, err
	}
	return int32(total), nil
}

func (p *PodGroupGangInfo) GetMode() (string, error) {
	mode := p.GetRawAnnotation(extension.AnnotationGangMode)
	if mode == "" {
		return extension.GangModeStrict, nil
	}
	if mode != extension.GangModeStrict && mode != extension.GangModeNonStrict {
		return extension.GangModeStrict, fmt.Errorf("illegal gang mode: %s", mode)
	}
	return mode, nil
}

func (p *PodGroupGangInfo) GetWaitTime() (time.Duration, error) {
	defaultTimeout := 0 * time.Second
	if p.args != nil {
		defaultTimeout = p.args.DefaultTimeout.Duration
	}
	return util.GetWaitTimeDuration(p.pg, defaultTimeout), nil
}

func (p *PodGroupGangInfo) GetMatchPolicy() (string, error) {
	defaultPolicy := extension.GangMatchPolicyOnceSatisfied
	if p.args != nil && p.args.DefaultMatchPolicy != "" {
		defaultPolicy = p.args.DefaultMatchPolicy
	}
	policy := extension.GetGangMatchPolicy(p.pg)
	if policy == "" {
		return defaultPolicy, nil
	}
	if policy != extension.GangMatchPolicyOnlyWaiting &&
		policy != extension.GangMatchPolicyWaitingAndRunning &&
		policy != extension.GangMatchPolicyOnceSatisfied {
		return defaultPolicy, fmt.Errorf("illegal gang match policy: %s", policy)
	}
	return policy, nil
}

func (p *PodGroupGangInfo) GetGangGroups() ([]string, error) {
	groups, err := util.StringToGangGroupSlice(p.GetRawAnnotation(extension.AnnotationGangGroups))
	if err != nil {
		return nil, err
	}
	return groups, nil
}

func (p *PodGroupGangInfo) GetNetworkTopologySpec() (*extension.NetworkTopologySpec, error) {
	return extension.GetNetworkTopologySpec(p.pg)
}

func (p *PodGroupGangInfo) GetCreationTimestamp() metav1.Time {
	if p.pg == nil {
		return metav1.Time{}
	}
	return p.pg.CreationTimestamp
}
