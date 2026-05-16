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

package loadaware

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func TestIsSchedulableAfterPodDeletion(t *testing.T) {
	p := &Plugin{}
	logger := klog.Background()

	tests := []struct {
		name       string
		deletedPod *corev1.Pod
		want       fwktype.QueueingHint
	}{
		{
			name: "deleted pod not assigned",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
			},
			want: fwktype.QueueSkip,
		},
		{
			name: "deleted pod assigned to node",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
				Spec:       corev1.PodSpec{NodeName: "node1"},
			},
			want: fwktype.Queue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := p.isSchedulableAfterPodDeletion(logger, nil, tt.deletedPod, nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsSchedulableAfterNodeMetricChanged(t *testing.T) {
	p := &Plugin{}
	logger := klog.Background()

	tests := []struct {
		name      string
		oldMetric *slov1alpha1.NodeMetric
		newMetric *slov1alpha1.NodeMetric
		want      fwktype.QueueingHint
	}{
		{
			name:      "add NodeMetric",
			oldMetric: nil,
			newMetric: &slov1alpha1.NodeMetric{},
			want:      fwktype.Queue,
		},
		{
			name: "usage decreased",
			oldMetric: &slov1alpha1.NodeMetric{
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
							},
						},
					},
				},
			},
			newMetric: &slov1alpha1.NodeMetric{
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU: *resource.NewQuantity(5, resource.DecimalSI),
							},
						},
					},
				},
			},
			want: fwktype.Queue,
		},
		{
			name: "usage increased",
			oldMetric: &slov1alpha1.NodeMetric{
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU: *resource.NewQuantity(5, resource.DecimalSI),
							},
						},
					},
				},
			},
			newMetric: &slov1alpha1.NodeMetric{
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: slov1alpha1.ResourceMap{
							ResourceList: corev1.ResourceList{
								corev1.ResourceCPU: *resource.NewQuantity(10, resource.DecimalSI),
							},
						},
					},
				},
			},
			want: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := p.isSchedulableAfterNodeMetricChanged(logger, nil, tt.oldMetric, tt.newMetric)
			assert.Equal(t, tt.want, got)
		})
	}
}
