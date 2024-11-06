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
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	"github.com/stretchr/testify/assert"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func Test_Plugin_ReservationRestore(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)
	pl := p.(*Plugin)

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
	_, status := pl.PreFilter(context.TODO(), cycleState, pod)
	assert.True(t, status.IsSuccess())

	pl.nodeDeviceCache.updateNodeDevice("test-node-1", &schedulingv1alpha1.Device{
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  pointer.Int32(1),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  pointer.Int32(2),
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
				matched: []reservationAlloc{
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

func Test_tryAllocateFromReservation(t *testing.T) {
	resources := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
		apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
	}
	device := &schedulingv1alpha1.Device{}
	for i := 0; i < 2; i++ {
		device.Spec.Devices = append(device.Spec.Devices, schedulingv1alpha1.DeviceInfo{
			Minor:     pointer.Int32(int32(i)),
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
		wantStatus              *framework.Status
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
				matched: []reservationAlloc{
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
				matched: []reservationAlloc{
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
				matched: []reservationAlloc{
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
				matched: []reservationAlloc{
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
				matched: []reservationAlloc{
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
			wantStatus:              framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient gpu devices"),
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
				matched: []reservationAlloc{
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
			wantStatus:              framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient gpu devices"),
		},
		{
			name: "allocate from Restricted policy reservation",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reservationAlloc{
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
				matched: []reservationAlloc{
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
			wantStatus:              framework.NewStatus(framework.Unschedulable, "Reservation(s) Insufficient gpu devices"),
		},
		{
			name: "allocate from Restricted policy reservation with reservation-ignored pods",
			state: &preFilterState{
				podRequests: podRequestsHalfGPU,
			},
			restoreState: &nodeReservationRestoreStateData{
				matched: []reservationAlloc{
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
				matched: []reservationAlloc{
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
				matched: []reservationAlloc{
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
			wantStatus: framework.NewStatus(framework.Unschedulable, "Insufficient gpu devices"),
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
			tt.state.gpuRequirements, _ = parseGPURequirements(allocator.pod, tt.state.podRequests, nil)

			result, status := pl.tryAllocateFromReservation(
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
