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

package nodenumaresource

import (
	"testing"

	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
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
			name: "deleted pod has no NUMA status",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod1"},
			},
			want: fwktype.QueueSkip,
		},
		{
			name: "deleted pod has NUMA status",
			deletedPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod1",
					Annotations: map[string]string{
						"scheduling.koordinator.sh/resource-status": `{"numaNodeResources": [{"node": 0, "resources": {"cpu": "1"}}]}`,
					},
				},
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

func TestIsSchedulableAfterNRTChanged(t *testing.T) {
	p := &Plugin{}
	logger := klog.Background()

	tests := []struct {
		name   string
		oldNRT *nrtv1alpha1.NodeResourceTopology
		newNRT *nrtv1alpha1.NodeResourceTopology
		want   fwktype.QueueingHint
	}{
		{
			name:   "add NRT",
			oldNRT: nil,
			newNRT: &nrtv1alpha1.NodeResourceTopology{},
			want:   fwktype.Queue,
		},
		{
			name: "resources increased",
			oldNRT: &nrtv1alpha1.NodeResourceTopology{
				Zones: nrtv1alpha1.ZoneList{
					{
						Name: "node-0",
						Resources: nrtv1alpha1.ResourceInfoList{
							{Name: "cpu", Available: *resource.NewQuantity(1, resource.DecimalSI)},
						},
					},
				},
			},
			newNRT: &nrtv1alpha1.NodeResourceTopology{
				Zones: nrtv1alpha1.ZoneList{
					{
						Name: "node-0",
						Resources: nrtv1alpha1.ResourceInfoList{
							{Name: "cpu", Available: *resource.NewQuantity(2, resource.DecimalSI)},
						},
					},
				},
			},
			want: fwktype.Queue,
		},
		{
			name: "resources decreased",
			oldNRT: &nrtv1alpha1.NodeResourceTopology{
				Zones: nrtv1alpha1.ZoneList{
					{
						Name: "node-0",
						Resources: nrtv1alpha1.ResourceInfoList{
							{Name: "cpu", Available: *resource.NewQuantity(2, resource.DecimalSI)},
						},
					},
				},
			},
			newNRT: &nrtv1alpha1.NodeResourceTopology{
				Zones: nrtv1alpha1.ZoneList{
					{
						Name: "node-0",
						Resources: nrtv1alpha1.ResourceInfoList{
							{Name: "cpu", Available: *resource.NewQuantity(1, resource.DecimalSI)},
						},
					},
				},
			},
			want: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := p.isSchedulableAfterNRTChanged(logger, nil, tt.oldNRT, tt.newNRT)
			assert.Equal(t, tt.want, got)
		})
	}
}
