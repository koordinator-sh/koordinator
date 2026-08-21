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

package deviceshare

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/ptr"

	"github.com/stretchr/testify/assert"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func Test_Plugin_ReservationRestore(t *testing.T) {
	suit := newPluginTestSuit(t, []*corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "test-node-1"}},
	})
	p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	// Start informer factory to prevent gcNodeDevice goroutine from deleting device cache entries.
	stopCh := make(chan struct{})
	defer close(stopCh)
	suit.Framework.SharedInformerFactory().Start(stopCh)
	suit.Framework.SharedInformerFactory().WaitForCacheSync(stopCh)

	cycleState := framework.NewCycleState()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("100"),
						},
					},
				},
			},
		},
	}
	_, status := pl.PreFilter(context.TODO(), cycleState, pod, nil)
	assert.True(t, status.IsSuccess())

	pl.nodeDeviceCache.updateNodeDevice("test-node-1", &schedulingv1alpha1.Device{
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  ptr.To[int32](1),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  ptr.To[int32](2),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
		},
	})
	nd := pl.nodeDeviceCache.getNodeDevice("test-node-1", false)
	allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}

	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "reservation-1",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
		},
	}
	nd.updateCacheUsed(allocations, reservationutil.NewReservePod(reservation), true)

	podAllocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	allocatedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "allocated-pod-1",
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
		},
	}
	nd.updateCacheUsed(podAllocations, allocatedPod, true)

	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	})

	status = pl.PreRestoreReservation(context.TODO(), cycleState, pod)
	assert.True(t, status.IsSuccess())

	reservationInfo := frameworkext.NewReservationInfo(reservation)
	reservationInfo.AddAssignedPod(allocatedPod)
	nodeRestoreState, status := pl.RestoreReservation(context.TODO(), cycleState, pod, []*frameworkext.ReservationInfo{reservationInfo}, nil, nodeInfo)
	assert.True(t, status.IsSuccess())
	assert.NotNil(t, nodeRestoreState)
	// TODO: remove deprecated methods
	pl.FinalRestoreReservation(context.TODO(), cycleState, pod, frameworkext.NodeReservationRestoreStates{
		"test-node-1": nodeRestoreState,
	})

	expectedRestoreState := &reservationRestoreStateData{
		skip: false,
		nodeToState: frameworkext.NodeReservationRestoreStates{
			"test-node-1": &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo: reservationInfo,
						allocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								1: {
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
						allocated: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								1: {
									apiext.ResourceGPUCore:        resource.MustParse("50"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
								},
							},
						},
						remained: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								1: {
									apiext.ResourceGPUCore:        *resource.NewQuantity(50, resource.DecimalSI),
									apiext.ResourceGPUMemory:      *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
									apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
								},
							},
						},
					},
				},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: {
						1: {
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						},
					},
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: {
						1: {
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
						},
					},
				},
			},
		},
	}

	state := getReservationRestoreState(cycleState)
	assert.Equal(t, expectedRestoreState, state)
}

func Test_Plugin_RestoreReservationPreAllocation(t *testing.T) {
	suit := newPluginTestSuit(t, []*corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "test-node-1"}},
	})
	p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	// Start informer factory to prevent gcNodeDevice goroutine from deleting device cache entries.
	stopCh := make(chan struct{})
	defer close(stopCh)
	suit.Framework.SharedInformerFactory().Start(stopCh)
	suit.Framework.SharedInformerFactory().WaitForCacheSync(stopCh)

	cycleState := framework.NewCycleState()
	reservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: "pre-allocation-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPU: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
		},
		Status: schedulingv1alpha1.ReservationStatus{
			NodeName: "test-node-1",
		},
	}

	pl.nodeDeviceCache.updateNodeDevice("test-node-1", &schedulingv1alpha1.Device{
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  ptr.To[int32](1),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  ptr.To[int32](2),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
		},
	})

	// Create pre-allocatable pods
	preAllocatablePod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "pre-allocatable-pod-1",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("50"),
						},
					},
				},
			},
		},
	}

	preAllocatablePod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "pre-allocatable-pod-2",
			UID:       uuid.NewUUID(),
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node-1",
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("100"),
						},
					},
				},
			},
		},
	}

	nd := pl.nodeDeviceCache.getNodeDevice("test-node-1", false)
	// Allocate devices for pre-allocatable pods
	pod1Allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 1,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				},
			},
		},
	}
	nd.updateCacheUsed(pod1Allocations, preAllocatablePod1, true)

	pod2Allocations := apiext.DeviceAllocations{
		schedulingv1alpha1.GPU: {
			{
				Minor: 2,
				Resources: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
				},
			},
		},
	}
	nd.updateCacheUsed(pod2Allocations, preAllocatablePod2, true)

	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	})

	status := pl.PreRestoreReservationPreAllocation(context.TODO(), cycleState, frameworkext.NewReservationInfo(reservation))
	assert.True(t, status.IsSuccess())

	preAllocatable := []*corev1.Pod{preAllocatablePod1, preAllocatablePod2}
	nodeRestoreState, status := pl.RestoreReservationPreAllocation(context.TODO(), cycleState, frameworkext.NewReservationInfo(reservation), preAllocatable, nodeInfo)
	assert.True(t, status.IsSuccess())
	assert.NotNil(t, nodeRestoreState)

	nodeState, ok := nodeRestoreState.(*nodeReservationRestoreStateData)
	assert.True(t, ok)
	assert.NotNil(t, nodeState)

	// Verify that 2 pre-allocatable pods are restored
	assert.Len(t, nodeState.matched, 2)
	assert.NotNil(t, nodeState.preAllocationRInfo)
	assert.Equal(t, "pre-allocation-reservation", nodeState.preAllocationRInfo.GetName())

	// Verify first pre-allocatable pod allocation
	assert.Equal(t, preAllocatablePod1, nodeState.matched[0].preAllocatable)
	assert.NotNil(t, nodeState.matched[0].rInfo)
	assert.Equal(t, reservation.UID, nodeState.matched[0].rInfo.UID())
	assert.Equal(t, pod1Allocations[schedulingv1alpha1.GPU][0].Minor, int32(1))

	// Verify second pre-allocatable pod allocation
	assert.Equal(t, preAllocatablePod2, nodeState.matched[1].preAllocatable)
	assert.NotNil(t, nodeState.matched[1].rInfo)
	assert.Equal(t, reservation.UID, nodeState.matched[1].rInfo.UID())
	assert.Equal(t, pod2Allocations[schedulingv1alpha1.GPU][0].Minor, int32(2))

	// Verify merged allocatable resources
	assert.NotNil(t, nodeState.mergedMatchedAllocatable)
	assert.NotEmpty(t, nodeState.mergedMatchedAllocatable[schedulingv1alpha1.GPU])
}

func Test_tryAllocateFromReservation(t *testing.T) {
	resources := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
		apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
	}
	device := &schedulingv1alpha1.Device{}
	for i := 0; i < 2; i++ {
		device.Spec.Devices = append(device.Spec.Devices, schedulingv1alpha1.DeviceInfo{
			Minor:     ptr.To[int32](int32(i)),
			Health:    true,
			Type:      schedulingv1alpha1.GPU,
			Resources: resources,
		})
	}
	deviceCache := newNodeDeviceCache()
	deviceCache.updateNodeDevice("test-node", device)

	podRequestsHalfGPU := map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
		schedulingv1alpha1.GPU: {
			apiext.ResourceGPUCore:   resource.MustParse("50"),
			apiext.ResourceGPUMemory: resource.MustParse("4Gi"),
		},
	}

	defaultPolicyReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-policy-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{},
		},
	}

	alignedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-policy-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template:       &corev1.PodTemplateSpec{},
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
		},
	}

	restrictedReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-policy-reservation",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template:       &corev1.PodTemplateSpec{},
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
		},
	}

	reservationOne := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			0: {
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			1: {
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	}
	reservationHalf := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			0: {
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
		},
	}
	reservation75Percent := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			0: {
				apiext.ResourceGPUCore:        resource.MustParse("75"),
				apiext.ResourceGPUMemory:      resource.MustParse("6Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
			},
		},
	}
	reservation25Percent := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			0: {
				apiext.ResourceGPUCore:        resource.MustParse("25"),
				apiext.ResourceGPUMemory:      resource.MustParse("2Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
			},
		},
	}
	reservation25Percent1 := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU: {
			0: {
				apiext.ResourceGPUCore:        resource.MustParse("25"),
				apiext.ResourceGPUMemory:      resource.MustParse("2Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
			},
			1: {
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	}

	tests := []struct {
		name                    string
		state                   *preFilterState
		restoreState            *nodeReservationRestoreStateData
		deviceUsed              deviceResources
		requiredFromReservation bool
		pod                     *corev1.Pod
		wantResult              apiext.DeviceAllocations
		wantStatus              *fwktype.Status
	}{
		{
			name: "no matched reservations",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				mergedUnmatchedUsed:      map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocated:   map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
			wantResult: nil,
			wantStatus: nil,
		},
		{
			name: "allocate from default policy reservation",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(defaultPolicyReservation),
						allocatable: reservation25Percent,
						remained:    reservation25Percent,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservation25Percent[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
			wantResult: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "allocate from default policy reservation and required from reservation",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(defaultPolicyReservation),
						allocatable: reservationHalf,
						allocated:   reservation25Percent,
						remained:    reservationHalf,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservation25Percent[schedulingv1alpha1.GPU],
				},
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
				},
			},
			requiredFromReservation: true,
			wantResult: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "allocate from default policy reservation and required from reservation and reservation empty",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(defaultPolicyReservation),
						allocatable: reservationHalf,
						allocated:   reservationHalf,
						remained:    nil,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("150"),
					apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(150, resource.DecimalSI),
				},
			},
			requiredFromReservation: true,
			wantResult: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "allocate from Aligned policy reservation",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(alignedReservation),
						allocatable: reservationHalf,
						allocated:   nil,
						remained:    reservationHalf,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: nil,
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
				1: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			wantResult: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to allocate from Aligned policy reservation with bigger request but no remaining resources on node",
			state: &preFilterState{
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:   resource.MustParse("60"),
						apiext.ResourceGPUMemory: resource.MustParse("5Gi"),
					},
				},
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(alignedReservation),
						allocatable: reservationHalf,
						allocated:   nil,
						remained:    reservationHalf,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: nil,
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
				1: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			requiredFromReservation: true,
			wantResult:              nil,
			wantStatus:              fwktype.NewStatus(fwktype.Unschedulable, "Reservation(s) Insufficient gpu devices"),
		},
		{
			name: "failed to allocate from Aligned policy reservation that remaining little not fits request",
			state: &preFilterState{
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:   resource.MustParse("30"),
						apiext.ResourceGPUMemory: resource.MustParse("1Gi"),
					},
				},
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(alignedReservation),
						allocatable: reservationHalf,
						allocated:   reservation25Percent,
						remained:    reservation25Percent,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservation25Percent[schedulingv1alpha1.GPU],
				},
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("125"),
					apiext.ResourceGPUMemory:      resource.MustParse("10Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(125, resource.DecimalSI),
				},
				1: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			requiredFromReservation: true,
			wantResult:              nil,
			wantStatus:              fwktype.NewStatus(fwktype.Unschedulable, "Reservation(s) Insufficient gpu devices"),
		},
		{
			name: "allocate from Restricted policy reservation",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(restrictedReservation),
						allocatable: reservationHalf,
						allocated:   nil,
						remained:    reservationHalf,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: nil,
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
				1: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			wantResult: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to allocate from Restricted policy reservation since node remains resources but reservation not fits",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(restrictedReservation),
						allocatable: reservationHalf,
						allocated:   reservation25Percent,
						remained:    reservation25Percent,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservation25Percent[schedulingv1alpha1.GPU],
				},
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("75"),
					apiext.ResourceGPUMemory:      resource.MustParse("6Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(75, resource.DecimalSI),
				},
				1: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			requiredFromReservation: true,
			wantResult:              nil,
			wantStatus:              fwktype.NewStatus(fwktype.Unschedulable, "Reservation(s) Insufficient gpu devices"),
		},
		{
			name: "allocate from Restricted policy reservation with reservation-ignored pods",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(restrictedReservation),
						allocatable: reservationOne,
						allocated:   nil,
						remained:    reservationOne,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: nil,
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("175"),
					apiext.ResourceGPUMemory:      resource.MustParse("14Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(175, resource.DecimalSI),
				},
				1: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("150"),
					apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(150, resource.DecimalSI),
				},
			},
			wantResult: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "allocate with a reservation-ignored pod",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(restrictedReservation),
						allocatable: reservationOne,
						allocated:   reservation75Percent,
						remained:    reservation25Percent1,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationOne[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservation75Percent[schedulingv1alpha1.GPU],
				},
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("175"),
					apiext.ResourceGPUMemory:      resource.MustParse("14Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(175, resource.DecimalSI),
				},
				1: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("100"),
					apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(100, resource.DecimalSI),
				},
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelReservationIgnored: "true",
					},
				},
			},
			wantResult: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
						},
					},
				},
			},
			wantStatus: nil,
		},
		{
			name: "failed to allocate with a reservation-ignored pod",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo:       frameworkext.NewReservationInfo(restrictedReservation),
						allocatable: reservationOne,
						allocated:   reservationOne,
						remained:    nil,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationOne[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationOne[schedulingv1alpha1.GPU],
				},
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("200"),
					apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
				},
				1: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("200"),
					apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(200, resource.DecimalSI),
				},
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						apiext.LabelReservationIgnored: "true",
					},
				},
			},
			wantResult: nil,
			wantStatus: fwktype.NewStatus(fwktype.Unschedulable, "Insufficient gpu devices"),
		},
		{
			name: "allocate from restricted reservation with pre-allocatable pod - success",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{
						rInfo: frameworkext.NewReservationInfo(restrictedReservation),
						preAllocatable: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "pre-allocatable-pod",
							},
						},
						allocatable: reservationHalf,
						allocated:   nil,
						remained:    reservationHalf,
					},
				},
				mergedUnmatchedUsed: map[schedulingv1alpha1.DeviceType]deviceResources{},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: reservationHalf[schedulingv1alpha1.GPU],
				},
				mergedMatchedAllocated: map[schedulingv1alpha1.DeviceType]deviceResources{},
			},
			deviceUsed: deviceResources{
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("50"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
					apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
				},
			},
			wantResult: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
							apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(50, resource.DecimalSI),
						},
					},
				},
			},
			wantStatus: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := &Plugin{}

			basicPreemptible := appendAllocated(nil, tt.restoreState.mergedUnmatchedUsed, tt.state.preemptibleDevices["test-node"])

			nodeDeviceInfo := deviceCache.getNodeDevice("test-node", false)
			nodeDeviceInfo.deviceUsed[schedulingv1alpha1.GPU] = tt.deviceUsed
			nodeDeviceInfo.resetDeviceFree(schedulingv1alpha1.GPU)

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-1",
				},
			}

			allocator := &AutopilotAllocator{
				state:      tt.state,
				nodeDevice: nodeDeviceInfo,
				node:       node,
				pod:        &corev1.Pod{},
			}
			tt.state.gpuRequirements, _ = parseGPURequirements(allocator.pod, tt.state.podRequests, nil, nil, nil)

			result, status := pl.tryAllocateFromReusable(
				allocator,
				tt.state,
				tt.restoreState,
				tt.restoreState.matched,
				tt.pod,
				node,
				basicPreemptible,
				tt.requiredFromReservation,
			)
			err := fillGPUTotalMem(result, nodeDeviceInfo)
			assert.Equal(t, tt.wantStatus, status)
			if tt.wantResult != nil {
				for deviceType := range tt.wantResult {
					for i := range tt.wantResult[deviceType] {
						tt.wantResult[deviceType][i].Resources = removeFormat(tt.wantResult[deviceType][i].Resources)
						result[deviceType][i].Resources = removeFormat(result[deviceType][i].Resources)
					}
				}
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func Test_allocateWithNominated(t *testing.T) {
	// Fixed UIDs to avoid non-determinism from uuid.NewUUID()
	const (
		reservationUID = types.UID("test-reservation-uid")
		reservePodUID  = types.UID("test-reserve-pod-uid")
		ignoredPodUID  = types.UID("test-ignored-pod-uid")
		normalPodUID   = types.UID("test-normal-pod-uid")
	)

	// newDevice returns a fresh Device with two healthy GPUs (minor 0 and 1).
	newDevice := func() *schedulingv1alpha1.Device {
		return &schedulingv1alpha1.Device{
			Spec: schedulingv1alpha1.DeviceSpec{
				Devices: []schedulingv1alpha1.DeviceInfo{
					{
						Type:   schedulingv1alpha1.GPU,
						Minor:  ptr.To[int32](0),
						Health: true,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						},
					},
					{
						Type:   schedulingv1alpha1.GPU,
						Minor:  ptr.To[int32](1),
						Health: true,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("100"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
						},
					},
				},
			},
		}
	}

	// newReservationInfo returns a fresh ReservationInfo with a fixed UID.
	newReservationInfo := func() *frameworkext.ReservationInfo {
		reservation := &schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				UID:  reservationUID,
				Name: "test-reservation",
			},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template: &corev1.PodTemplateSpec{},
			},
			Status: schedulingv1alpha1.ReservationStatus{
				NodeName: "test-node",
			},
		}
		return frameworkext.NewReservationInfo(reservation)
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	tests := []struct {
		name              string
		pod               *corev1.Pod
		buildRestoreState func(rInfo *frameworkext.ReservationInfo) *nodeReservationRestoreStateData
		nominate          bool
		allocatePolicy    schedulingv1alpha1.ReservationAllocatePolicy
		wantNil           bool
		wantSuccess       bool
		wantNominated     bool
	}{
		{
			name: "reserve pod without pre-allocation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "reserve-pod",
					UID:  reservePodUID,
					Annotations: map[string]string{
						reservationutil.AnnotationReservePod: "true",
					},
				},
			},
			buildRestoreState: func(_ *frameworkext.ReservationInfo) *nodeReservationRestoreStateData {
				return &nodeReservationRestoreStateData{}
			},
			wantNil:     true,
			wantSuccess: true,
		},
		{
			name: "reservation-ignored pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ignored-pod",
					UID:  ignoredPodUID,
					Labels: map[string]string{
						apiext.LabelReservationIgnored: "true",
					},
				},
			},
			buildRestoreState: func(rInfo *frameworkext.ReservationInfo) *nodeReservationRestoreStateData {
				return &nodeReservationRestoreStateData{
					matched: []reusableAlloc{
						{
							rInfo: rInfo,
							allocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: {
										apiext.ResourceGPUCore:        resource.MustParse("50"),
										apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
									},
								},
							},
							remained: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: {
										apiext.ResourceGPUCore:        resource.MustParse("50"),
										apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
									},
								},
							},
						},
					},
				}
			},
			wantNil:     false,
			wantSuccess: true,
		},
		{
			name: "normal pod without nominated reservation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "normal-pod",
					UID:  normalPodUID,
				},
			},
			buildRestoreState: func(_ *frameworkext.ReservationInfo) *nodeReservationRestoreStateData {
				return &nodeReservationRestoreStateData{}
			},
			wantNil:     true,
			wantSuccess: true,
		},
		{
			// The pod is nominated to a Restricted reservation whose reserved device is exhausted.
			// It must be rejected instead of silently spilling over to the devices reserved by
			// the other reservations or to the remaining devices of the node.
			name: "normal pod nominated to an exhausted restricted reservation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "normal-pod",
					UID:  normalPodUID,
				},
			},
			nominate:       true,
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			buildRestoreState: func(rInfo *frameworkext.ReservationInfo) *nodeReservationRestoreStateData {
				return &nodeReservationRestoreStateData{
					matched: []reusableAlloc{
						{
							rInfo: rInfo,
							allocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
								schedulingv1alpha1.GPU: {
									0: {
										apiext.ResourceGPUCore:        resource.MustParse("50"),
										apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
										apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
									},
								},
							},
							// fully consumed by the other owner pods, so nothing is reusable
							remained: nil,
						},
					},
				}
			},
			wantNil:       true,
			wantSuccess:   false,
			wantNominated: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			// Each subtest gets its own isolated plugin instance, device cache, and rInfo
			// to avoid any shared-state races between subtests or background goroutines.
			// Pass node so that the fake client's node lister includes "test-node",
			// preventing gcNodeDevice from GC-ing it once the informer syncs.
			suit := newPluginTestSuit(t, []*corev1.Node{node})
			p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)
			pl := p.(*Plugin)

			// Start and sync the informer factory so the node lister is warm.
			// gcNodeDevice waits for the informer to sync before starting GC;
			// since "test-node" is in the node lister, GC will not remove it.
			stopCh := make(chan struct{})
			defer close(stopCh)
			suit.Framework.SharedInformerFactory().Start(stopCh)
			suit.Framework.SharedInformerFactory().WaitForCacheSync(stopCh)

			pl.nodeDeviceCache.updateNodeDevice("test-node", newDevice())

			rInfo := newReservationInfo()
			if tt.allocatePolicy != "" {
				rInfo.Reservation.Spec.AllocatePolicy = tt.allocatePolicy
			}
			restoreState := tt.buildRestoreState(rInfo)
			if tt.nominate {
				pl.handle.GetReservationNominator().AddNominatedReservation(tt.pod, node.Name, rInfo)
			}

			state := &preFilterState{
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:   resource.MustParse("50"),
						apiext.ResourceGPUMemory: resource.MustParse("4Gi"),
					},
				},
				preemptibleInRRs: map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources{
					"test-node": {},
				},
			}
			state.gpuRequirements, _ = parseGPURequirements(tt.pod, state.podRequests, nil, nil, nil)

			nodeDeviceInfo := pl.nodeDeviceCache.getNodeDevice("test-node", false)
			allocator := &AutopilotAllocator{
				state:      state,
				nodeDevice: nodeDeviceInfo,
				node:       node,
				pod:        tt.pod,
			}

			result, nominated, status := pl.allocateWithNominated(
				allocator,
				state,
				restoreState,
				node,
				tt.pod,
				nil,
			)

			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
			}

			if tt.wantSuccess {
				assert.True(t, status == nil || status.IsSuccess())
			} else {
				assert.False(t, status.IsSuccess())
			}
			assert.Equal(t, tt.wantNominated, nominated)
		})
	}
}

func Test_isDeviceAllocationsInclude(t *testing.T) {
	tests := []struct {
		name        string
		allocations apiext.DeviceAllocations
		required    map[schedulingv1alpha1.DeviceType]deviceResources
		want        bool
	}{
		{
			name: "allocations include all required devices",
			allocations: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("100"),
							apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
						},
					},
					{
						Minor: 1,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("50"),
							apiext.ResourceGPUMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			required: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {
						apiext.ResourceGPUCore:   resource.MustParse("100"),
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					1: {
						apiext.ResourceGPUCore:   resource.MustParse("50"),
						apiext.ResourceGPUMemory: resource.MustParse("4Gi"),
					},
				},
			},
			want: true,
		},
		{
			name: "allocations include partial required devices",
			allocations: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("100"),
							apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
			required: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {
						apiext.ResourceGPUCore:   resource.MustParse("100"),
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
				},
			},
			want: true,
		},
		{
			name: "allocations missing required device minors",
			allocations: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:   resource.MustParse("100"),
							apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
						},
					},
				},
			},
			required: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {
						apiext.ResourceGPUCore:   resource.MustParse("100"),
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
					1: {
						apiext.ResourceGPUCore:   resource.MustParse("50"),
						apiext.ResourceGPUMemory: resource.MustParse("4Gi"),
					},
				},
			},
			want: false,
		},
		{
			name: "allocations missing device type",
			allocations: apiext.DeviceAllocations{
				schedulingv1alpha1.RDMA: {
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceRDMA: resource.MustParse("100"),
						},
					},
				},
			},
			required: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {
						apiext.ResourceGPUCore:   resource.MustParse("100"),
						apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
					},
				},
			},
			want: false,
		},
		{
			name:        "empty required devices",
			allocations: apiext.DeviceAllocations{},
			required:    map[schedulingv1alpha1.DeviceType]deviceResources{},
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDeviceAllocationsInclude(tt.allocations, tt.required)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_dumpDeviceMinors(t *testing.T) {
	tests := []struct {
		name      string
		resources map[schedulingv1alpha1.DeviceType]deviceResources
		want      map[schedulingv1alpha1.DeviceType][]int
	}{
		{
			name:      "nil resources",
			resources: nil,
			want:      nil,
		},
		{
			name:      "empty resources",
			resources: map[schedulingv1alpha1.DeviceType]deviceResources{},
			want:      nil,
		},
		{
			name: "minors are sorted",
			resources: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					7: {apiext.ResourceGPUCore: resource.MustParse("100")},
					2: {apiext.ResourceGPUCore: resource.MustParse("100")},
					5: {apiext.ResourceGPUCore: resource.MustParse("100")},
				},
			},
			want: map[schedulingv1alpha1.DeviceType][]int{
				schedulingv1alpha1.GPU: {2, 5, 7},
			},
		},
		{
			// an exhausted reservation holds zero-valued resources, which must not be reported
			// as a minor still having remaining capacity
			name: "zero and empty resources are skipped",
			resources: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {apiext.ResourceGPUCore: resource.MustParse("0")},
					1: {},
					2: {apiext.ResourceGPUCore: resource.MustParse("100")},
				},
			},
			want: map[schedulingv1alpha1.DeviceType][]int{
				schedulingv1alpha1.GPU: {2},
			},
		},
		{
			name: "multiple device types",
			resources: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					1: {apiext.ResourceGPUCore: resource.MustParse("100")},
				},
				schedulingv1alpha1.RDMA: {
					3: {apiext.ResourceRDMA: resource.MustParse("100")},
					0: {apiext.ResourceRDMA: resource.MustParse("0")},
				},
			},
			want: map[schedulingv1alpha1.DeviceType][]int{
				schedulingv1alpha1.GPU:  {1},
				schedulingv1alpha1.RDMA: {3},
			},
		},
		{
			// a device type whose every minor is exhausted still reports an entry, so the log
			// distinguishes "reserved but exhausted" from "never reserved"
			name: "device type with all minors exhausted",
			resources: map[schedulingv1alpha1.DeviceType]deviceResources{
				schedulingv1alpha1.GPU: {
					0: {apiext.ResourceGPUCore: resource.MustParse("0")},
				},
			},
			want: map[schedulingv1alpha1.DeviceType][]int{
				schedulingv1alpha1.GPU: {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, dumpDeviceMinors(tt.resources))
		})
	}
}

func Test_dumpReusableAllocs(t *testing.T) {
	newRInfo := func(name string, uid types.UID, policy schedulingv1alpha1.ReservationAllocatePolicy) *frameworkext.ReservationInfo {
		return frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template:       &corev1.PodTemplateSpec{},
				AllocatePolicy: policy,
			},
		})
	}

	tests := []struct {
		name   string
		allocs []reusableAlloc
		want   []string
	}{
		{
			name:   "nil allocs",
			allocs: nil,
			want:   nil,
		},
		{
			name:   "empty allocs",
			allocs: []reusableAlloc{},
			want:   nil,
		},
		{
			// the reservation still holds its whole reserved device, so remained equals allocatable
			name: "restricted reservation with remaining capacity",
			allocs: []reusableAlloc{
				{
					rInfo: newRInfo("reservation-a", "uid-a", schedulingv1alpha1.ReservationAllocatePolicyRestricted),
					allocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
						schedulingv1alpha1.GPU: {7: {apiext.ResourceGPUCore: resource.MustParse("100")}},
					},
					remained: map[schedulingv1alpha1.DeviceType]deviceResources{
						schedulingv1alpha1.GPU: {7: {apiext.ResourceGPUCore: resource.MustParse("100")}},
					},
				},
			},
			want: []string{
				"reservation-a(uid=uid-a, policy=Restricted, allocatable=map[gpu:[7]], remained=map[gpu:[7]])",
			},
		},
		{
			// an exhausted reservation reports an empty remained, which is the signal that the
			// nominated reservation cannot serve the pod
			name: "exhausted reservation reports empty remained",
			allocs: []reusableAlloc{
				{
					rInfo: newRInfo("reservation-b", "uid-b", schedulingv1alpha1.ReservationAllocatePolicyRestricted),
					allocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
						schedulingv1alpha1.GPU: {2: {apiext.ResourceGPUCore: resource.MustParse("100")}},
					},
					remained: nil,
				},
			},
			want: []string{
				"reservation-b(uid=uid-b, policy=Restricted, allocatable=map[gpu:[2]], remained=map[])",
			},
		},
		{
			name: "pre-allocatable pod is appended to the reservation name",
			allocs: []reusableAlloc{
				{
					rInfo:          newRInfo("reservation-c", "uid-c", schedulingv1alpha1.ReservationAllocatePolicyAligned),
					preAllocatable: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pre-allocatable-pod"}},
					allocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
						schedulingv1alpha1.GPU: {1: {apiext.ResourceGPUCore: resource.MustParse("100")}},
					},
				},
			},
			want: []string{
				"reservation-c/pre-allocatable-pod(uid=uid-c, policy=Aligned, allocatable=map[gpu:[1]], remained=map[])",
			},
		},
		{
			// dumping is only used for logging, so a malformed alloc must not panic
			name:   "nil rInfo does not panic",
			allocs: []reusableAlloc{{}},
			want:   []string{"(uid=, policy=, allocatable=map[], remained=map[])"},
		},
		{
			name: "multiple allocs keep their order",
			allocs: []reusableAlloc{
				{rInfo: newRInfo("reservation-d", "uid-d", schedulingv1alpha1.ReservationAllocatePolicyRestricted)},
				{rInfo: newRInfo("reservation-e", "uid-e", schedulingv1alpha1.ReservationAllocatePolicyDefault)},
			},
			want: []string{
				"reservation-d(uid=uid-d, policy=Restricted, allocatable=map[], remained=map[])",
				"reservation-e(uid=uid-e, policy=, allocatable=map[], remained=map[])",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, dumpReusableAllocs(tt.allocs))
		})
	}
}

func Test_getNominatedReusableAlloc(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}

	newRInfo := func(uid types.UID, preAllocation bool) *frameworkext.ReservationInfo {
		return frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: "reservation-" + string(uid), UID: uid},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template:       &corev1.PodTemplateSpec{},
				PreAllocation:  preAllocation,
				AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			},
			Status: schedulingv1alpha1.ReservationStatus{NodeName: node.Name},
		})
	}
	reservedGPU := func(minor int) map[schedulingv1alpha1.DeviceType]deviceResources {
		return map[schedulingv1alpha1.DeviceType]deviceResources{
			schedulingv1alpha1.GPU: {minor: {apiext.ResourceGPUCore: resource.MustParse("100")}},
		}
	}

	normalPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "normal-pod", UID: "normal-pod-uid"}}
	reservePod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "reserve-pod", UID: "reserve-pod-uid",
		Annotations: map[string]string{reservationutil.AnnotationReservePod: "true"},
	}}
	preAllocReservePod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "pre-alloc-reserve-pod", UID: "pre-alloc-reserve-pod-uid",
		Annotations: map[string]string{
			reservationutil.AnnotationReservePod:      "true",
			reservationutil.AnnotationIsPreAllocation: "true",
		},
	}}
	preAllocatablePod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pre-allocatable-pod", UID: "pre-allocatable-pod-uid"}}
	otherPreAllocatablePod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other-pre-allocatable-pod", UID: "other-pre-allocatable-pod-uid"}}

	tests := []struct {
		name string
		pod  *corev1.Pod
		// setup builds the restore state and registers the nomination on the plugin's nominator.
		setup func(pl *Plugin) *nodeReservationRestoreStateData
		// wantMatchedIndex is the index in restoreState.matched expected to be returned,
		// or -1 when no reusable allocation is expected.
		wantMatchedIndex int
		wantNominated    bool
	}{
		{
			name: "normal pod without nominated reservation",
			pod:  normalPod,
			setup: func(_ *Plugin) *nodeReservationRestoreStateData {
				return &nodeReservationRestoreStateData{}
			},
			wantMatchedIndex: -1,
			wantNominated:    false,
		},
		{
			name: "normal pod nominated to a reservation reserving devices",
			pod:  normalPod,
			setup: func(pl *Plugin) *nodeReservationRestoreStateData {
				rInfo := newRInfo("reserving-devices", false)
				pl.handle.GetReservationNominator().AddNominatedReservation(normalPod, node.Name, rInfo)
				return &nodeReservationRestoreStateData{
					matched: []reusableAlloc{
						{rInfo: newRInfo("other", false), allocatable: reservedGPU(2), remained: reservedGPU(2)},
						{rInfo: rInfo, allocatable: reservedGPU(7), remained: reservedGPU(7)},
					},
				}
			},
			wantMatchedIndex: 1,
			wantNominated:    true,
		},
		{
			// The reservation is nominated but reserves no device resource, so it is absent from
			// the restored matched list. It must still be reported as nominated, otherwise the
			// caller would fall back and take the devices reserved by the other reservations.
			name: "normal pod nominated to a reservation reserving no device",
			pod:  normalPod,
			setup: func(pl *Plugin) *nodeReservationRestoreStateData {
				pl.handle.GetReservationNominator().AddNominatedReservation(normalPod, node.Name, newRInfo("reserving-nothing", false))
				return &nodeReservationRestoreStateData{
					matched: []reusableAlloc{
						{rInfo: newRInfo("other", false), allocatable: reservedGPU(2), remained: reservedGPU(2)},
					},
				}
			},
			wantMatchedIndex: -1,
			wantNominated:    true,
		},
		{
			name: "reserve pod without pre-allocation",
			pod:  reservePod,
			setup: func(_ *Plugin) *nodeReservationRestoreStateData {
				return &nodeReservationRestoreStateData{}
			},
			wantMatchedIndex: -1,
			wantNominated:    false,
		},
		{
			name: "pre-allocating reserve pod without restored pre-allocation info",
			pod:  preAllocReservePod,
			setup: func(_ *Plugin) *nodeReservationRestoreStateData {
				return &nodeReservationRestoreStateData{}
			},
			wantMatchedIndex: -1,
			wantNominated:    false,
		},
		{
			name: "pre-allocating reserve pod without nominated pre-allocatable pod",
			pod:  preAllocReservePod,
			setup: func(_ *Plugin) *nodeReservationRestoreStateData {
				return &nodeReservationRestoreStateData{
					preAllocationRInfo: newRInfo("pre-allocating", true),
					matched: []reusableAlloc{
						{rInfo: newRInfo("pre-allocating", true), preAllocatable: preAllocatablePod, allocatable: reservedGPU(7)},
					},
				}
			},
			wantMatchedIndex: -1,
			wantNominated:    false,
		},
		{
			name: "pre-allocating reserve pod nominated to a pre-allocatable pod reserving devices",
			pod:  preAllocReservePod,
			setup: func(pl *Plugin) *nodeReservationRestoreStateData {
				rInfo := newRInfo("pre-allocating", true)
				pl.handle.GetReservationNominator().AddNominatedPreAllocation(rInfo, node.Name, preAllocatablePod)
				return &nodeReservationRestoreStateData{
					preAllocationRInfo: rInfo,
					matched: []reusableAlloc{
						{rInfo: rInfo, preAllocatable: otherPreAllocatablePod, allocatable: reservedGPU(2)},
						{rInfo: rInfo, preAllocatable: preAllocatablePod, allocatable: reservedGPU(7)},
					},
				}
			},
			wantMatchedIndex: 1,
			wantNominated:    true,
		},
		{
			// The nominated pre-allocatable pod holds no device, so it is absent from the matched
			// list while the nomination itself still stands.
			name: "pre-allocating reserve pod nominated to a pre-allocatable pod reserving no device",
			pod:  preAllocReservePod,
			setup: func(pl *Plugin) *nodeReservationRestoreStateData {
				rInfo := newRInfo("pre-allocating", true)
				pl.handle.GetReservationNominator().AddNominatedPreAllocation(rInfo, node.Name, preAllocatablePod)
				return &nodeReservationRestoreStateData{
					preAllocationRInfo: rInfo,
					matched: []reusableAlloc{
						{rInfo: rInfo, preAllocatable: otherPreAllocatablePod, allocatable: reservedGPU(2)},
					},
				}
			},
			wantMatchedIndex: -1,
			wantNominated:    true,
		},
		{
			// RestoreReservationPreAllocation appends the pre-allocatable allocations to the ones
			// already restored by RestoreReservation, and the latter carry no pre-allocatable pod.
			// Such entries must be skipped rather than dereferenced.
			name: "pre-allocating reserve pod with a plain matched reservation in the restore state",
			pod:  preAllocReservePod,
			setup: func(pl *Plugin) *nodeReservationRestoreStateData {
				rInfo := newRInfo("pre-allocating", true)
				pl.handle.GetReservationNominator().AddNominatedPreAllocation(rInfo, node.Name, preAllocatablePod)
				return &nodeReservationRestoreStateData{
					preAllocationRInfo: rInfo,
					matched: []reusableAlloc{
						{rInfo: newRInfo("plain", false), allocatable: reservedGPU(2), remained: reservedGPU(2)},
						{rInfo: rInfo, preAllocatable: preAllocatablePod, allocatable: reservedGPU(7)},
					},
				}
			},
			wantMatchedIndex: 1,
			wantNominated:    true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{node})
			p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)
			pl := p.(*Plugin)

			restoreState := tt.setup(pl)

			got, nominated, status := pl.getNominatedReusableAlloc(restoreState, tt.pod, node)

			assert.True(t, status.IsSuccess())
			assert.Equal(t, tt.wantNominated, nominated)
			if tt.wantMatchedIndex < 0 {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, restoreState.matched[tt.wantMatchedIndex:tt.wantMatchedIndex+1], got)
		})
	}
}

// Test_Plugin_Reserve_NominatedReservationMustNotSpill reproduces the production incident where a pod
// nominated to a Restricted reservation holding one GPU was allocated the GPU reserved by a *different*
// reservation: every device of the node was reserved, the nominated reservation could not serve the pod,
// and the allocation silently fell back to the devices released by mergedMatchedAllocatable, picking the
// smallest minor. Such a pod would be accounted as an owner of its nominated reservation while occupying
// another reservation's device, so it must be rejected instead.
func Test_Plugin_Reserve_NominatedReservationMustNotSpill(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}

	wholeGPU := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
		apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
	}
	reservedGPU := func(minor int) map[schedulingv1alpha1.DeviceType]deviceResources {
		return map[schedulingv1alpha1.DeviceType]deviceResources{
			schedulingv1alpha1.GPU: {minor: wholeGPU.DeepCopy()},
		}
	}
	newRInfo := func(name string, uid types.UID) *frameworkext.ReservationInfo {
		return frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name, UID: uid},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template:       &corev1.PodTemplateSpec{},
				AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			},
			Status: schedulingv1alpha1.ReservationStatus{NodeName: node.Name},
		})
	}

	// Both GPUs of the node are entirely held by the two reserve pods, so nothing is free outside
	// of the reservations, exactly like the node observed in the incident.
	newNodeDeviceCache := func() *nodeDeviceCache {
		return &nodeDeviceCache{
			nodeDeviceInfos: map[string]*nodeDevice{
				node.Name: {
					deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
						schedulingv1alpha1.GPU: {0: wholeGPU.DeepCopy(), 1: wholeGPU.DeepCopy()},
					},
					deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
						schedulingv1alpha1.GPU: {0: wholeGPU.DeepCopy(), 1: wholeGPU.DeepCopy()},
					},
					deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
						schedulingv1alpha1.GPU: {},
					},
					allocateSet:   map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
					vfAllocations: map[schedulingv1alpha1.DeviceType]*VFAllocation{},
					numaTopology:  &NUMATopology{},
					deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
						schedulingv1alpha1.GPU: {
							{Type: schedulingv1alpha1.GPU, Health: true, UUID: "gpu-0", Minor: ptr.To[int32](0), Resources: wholeGPU.DeepCopy()},
							{Type: schedulingv1alpha1.GPU, Health: true, UUID: "gpu-1", Minor: ptr.To[int32](1), Resources: wholeGPU.DeepCopy()},
						},
					},
				},
			},
		}
	}

	rInfoA := newRInfo("reservation-a", "reservation-a-uid")
	rInfoB := newRInfo("reservation-b", "reservation-b-uid")

	tests := []struct {
		name         string
		restoreState *nodeReservationRestoreStateData
	}{
		{
			// reservation-b reserves GPU 1 but has already handed it to its own owner pods,
			// so the Restricted policy leaves the pod nothing to reuse.
			name: "nominated reservation is exhausted",
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{rInfo: rInfoA, allocatable: reservedGPU(0), remained: reservedGPU(0)},
					{rInfo: rInfoB, allocatable: reservedGPU(1), remained: nil},
				},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: {0: wholeGPU.DeepCopy(), 1: wholeGPU.DeepCopy()},
				},
			},
		},
		{
			// reservation-b is nominated but reserves no device at all, so it is absent from the
			// restored matched list. The pod must not take GPU 0 reserved by reservation-a.
			name: "nominated reservation reserves no device",
			restoreState: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{rInfo: rInfoA, allocatable: reservedGPU(0), remained: reservedGPU(0)},
				},
				mergedMatchedAllocatable: map[schedulingv1alpha1.DeviceType]deviceResources{
					schedulingv1alpha1.GPU: {0: wholeGPU.DeepCopy()},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{node})
			p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)
			pl := p.(*Plugin)
			pl.nodeDeviceCache = newNodeDeviceCache()

			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Namespace: "default", Name: "gpu-pod", UID: "gpu-pod-uid",
			}}
			state := &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: wholeGPU.DeepCopy(),
				},
			}
			state.gpuRequirements, err = parseGPURequirements(pod, state.podRequests, nil,
				testGPUSharedResourceTemplatesCache, testGPUSharedResourceTemplatesMatchedResources)
			assert.NoError(t, err)

			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, state)
			cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{
				nodeToState: frameworkext.NodeReservationRestoreStates{node.Name: tt.restoreState},
			})
			// the reservation plugin has already nominated reservation-b for this pod
			pl.handle.GetReservationNominator().AddNominatedReservation(pod, node.Name, rInfoB)

			status := pl.Reserve(context.TODO(), cycleState, pod, node.Name)

			assert.Equal(t, fwktype.Unschedulable, status.Code(), "the pod must be rejected, got status %v", status)
			// most importantly, it must not have been given GPU 0 reserved by reservation-a
			assert.Nil(t, state.allocationResult)
		})
	}
}

// Test_Plugin_FilterNominateReservation_PreAllocation checks that the pre-allocation branch skips the
// allocations restored for the matched reservations, which carry no pre-allocatable pod, instead of
// dereferencing them. RestoreReservationPreAllocation appends the pre-allocatable allocations to the
// ones already restored by RestoreReservation, so both kinds can coexist in the matched list.
func Test_Plugin_FilterNominateReservation_PreAllocation(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}
	suit := newPluginTestSuit(t, []*corev1.Node{node})
	p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

	stopCh := make(chan struct{})
	defer close(stopCh)
	suit.Framework.SharedInformerFactory().Start(stopCh)
	suit.Framework.SharedInformerFactory().WaitForCacheSync(stopCh)

	wholeGPU := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
	}
	pl.nodeDeviceCache.updateNodeDevice(node.Name, &schedulingv1alpha1.Device{
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{Type: schedulingv1alpha1.GPU, Minor: ptr.To[int32](1), Health: true, Resources: wholeGPU.DeepCopy()},
			},
		},
	})

	preAllocatablePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "pre-allocatable-pod", UID: uuid.NewUUID()},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				apiext.ResourceGPU: resource.MustParse("100"),
			}},
		}}},
	}

	cycleState := framework.NewCycleState()
	_, status := pl.PreFilter(context.TODO(), cycleState, preAllocatablePod, nil)
	assert.True(t, status.IsSuccess())

	newRInfo := func(name string, preAllocation bool) *frameworkext.ReservationInfo {
		return frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name, UID: uuid.NewUUID()},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template:       &corev1.PodTemplateSpec{},
				PreAllocation:  preAllocation,
				AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			},
			Status: schedulingv1alpha1.ReservationStatus{NodeName: node.Name},
		})
	}
	reservedGPU := func() map[schedulingv1alpha1.DeviceType]deviceResources {
		return map[schedulingv1alpha1.DeviceType]deviceResources{
			schedulingv1alpha1.GPU: {1: wholeGPU.DeepCopy()},
		}
	}

	preAllocRInfo := newRInfo("pre-allocating-reservation", true)
	cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{
		nodeToState: frameworkext.NodeReservationRestoreStates{
			node.Name: &nodeReservationRestoreStateData{
				preAllocationRInfo: preAllocRInfo,
				matched: []reusableAlloc{
					// restored by RestoreReservation, so it has no pre-allocatable pod
					{rInfo: newRInfo("plain-reservation", false), allocatable: reservedGPU(), remained: reservedGPU()},
					// restored by RestoreReservationPreAllocation
					{rInfo: preAllocRInfo, preAllocatable: preAllocatablePod, allocatable: reservedGPU(), remained: reservedGPU()},
				},
			},
		},
	})

	status = pl.FilterNominateReservation(context.TODO(), cycleState, preAllocatablePod, preAllocRInfo, node.Name)
	assert.True(t, status.IsSuccess(), "status: %v", status)
}
