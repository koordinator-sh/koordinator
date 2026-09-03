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
	"time"

	corev1 "k8s.io/api/core/v1"
	schedulingv1alpha1 "k8s.io/api/scheduling/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
)

const (
	GangFromPodGroupCrd    string = "GangFromPodGroupCrd"
	GangFromPodAnnotation  string = "GangFromPodAnnotation"
	GangFromNativeWorkload string = "GangFromNativeWorkload"
)

// GangInfo defines the interface for different gang sources (PodGroup CRD, Native Workload, or Annotations).
type GangInfo interface {
	GetName() string
	GetNamespace() string
	GetMinMember() int32
	GetTotalMember() int32
	GetMode() string
	GetWaitTime() time.Duration
	GetMatchPolicy() string
	GetGangGroups() []string
	GetNetworkTopologySpec() *extension.NetworkTopologySpec
	GetCreationTimestamp() metav1.Time
	GetGangFrom() string
}

// AnnotationGangInfo implements GangInfo using Pod annotations.
type AnnotationGangInfo struct {
	pod  *corev1.Pod
	args *config.CoschedulingArgs
}

func NewAnnotationGangInfo(pod *corev1.Pod, args *config.CoschedulingArgs) *AnnotationGangInfo {
	return &AnnotationGangInfo{pod: pod, args: args}
}

func (a *AnnotationGangInfo) GetName() string {
	return util.GetGangNameByPod(a.pod)
}

func (a *AnnotationGangInfo) GetNamespace() string {
	return a.pod.Namespace
}

func (a *AnnotationGangInfo) GetMinMember() int32 {
	min, err := util.GetGangMinNumFromPod(a.pod)
	if err != nil {
		return -1
	}
	return int32(min)
}

func (a *AnnotationGangInfo) GetTotalMember() int32 {
	total, _ := extension.GetGangTotalNum(a.pod)
	return int32(total)
}

func (a *AnnotationGangInfo) GetMode() string {
	mode := a.pod.Annotations[extension.AnnotationGangMode]
	if mode != extension.GangModeStrict && mode != extension.GangModeNonStrict {
		return extension.GangModeStrict
	}
	return mode
}

func (a *AnnotationGangInfo) GetWaitTime() time.Duration {
	waitTime, _ := extension.GetGangWaitTime(a.pod)
	if waitTime <= 0 {
		return a.args.DefaultTimeout.Duration
	}
	return waitTime
}

func (a *AnnotationGangInfo) GetMatchPolicy() string {
	policy := extension.GetGangMatchPolicy(a.pod)
	if policy == "" {
		return a.args.DefaultMatchPolicy
	}
	return policy
}

func (a *AnnotationGangInfo) GetGangGroups() []string {
	groups, _ := util.StringToGangGroupSlice(a.pod.Annotations[extension.AnnotationGangGroups])
	if len(groups) == 0 {
		return []string{util.GetId(a.pod.Namespace, a.GetName())}
	}
	return groups
}

func (a *AnnotationGangInfo) GetNetworkTopologySpec() *extension.NetworkTopologySpec {
	spec, _ := extension.GetNetworkTopologySpec(a.pod)
	return spec
}

func (a *AnnotationGangInfo) GetCreationTimestamp() metav1.Time {
	return a.pod.CreationTimestamp
}

func (a *AnnotationGangInfo) GetGangFrom() string {
	return GangFromPodAnnotation
}

// PodGroupGangInfo implements GangInfo using the PodGroup CRD.
type PodGroupGangInfo struct {
	pg   *v1alpha1.PodGroup
	args *config.CoschedulingArgs
}

func NewPodGroupGangInfo(pg *v1alpha1.PodGroup, args *config.CoschedulingArgs) *PodGroupGangInfo {
	return &PodGroupGangInfo{pg: pg, args: args}
}

func (p *PodGroupGangInfo) GetName() string {
	return p.pg.Name
}

func (p *PodGroupGangInfo) GetNamespace() string {
	return p.pg.Namespace
}

func (p *PodGroupGangInfo) GetMinMember() int32 {
	return p.pg.Spec.MinMember
}

func (p *PodGroupGangInfo) GetTotalMember() int32 {
	total, _ := extension.GetGangTotalNum(p.pg)
	return int32(total)
}

func (p *PodGroupGangInfo) GetMode() string {
	mode := p.pg.Annotations[extension.AnnotationGangMode]
	if mode != extension.GangModeStrict && mode != extension.GangModeNonStrict {
		return extension.GangModeStrict
	}
	return mode
}

func (p *PodGroupGangInfo) GetWaitTime() time.Duration {
	return util.GetWaitTimeDuration(p.pg, p.args.DefaultTimeout.Duration)
}

func (p *PodGroupGangInfo) GetMatchPolicy() string {
	policy := extension.GetGangMatchPolicy(p.pg)
	if policy == "" {
		return p.args.DefaultMatchPolicy
	}
	return policy
}

func (p *PodGroupGangInfo) GetGangGroups() []string {
	groups, _ := util.StringToGangGroupSlice(p.pg.Annotations[extension.AnnotationGangGroups])
	if len(groups) == 0 {
		return []string{util.GetId(p.pg.Namespace, p.pg.Name)}
	}
	return groups
}

func (p *PodGroupGangInfo) GetNetworkTopologySpec() *extension.NetworkTopologySpec {
	spec, _ := extension.GetNetworkTopologySpec(p.pg)
	return spec
}

func (p *PodGroupGangInfo) GetCreationTimestamp() metav1.Time {
	return p.pg.CreationTimestamp
}

func (p *PodGroupGangInfo) GetGangFrom() string {
	return GangFromPodGroupCrd
}

// WorkloadGangInfo implements GangInfo using the native Kubernetes Workload API.
type WorkloadGangInfo struct {
	workload     *schedulingv1alpha1.Workload
	podGroupName string
	args         *config.CoschedulingArgs
}

func NewWorkloadGangInfo(workload *schedulingv1alpha1.Workload, podGroupName string, args *config.CoschedulingArgs) *WorkloadGangInfo {
	return &WorkloadGangInfo{workload: workload, podGroupName: podGroupName, args: args}
}

func (w *WorkloadGangInfo) GetName() string {
	return w.podGroupName
}

func (w *WorkloadGangInfo) GetNamespace() string {
	return w.workload.Namespace
}

func (w *WorkloadGangInfo) GetMinMember() int32 {
	for _, pg := range w.workload.Spec.PodGroups {
		if pg.Name == w.podGroupName && pg.Policy.Gang != nil {
			return pg.Policy.Gang.MinCount
		}
	}
	return 0
}

func (w *WorkloadGangInfo) GetTotalMember() int32 {
	return w.GetMinMember()
}

func (w *WorkloadGangInfo) GetMode() string {
	return extension.GangModeStrict
}

func (w *WorkloadGangInfo) GetWaitTime() time.Duration {
	return w.args.DefaultTimeout.Duration
}

func (w *WorkloadGangInfo) GetMatchPolicy() string {
	return w.args.DefaultMatchPolicy
}

func (w *WorkloadGangInfo) GetGangGroups() []string {
	var groups []string
	for _, pg := range w.workload.Spec.PodGroups {
		groups = append(groups, util.GetId(w.workload.Namespace, pg.Name))
	}
	return groups
}

func (w *WorkloadGangInfo) GetNetworkTopologySpec() *extension.NetworkTopologySpec {
	spec, _ := extension.GetNetworkTopologySpec(w.workload)
	return spec
}

func (w *WorkloadGangInfo) GetCreationTimestamp() metav1.Time {
	return w.workload.CreationTimestamp
}

func (w *WorkloadGangInfo) GetGangFrom() string {
	return GangFromNativeWorkload
}
