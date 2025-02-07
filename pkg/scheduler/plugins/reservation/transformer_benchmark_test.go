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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodeaffinity"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func BenchmarkBeforePrefilterWithMatchedPod(b *testing.B) {
	var nodes []*corev1.Node
	var pods []*corev1.Pod
	for i := 0; i < 1024; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
				Labels: map[string]string{
					"zone": fmt.Sprintf("zone-%d", i%2),
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("40"),
					corev1.ResourceMemory: resource.MustParse("80Gi"),
				},
			},
		})

		for j := 0; j < 8; j++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("test-pod-%d-%d", i, j),
					Namespace: "default",
					UID:       types.UID(fmt.Sprintf("%d-%d", i, j)),
				},
				Spec: corev1.PodSpec{
					NodeName: fmt.Sprintf("node-%d", i),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			}
			pods = append(pods, pod)
		}
	}
	suit := newPluginTestSuitWith(b, pods, nodes)
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
		pl.reservationCache.UpdateReservation(reservation)
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
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "zone",
										Operator: corev1.NodeSelectorOpIn,
										Values: []string{
											"zone-0",
										},
									},
								},
							},
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

		preFilterResult, status := pl.PreFilter(context.TODO(), cycleState, pod)
		assert.True(b, status.IsSuccess())

		affinityPl := &nodeaffinity.NodeAffinity{}
		preFilterResult1, status := affinityPl.PreFilter(context.TODO(), cycleState, pod)
		assert.True(b, status.IsSuccess())
		preFilterResult = preFilterResult.Merge(preFilterResult1)

		status = pl.AfterPreFilter(context.TODO(), cycleState, pod, preFilterResult)
		assert.True(b, status.IsSuccess())

		b.StopTimer()
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
		b.StartTimer()
	}
}

func BenchmarkBeforePrefilterWithUnmatchedPod(b *testing.B) {
	var nodes []*corev1.Node
	var pods []*corev1.Pod
	for i := 0; i < 1024; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
				Labels: map[string]string{
					"zone": fmt.Sprintf("zone-%d", i%2),
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("40"),
					corev1.ResourceMemory: resource.MustParse("80Gi"),
				},
			},
		})

		for j := 0; j < 8; j++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("test-pod-%d-%d", i, j),
					Namespace: "default",
					UID:       types.UID(fmt.Sprintf("%d-%d", i, j)),
				},
				Spec: corev1.PodSpec{
					NodeName: fmt.Sprintf("node-%d", i),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			}
			pods = append(pods, pod)
		}
	}
	suit := newPluginTestSuitWith(b, pods, nodes)
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
		pl.reservationCache.UpdateReservation(reservation)
		nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(node.Name)
		assert.NoError(b, err)
		reservePod := reservationutil.NewReservePod(reservation)
		nodeInfo.AddPod(reservePod)
		assert.NoError(b, pl.handle.Scheduler().GetCache().AddPod(klog.Background(), reservePod))

		for j := 0; j < 4; j++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Name:      fmt.Sprintf("pod-%s-%d", reservation.Name, j),
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: node.Name,
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			}
			pl.reservationCache.UpdatePod(reservation.UID, nil, pod)
			nodeInfo.AddPod(pod)
			assert.NoError(b, pl.handle.Scheduler().GetCache().AddPod(klog.Background(), pod))
		}
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
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "zone",
										Operator: corev1.NodeSelectorOpIn,
										Values: []string{
											"zone-0",
										},
									},
								},
							},
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

		preFilterResult, status := pl.PreFilter(context.TODO(), cycleState, pod)
		assert.True(b, status.IsSuccess())

		affinityPl := &nodeaffinity.NodeAffinity{}
		preFilterResult1, status := affinityPl.PreFilter(context.TODO(), cycleState, pod)
		assert.True(b, status.IsSuccess())
		preFilterResult = preFilterResult.Merge(preFilterResult1)

		status = pl.AfterPreFilter(context.TODO(), cycleState, pod, preFilterResult)
		assert.True(b, status.IsSuccess())
	}
}

func BenchmarkBeforePrefilterWithMatchedPodEnableLazyReservationRestore(b *testing.B) {
	var nodes []*corev1.Node
	var pods []*corev1.Pod
	for i := 0; i < 1024; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
				Labels: map[string]string{
					"zone": fmt.Sprintf("zone-%d", i%2),
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("40"),
					corev1.ResourceMemory: resource.MustParse("80Gi"),
				},
			},
		})

		for j := 0; j < 8; j++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("test-pod-%d-%d", i, j),
					Namespace: "default",
					UID:       types.UID(fmt.Sprintf("%d-%d", i, j)),
				},
				Spec: corev1.PodSpec{
					NodeName: fmt.Sprintf("node-%d", i),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			}
			pods = append(pods, pod)
		}
	}
	suit := newPluginTestSuitWith(b, pods, nodes)
	p, err := suit.pluginFactory()
	assert.NoError(b, err)
	pl := p.(*Plugin)
	pl.enableLazyReservationRestore = true

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
		pl.reservationCache.UpdateReservation(reservation)
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
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "zone",
										Operator: corev1.NodeSelectorOpIn,
										Values: []string{
											"zone-0",
										},
									},
								},
							},
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

		preFilterResult, status := pl.PreFilter(context.TODO(), cycleState, pod)
		assert.True(b, status.IsSuccess())

		affinityPl := &nodeaffinity.NodeAffinity{}
		preFilterResult1, status := affinityPl.PreFilter(context.TODO(), cycleState, pod)
		assert.True(b, status.IsSuccess())
		preFilterResult = preFilterResult.Merge(preFilterResult1)

		status = pl.AfterPreFilter(context.TODO(), cycleState, pod, preFilterResult)
		assert.True(b, status.IsSuccess())

		b.StopTimer()
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
		b.StartTimer()
	}
}

func BenchmarkBeforePrefilterWithUnmatchedPodEnableLazyReservationRestore(b *testing.B) {
	var nodes []*corev1.Node
	var pods []*corev1.Pod
	for i := 0; i < 1024; i++ {
		nodes = append(nodes, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
				Labels: map[string]string{
					"zone": fmt.Sprintf("zone-%d", i%2),
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("40"),
					corev1.ResourceMemory: resource.MustParse("80Gi"),
				},
			},
		})

		for j := 0; j < 8; j++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("test-pod-%d-%d", i, j),
					Namespace: "default",
					UID:       types.UID(fmt.Sprintf("%d-%d", i, j)),
				},
				Spec: corev1.PodSpec{
					NodeName: fmt.Sprintf("node-%d", i),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			}
			pods = append(pods, pod)
		}
	}
	suit := newPluginTestSuitWith(b, pods, nodes)
	p, err := suit.pluginFactory()
	assert.NoError(b, err)
	pl := p.(*Plugin)
	pl.enableLazyReservationRestore = true

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
		pl.reservationCache.UpdateReservation(reservation)
		nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(node.Name)
		assert.NoError(b, err)
		reservePod := reservationutil.NewReservePod(reservation)
		nodeInfo.AddPod(reservePod)
		assert.NoError(b, pl.handle.Scheduler().GetCache().AddPod(klog.Background(), reservePod))

		for j := 0; j < 4; j++ {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID:       uuid.NewUUID(),
					Name:      fmt.Sprintf("pod-%s-%d", reservation.Name, j),
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					NodeName: node.Name,
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			}
			pl.reservationCache.UpdatePod(reservation.UID, nil, pod)
			nodeInfo.AddPod(pod)
			assert.NoError(b, pl.handle.Scheduler().GetCache().AddPod(klog.Background(), pod))
		}
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
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "zone",
										Operator: corev1.NodeSelectorOpIn,
										Values: []string{
											"zone-0",
										},
									},
								},
							},
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

		preFilterResult, status := pl.PreFilter(context.TODO(), cycleState, pod)
		assert.True(b, status.IsSuccess())

		affinityPl := &nodeaffinity.NodeAffinity{}
		preFilterResult1, status := affinityPl.PreFilter(context.TODO(), cycleState, pod)
		assert.True(b, status.IsSuccess())
		preFilterResult = preFilterResult.Merge(preFilterResult1)

		status = pl.AfterPreFilter(context.TODO(), cycleState, pod, preFilterResult)
		assert.True(b, status.IsSuccess())
	}
}
