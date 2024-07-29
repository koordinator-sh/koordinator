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

package reservation

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func BenchmarkBeforePrefilterWithMatchedPod(b *testing.B) {
	var nodes []*corev1.Node
	for i := 0; i < 1024; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("64Gi"),
				},
			},
		})
	}
	suit := newPluginTestSuitWith(b, nil, nodes)
	p, err := suit.pluginFactory()
	assert.NoError(b, err)
	pl := p.(*Plugin)

	reservePods := map[string]*corev1.Pod{}
	for i, node := range nodes {
		reservation := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				UID:  uuid.NewUUID(),
				Name: fmt.Sprintf("reservation-%d", i),
				Labels: map[string]string{
					"test-reservation": "true",
				},
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				AllocateOnce: pointer.Bool(false),
				Owners: []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"test-reservation": "true",
							},
						},
					},
				},
				Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("32"),
										corev1.ResourceMemory: resource.MustParse("64Gi"),
									},
								},
							},
						},
					},
				},
			},
			Status: schedulingv1alpha1.ReservationStatus{
				Phase:    schedulingv1alpha1.ReservationAvailable,
				NodeName: node.Name,
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("64Gi"),
				},
			},
		}
		pl.reservationCache.updateReservation(reservation)
		nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(node.Name)
		assert.NoError(b, err)
		reservePod := reservationutil.NewReservePod(reservation)
		reservePods[string(reservePod.UID)] = reservePod
		nodeInfo.AddPod(reservePod)
		assert.NoError(b, pl.handle.Scheduler().GetCache().AddPod(klog.Background(), reservePod))
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Labels: map[string]string{
				"test-reservation": "true",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("32"),
							corev1.ResourceMemory: resource.MustParse("64Gi"),
						},
					},
				},
			},
		},
	}
	err = apiext.SetReservationAffinity(pod, &apiext.ReservationAffinity{
		ReservationSelector: map[string]string{
			"test-reservation": "true",
		},
	})
	assert.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, pod)
		assert.True(b, restored)
		assert.True(b, status.IsSuccess())

		sd := getStateData(cycleState)
		for _, v := range sd.nodeReservationStates {
			nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(v.nodeName)
			assert.NoError(b, err)
			for _, ri := range v.matchedOrIgnored {
				p := reservePods[string(ri.UID())]
				if p != nil {
					nodeInfo.AddPod(p)
				}
			}
		}
	}
}

func BenchmarkBeforePrefilterWithUnmatchedPod(b *testing.B) {
	var nodes []*corev1.Node
	for i := 0; i < 1024; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("64Gi"),
				},
			},
		})
	}
	suit := newPluginTestSuitWith(b, nil, nodes)
	p, err := suit.pluginFactory()
	assert.NoError(b, err)
	pl := p.(*Plugin)

	for i, node := range nodes {
		reservation := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				UID:  uuid.NewUUID(),
				Name: fmt.Sprintf("reservation-%d", i),
				Labels: map[string]string{
					"test-reservation": "true",
				},
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				AllocateOnce: pointer.Bool(false),
				Owners: []schedulingv1alpha1.ReservationOwner{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"test-reservation": "true",
							},
						},
					},
				},
				Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("32"),
										corev1.ResourceMemory: resource.MustParse("64Gi"),
									},
								},
							},
						},
					},
				},
			},
			Status: schedulingv1alpha1.ReservationStatus{
				Phase:    schedulingv1alpha1.ReservationAvailable,
				NodeName: node.Name,
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("64Gi"),
				},
			},
		}
		pl.reservationCache.updateReservation(reservation)
		assignedPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				UID:       uuid.NewUUID(),
				Name:      fmt.Sprintf("pod-%s", reservation.Name),
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				NodeName: node.Name,
				Containers: []corev1.Container{
					{
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("4"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
						},
					},
				},
			},
		}
		pl.reservationCache.updatePod(reservation.UID, nil, assignedPod)
		nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(node.Name)
		assert.NoError(b, err)
		reservePod := reservationutil.NewReservePod(reservation)
		nodeInfo.AddPod(reservePod)
		nodeInfo.AddPod(assignedPod)
		assert.NoError(b, pl.handle.Scheduler().GetCache().AddPod(klog.Background(), reservePod))
		assert.NoError(b, pl.handle.Scheduler().GetCache().AddPod(klog.Background(), assignedPod))
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("32"),
							corev1.ResourceMemory: resource.MustParse("64Gi"),
						},
					},
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cycleState := framework.NewCycleState()
		_, restored, status := pl.BeforePreFilter(context.TODO(), cycleState, pod)
		assert.True(b, restored)
		assert.True(b, status.IsSuccess())
	}
}
