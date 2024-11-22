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

package statesinformer

import (
	"fmt"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

type PodMeta struct {
	Pod              *corev1.Pod
	CgroupDir        string
	ContainerTaskIds map[string][]int32
}

// DeepCopyContainerTaskIds creates a deep copy of ContainerTaskIds
func DeepCopyContainerTaskIds(in map[string][]int32) map[string][]int32 {
	if in == nil {
		return nil
	}

	out := make(map[string][]int32, len(in))

	for key, value := range in {
		if value == nil {
			out[key] = nil
		} else {
			copyValue := make([]int32, len(value))
			copy(copyValue, value)
			out[key] = copyValue
		}
	}

	return out
}

func (in *PodMeta) DeepCopy() *PodMeta {
	out := new(PodMeta)
	out.Pod = in.Pod.DeepCopy()
	out.CgroupDir = in.CgroupDir
	out.ContainerTaskIds = DeepCopyContainerTaskIds(in.ContainerTaskIds)
	return out
}

func (in *PodMeta) Key() string {
	if in == nil || in.Pod == nil {
		return ""
	}
	return util.GetPodKey(in.Pod)
}

func (in *PodMeta) IsRunningOrPending() bool {
	if in == nil || in.Pod == nil {
		return false
	}
	phase := in.Pod.Status.Phase
	return phase == corev1.PodRunning || phase == corev1.PodPending
}

type RegisterType int64

const (
	RegisterTypeNodeSLOSpec RegisterType = iota
	RegisterTypeAllPods
	RegisterTypeNodeTopology
	RegisterTypeNodeMetadata
)

func (r RegisterType) String() string {
	switch r {
	case RegisterTypeNodeSLOSpec:
		return "RegisterTypeNodeSLOSpec"
	case RegisterTypeAllPods:
		return "RegisterTypeAllPods"
	case RegisterTypeNodeTopology:
		return "RegisterTypeNodeTopology"
	case RegisterTypeNodeMetadata:
		return "RegisterNodeMetadata"
	default:
		return "RegisterTypeUnknown"
	}
}

type CallbackTarget struct {
	Pods             []*PodMeta
	HostApplications []slov1alpha1.HostApplicationSpec
}

func (t *CallbackTarget) String() string {
	if t == nil {
		return "target: nil"
	}
	return fmt.Sprintf("target: pods num %v, host apps num %v", len(t.Pods), len(t.HostApplications))
}

type UpdateCbFn func(t RegisterType, obj interface{}, target *CallbackTarget)

type StatesInformer interface {
	Run(stopCh <-chan struct{}) error
	HasSynced() bool

	GetNode() *corev1.Node
	GetNodeSLO() *slov1alpha1.NodeSLO
	GetNodeMetricSpec() *slov1alpha1.NodeMetricSpec

	GetAllPods() []*PodMeta

	GetNodeTopo() *topov1alpha1.NodeResourceTopology

	GetVolumeName(pvcNamespace, pvcName string) string

	RegisterCallbacks(objType RegisterType, name, description string, callbackFn UpdateCbFn)
}
