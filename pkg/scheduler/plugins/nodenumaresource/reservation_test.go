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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// TestPlugin_RestoreReservationPreAllocation tests that a pre-allocation reservation can
// restore resources from pre-allocatable pods with CPUSet and NUMA resources
func TestPlugin_RestoreReservationPreAllocation(t *testing.T) {
	tests := []struct {
		name                 string
		preAllocatablePods   []*corev1.Pod
		preAllocatableCPUs   map[types.UID]cpuset.CPUSet
		preAllocatableNUMA   map[types.UID]map[int]corev1.ResourceList
		expectedRestoredUIDs int
		expectedMergedCPUs   cpuset.CPUSet
	}{
		{
			name: "restore single pod with CPUSet",
			preAllocatablePods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID:       "pod-1",
						Name:      "test-pod-1",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
				},
			},
			preAllocatableCPUs: map[types.UID]cpuset.CPUSet{
				"pod-1": cpuset.NewCPUSet(0, 1, 2, 3),
			},
			expectedRestoredUIDs: 1,
			expectedMergedCPUs:   cpuset.NewCPUSet(0, 1, 2, 3),
		},
		{
			name: "restore multiple pods with CPUSet and NUMA",
			preAllocatablePods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						UID:       "pod-1",
						Name:      "test-pod-1",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						UID:       "pod-2",
						Name:      "test-pod-2",
						Namespace: "default",
					},
					Spec: corev1.PodSpec{
						NodeName: "test-node",
					},
				},
			},
			preAllocatableCPUs: map[types.UID]cpuset.CPUSet{
				"pod-1": cpuset.NewCPUSet(0, 1),
				"pod-2": cpuset.NewCPUSet(2, 3),
			},
			preAllocatableNUMA: map[types.UID]map[int]corev1.ResourceList{
				"pod-1": {
					0: {
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
				"pod-2": {
					0: {
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
			expectedRestoredUIDs: 2,
			expectedMergedCPUs:   cpuset.NewCPUSet(0, 1, 2, 3),
		},
		{
			name:                 "no pre-allocatable pods",
			preAllocatablePods:   []*corev1.Pod{},
			expectedRestoredUIDs: 0,
			expectedMergedCPUs:   cpuset.NewCPUSet(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
			}
			suit := newPluginTestSuit(t, nil, []*corev1.Node{node})
			p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
			assert.NoError(t, err)
			assert.NotNil(t, p)

			pl := p.(*Plugin)
			suit.start()

			// Setup CPU topology
			pl.topologyOptionsManager.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
				options.CPUTopology = buildCPUTopologyForTest(1, 2, 4, 2)
				options.NUMANodeResources = []NUMANodeResource{
					{
						Node: 0,
						Resources: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("8"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
					},
				}
			})

			// Register pre-allocatable pods with their allocated resources
			for _, pod := range tt.preAllocatablePods {
				cpus, hasCPUs := tt.preAllocatableCPUs[pod.UID]
				numa, hasNUMA := tt.preAllocatableNUMA[pod.UID]

				if hasCPUs || hasNUMA {
					allocation := &PodAllocation{
						UID:                pod.UID,
						CPUSet:             cpus,
						CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
					}
					if hasNUMA {
						for nodeID, res := range numa {
							allocation.NUMANodeResources = append(allocation.NUMANodeResources, NUMANodeResource{
								Node:      nodeID,
								Resources: res,
							})
						}
					}
					pl.resourceManager.Update("test-node", allocation)
				}
			}

			// Create reservation
			reservation := &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					UID:  uuid.NewUUID(),
					Name: "test-reservation",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
									},
								},
							},
						},
					},
				},
				Status: schedulingv1alpha1.ReservationStatus{
					Phase:    schedulingv1alpha1.ReservationPending,
					NodeName: "",
				},
			}

			reservation.Spec.PreAllocation = true
			rInfo := frameworkext.NewReservationInfo(reservation)

			nodeInfo := framework.NewNodeInfo()
			nodeInfo.SetNode(node)

			cycleState := framework.NewCycleState()

			// PreRestoreReservationPreAllocation
			status := pl.PreRestoreReservationPreAllocation(context.TODO(), cycleState, rInfo)
			assert.True(t, status.IsSuccess(), "PreRestoreReservationPreAllocation should succeed")

			// RestoreReservationPreAllocation
			nodeState, status := pl.RestoreReservationPreAllocation(context.TODO(), cycleState, rInfo, tt.preAllocatablePods, nodeInfo)
			assert.True(t, status.IsSuccess(), "RestoreReservationPreAllocation should succeed")

			if tt.expectedRestoredUIDs > 0 {
				assert.NotNil(t, nodeState, "nodeState should not be nil when pods are restored")
				state := nodeState.(*nodeReservationRestoreStateData)
				assert.Equal(t, tt.expectedRestoredUIDs, len(state.matched), "restored pod count should match")
				assert.Equal(t, tt.expectedMergedCPUs, state.mergedMatchedRemainCPUs, "merged CPUSet should match")

				// Verify each pre-allocatable pod is properly restored
				for _, pod := range tt.preAllocatablePods {
					if cpus, ok := tt.preAllocatableCPUs[pod.UID]; ok {
						alloc, found := state.matched[pod.UID]
						assert.True(t, found, "pod %s should be in matched allocations", pod.UID)
						assert.Equal(t, cpus, alloc.remainedCPUs, "remained CPUs should match allocated CPUs")
						assert.Equal(t, cpus, alloc.allocatableCPUs, "allocatable CPUs should match allocated CPUs")
					}
				}
			} else {
				// When no pre-allocatable pods, nodeState can be nil
				if nodeState != nil {
					state := nodeState.(*nodeReservationRestoreStateData)
					assert.Equal(t, 0, len(state.matched), "no pods should be restored")
				}
			}
		})
	}
}

// TestPlugin_AllocateReservationPreAllocationFromPreAllocatablePods tests that
// a pre-allocation reservation can allocate resources from pre-allocatable pods
// and the final reservation includes superset CPUSet of the preempted pod
func TestPlugin_AllocateReservationPreAllocationFromPreAllocatablePods(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	// Pre-allocatable pod with 4 cores (0-3)
	preAllocatablePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "pre-pod-1",
			Name:      "pre-allocatable-pod",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
		},
		Spec: corev1.PodSpec{
			Priority: ptr.To[int32](extension.PriorityProdValueMax),
			NodeName: "test-node",
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

	suit := newPluginTestSuit(t, []*corev1.Pod{preAllocatablePod}, []*corev1.Node{node})
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	assert.NotNil(t, p)

	pl := p.(*Plugin)
	suit.start()

	// Setup CPU topology: 1 NUMA node, 2 sockets, 4 cores per socket, 2 threads per core
	// Total: 16 logical CPUs (0-15)
	pl.topologyOptionsManager.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(1, 2, 4, 2)
		options.NUMANodeResources = []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
		}
	})

	// Allocate CPUSet 0-3 to pre-allocatable pod
	preAllocatableCPUSet := cpuset.NewCPUSet(0, 1, 2, 3)
	pl.resourceManager.Update("test-node", &PodAllocation{
		UID:                preAllocatablePod.UID,
		CPUSet:             preAllocatableCPUSet,
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
		NUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
	})

	// Create a pre-allocation reservation that wants 8 cores
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-prealloc-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			PreAllocation:  true,
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("8"),
									corev1.ResourceMemory: resource.MustParse("16Gi"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationPending,
			NodeName: "",
		},
	}

	rInfo := frameworkext.NewReservationInfo(reservation)

	// Create reserve pod for the reservation
	reservePod := reservationutil.NewReservePod(reservation)
	// The NewReservePod will copy annotations from template, but let's ensure it's there
	if reservePod.Annotations == nil {
		reservePod.Annotations = make(map[string]string)
	}
	if _, ok := reservePod.Annotations[extension.AnnotationResourceSpec]; !ok {
		reservePod.Annotations[extension.AnnotationResourceSpec] = `{"preferredCPUBindPolicy": "FullPCPUs"}`
	}

	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	cycleState := framework.NewCycleState()

	// Step 1: PreFilter for reservation
	_, status := pl.PreFilter(context.TODO(), cycleState, reservePod)
	assert.True(t, status.IsSuccess(), "PreFilter should succeed")

	// Step 2: PreRestoreReservationPreAllocation
	status = pl.PreRestoreReservationPreAllocation(context.TODO(), cycleState, rInfo)
	assert.True(t, status.IsSuccess(), "PreRestoreReservationPreAllocation should succeed")

	// Step 3: RestoreReservationPreAllocation with the pre-allocatable pod
	nodeState, status := pl.RestoreReservationPreAllocation(context.TODO(), cycleState, rInfo, []*corev1.Pod{preAllocatablePod}, nodeInfo)
	assert.True(t, status.IsSuccess(), "RestoreReservationPreAllocation should succeed")
	assert.NotNil(t, nodeState, "nodeState should not be nil")

	state := nodeState.(*nodeReservationRestoreStateData)
	assert.Equal(t, 1, len(state.matched), "should have one matched pre-allocatable pod")
	assert.Equal(t, preAllocatableCPUSet, state.mergedMatchedRemainCPUs, "merged CPUs should include pre-allocatable pod's CPUs")

	// Step 4: Nominate the pre-allocatable pod for preemption
	fakeNominator := pl.handle.(frameworkext.ExtendedHandle).GetReservationNominator().(*frameworkext.FakeNominator)
	fakeNominator.AddNominatedPreAllocation(rInfo, "test-node", preAllocatablePod)

	// Step 5: Filter - should succeed as we have enough resources
	status = pl.Filter(context.TODO(), cycleState, reservePod, nodeInfo)
	assert.True(t, status.IsSuccess(), "Filter should succeed with pre-allocatable pod resources")

	// Step 6: Reserve - allocate resources
	status = pl.Reserve(context.TODO(), cycleState, reservePod, "test-node")
	if !status.IsSuccess() {
		t.Logf("Reserve failed: %v", status.Message())
	}
	assert.True(t, status.IsSuccess(), "Reserve should succeed")

	// Step 7: Verify allocation
	preFilterState, status := getPreFilterState(cycleState)
	assert.True(t, status.IsSuccess())
	if preFilterState.allocation == nil {
		t.Logf("Allocation is nil, preFilterState: %+v", preFilterState)
	}
	assert.NotNil(t, preFilterState.allocation, "allocation should be set")

	allocatedCPUs := preFilterState.allocation.CPUSet
	t.Logf("Pre-allocatable pod CPUs: %s", preAllocatableCPUSet.String())
	t.Logf("Reservation allocated CPUs: %s", allocatedCPUs.String())

	// Verify: reservation's CPUSet should be a superset of pre-allocatable pod's CPUSet
	assert.True(t, preAllocatableCPUSet.IsSubsetOf(allocatedCPUs),
		"pre-allocatable pod CPUSet %s should be subset of reservation CPUSet %s",
		preAllocatableCPUSet.String(), allocatedCPUs.String())

	// Verify: reservation should have 8 cores
	assert.Equal(t, 8, allocatedCPUs.Size(), "reservation should have 8 cores")

	// Step 8: PreBind - write CPUSet to reservation
	status = pl.PreBindReservation(context.TODO(), cycleState, reservation, "test-node")
	assert.True(t, status.IsSuccess(), "PreBindReservation should succeed")

	// Verify ResourceStatus is set on reservation
	resourceStatus, err := extension.GetResourceStatus(reservation.Annotations)
	assert.NoError(t, err)
	assert.NotNil(t, resourceStatus)
	assert.Equal(t, allocatedCPUs.String(), resourceStatus.CPUSet, "CPUSet should be written to reservation")

	t.Logf("✅ Test passed: Reservation successfully allocated CPUSet %s (superset of pod's %s)",
		allocatedCPUs.String(), preAllocatableCPUSet.String())
}

// TestPlugin_PreAllocationWithMultiplePreAllocatablePods tests pre-allocation
// with multiple pre-allocatable pods on the same node
func TestPlugin_PreAllocationWithMultiplePreAllocatablePods(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	// Two pre-allocatable pods, each with 4 cores
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "pre-pod-1",
			Name:      "pre-allocatable-pod-1",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
		},
		Spec: corev1.PodSpec{
			Priority: ptr.To[int32](extension.PriorityProdValueMax),
			NodeName: "test-node",
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

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "pre-pod-2",
			Name:      "pre-allocatable-pod-2",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
		},
		Spec: corev1.PodSpec{
			Priority: ptr.To[int32](extension.PriorityProdValueMax),
			NodeName: "test-node",
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

	suit := newPluginTestSuit(t, []*corev1.Pod{pod1, pod2}, []*corev1.Node{node})
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	assert.NotNil(t, p)

	pl := p.(*Plugin)
	suit.start()

	// Setup CPU topology
	pl.topologyOptionsManager.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(1, 2, 4, 2)
		options.NUMANodeResources = []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
		}
	})

	// Allocate CPUs to pre-allocatable pods
	pod1CPUs := cpuset.NewCPUSet(0, 1, 2, 3)
	pod2CPUs := cpuset.NewCPUSet(4, 5, 6, 7)

	pl.resourceManager.Update("test-node", &PodAllocation{
		UID:                pod1.UID,
		CPUSet:             pod1CPUs,
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
		NUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
	})

	pl.resourceManager.Update("test-node", &PodAllocation{
		UID:                pod2.UID,
		CPUSet:             pod2CPUs,
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
		NUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
	})

	// Create reservation
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-prealloc-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			PreAllocation:  true,
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("8"),
									corev1.ResourceMemory: resource.MustParse("16Gi"),
								},
							},
						},
					},
				},
			},
		},
	}

	rInfo := frameworkext.NewReservationInfo(reservation)

	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	cycleState := framework.NewCycleState()

	// RestoreReservationPreAllocation with both pods
	status := pl.PreRestoreReservationPreAllocation(context.TODO(), cycleState, rInfo)
	assert.True(t, status.IsSuccess())

	nodeState, status := pl.RestoreReservationPreAllocation(context.TODO(), cycleState, rInfo, []*corev1.Pod{pod1, pod2}, nodeInfo)
	assert.True(t, status.IsSuccess())
	assert.NotNil(t, nodeState)

	state := nodeState.(*nodeReservationRestoreStateData)
	assert.Equal(t, 2, len(state.matched), "should have two matched pre-allocatable pods")

	// Verify merged CPUs include both pods
	expectedMergedCPUs := pod1CPUs.Union(pod2CPUs)
	assert.Equal(t, expectedMergedCPUs, state.mergedMatchedRemainCPUs,
		"merged CPUs should include both pods' CPUs")

	t.Logf("Test passed: Successfully restored %d pre-allocatable pods with merged CPUSet %s",
		len(state.matched), state.mergedMatchedRemainCPUs.String())
}

// TestPlugin_FilterNominateReservationPreAllocation tests that FilterNominateReservation
// correctly handles pre-allocation reservations
func TestPlugin_FilterNominateReservationPreAllocation(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	// Pre-allocatable pod with 4 cores
	preAllocatablePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "pre-pod-1",
			Name:      "pre-allocatable-pod",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
		},
		Spec: corev1.PodSpec{
			Priority: ptr.To[int32](extension.PriorityProdValueMax),
			NodeName: "test-node",
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

	suit := newPluginTestSuit(t, []*corev1.Pod{preAllocatablePod}, []*corev1.Node{node})
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	assert.NotNil(t, p)

	pl := p.(*Plugin)
	suit.start()

	// Setup CPU topology
	pl.topologyOptionsManager.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(1, 2, 4, 2)
		options.NUMANodeResources = []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
		}
	})

	// Allocate CPUSet to pre-allocatable pod
	preAllocatableCPUSet := cpuset.NewCPUSet(0, 1, 2, 3)
	pl.resourceManager.Update("test-node", &PodAllocation{
		UID:                preAllocatablePod.UID,
		CPUSet:             preAllocatableCPUSet,
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
		NUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
		},
	})

	// Create a pre-allocation reservation
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-prealloc-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			PreAllocation:  true,
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					Annotations: map[string]string{
						extension.AnnotationResourceSpec: `{"preferredCPUBindPolicy": "FullPCPUs"}`,
					},
				},
				Spec: corev1.PodSpec{
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
			},
		},
	}

	rInfo := frameworkext.NewReservationInfo(reservation)
	reservePod := reservationutil.NewReservePod(reservation)
	if reservePod.Annotations == nil {
		reservePod.Annotations = make(map[string]string)
	}
	if _, ok := reservePod.Annotations[extension.AnnotationResourceSpec]; !ok {
		reservePod.Annotations[extension.AnnotationResourceSpec] = `{"preferredCPUBindPolicy": "FullPCPUs"}`
	}

	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	cycleState := framework.NewCycleState()

	// Step 1: PreFilter for reservation
	_, status := pl.PreFilter(context.TODO(), cycleState, reservePod)
	assert.True(t, status.IsSuccess(), "PreFilter should succeed")

	// Step 2: Restore pre-allocatable pods
	status = pl.PreRestoreReservationPreAllocation(context.TODO(), cycleState, rInfo)
	assert.True(t, status.IsSuccess())

	_, status = pl.RestoreReservationPreAllocation(context.TODO(), cycleState, rInfo, []*corev1.Pod{preAllocatablePod}, nodeInfo)
	assert.True(t, status.IsSuccess())

	// Step 3: FilterNominateReservation with pre-allocatable pod
	// Since preAllocationRInfo is set, it should use the pre-allocation logic
	status = pl.FilterNominateReservation(context.TODO(), cycleState, preAllocatablePod, rInfo, "test-node")

	// For pre-allocation, the pre-allocatable pod should be matched
	assert.True(t, status == nil || status.IsSuccess(),
		"FilterNominateReservation should succeed or return nil for matched pre-allocatable pod")
}

// TestPlugin_GetNominatedReusableAlloc tests the getNominatedReusableAlloc function
func TestPlugin_GetNominatedReusableAlloc(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	// Pre-allocatable pod
	preAllocatablePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:       "pre-pod-1",
			Name:      "pre-allocatable-pod",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLSR),
			},
		},
		Spec: corev1.PodSpec{
			Priority: ptr.To[int32](extension.PriorityProdValueMax),
			NodeName: "test-node",
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

	suit := newPluginTestSuit(t, []*corev1.Pod{preAllocatablePod}, []*corev1.Node{node})
	p, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	assert.NotNil(t, p)

	pl := p.(*Plugin)
	suit.start()

	// Setup CPU topology
	pl.topologyOptionsManager.UpdateTopologyOptions("test-node", func(options *TopologyOptions) {
		options.CPUTopology = buildCPUTopologyForTest(1, 2, 4, 2)
		options.NUMANodeResources = []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
		}
	})

	// Allocate CPUSet to pre-allocatable pod
	preAllocatableCPUSet := cpuset.NewCPUSet(0, 1, 2, 3)
	pl.resourceManager.Update("test-node", &PodAllocation{
		UID:                preAllocatablePod.UID,
		CPUSet:             preAllocatableCPUSet,
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
	})

	// Create a pre-allocation reservation
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "test-prealloc-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			PreAllocation:  true,
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: ptr.To[int32](extension.PriorityProdValueMax),
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
			},
		},
	}

	rInfo := frameworkext.NewReservationInfo(reservation)
	reservePod := reservationutil.NewReservePod(reservation)

	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(node)

	cycleState := framework.NewCycleState()

	// Restore pre-allocatable pods
	status := pl.PreRestoreReservationPreAllocation(context.TODO(), cycleState, rInfo)
	assert.True(t, status.IsSuccess())

	_, status = pl.RestoreReservationPreAllocation(context.TODO(), cycleState, rInfo, []*corev1.Pod{preAllocatablePod}, nodeInfo)
	assert.True(t, status.IsSuccess())

	// Setup fake nominator with pre-allocatable pod
	fakeNominator := pl.handle.(frameworkext.ExtendedHandle).GetReservationNominator().(*frameworkext.FakeNominator)
	fakeNominator.AddNominatedPreAllocation(rInfo, "test-node", preAllocatablePod)

	// Get restoreState
	reservationRestoreState := getReservationRestoreState(cycleState)
	restoreState := reservationRestoreState.getNodeState("test-node")

	// Test getNominatedReusableAlloc for pre-allocation reservation
	resourceOptions := &ResourceOptions{
		requiredPreAllocation: true,
	}

	reusableAllocMap, status := pl.getNominatedReusableAlloc(restoreState, resourceOptions, reservePod, node)
	assert.True(t, status == nil || status.IsSuccess())
	assert.NotNil(t, reusableAllocMap, "should return reusable alloc for pre-allocatable pod")

	if reusableAllocMap != nil {
		_, exists := reusableAllocMap[preAllocatablePod.UID]
		assert.True(t, exists, "pre-allocatable pod should be in reusable alloc map")
	}
}
