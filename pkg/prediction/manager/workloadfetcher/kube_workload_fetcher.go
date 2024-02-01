/*
 Copyright 2024 The Koordinator Authors.

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

package workloadfetcher

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type KubeWorkloadFetcher struct {
	client.Client
}

// GetPodsByWorkload returns the pods and template of the workload
func (f *KubeWorkloadFetcher) GetPodsByWorkload(workload *ControllerWorkloadKey) (*ControllerWorkloadStatus, error) {
	panic("implement me")
}

// GetPodsBySelector returns the pods by selector
func (f *KubeWorkloadFetcher) GetPodsBySelector(selector labels.Selector) ([]*corev1.Pod, error) {
	panic("implement me")
}

// ControllerWorkloadKey identifies a k8s workload
type ControllerWorkloadKey struct {
	ApiVersion string
	Kind       string
	Name       string
	Namespace  string
}

// ControllerWorkloadStatus contains the pod template and all pods of a k8s workload
type ControllerWorkloadStatus struct {
	Pods        []*corev1.Pod
	PodTemplate corev1.PodTemplateSpec
}
