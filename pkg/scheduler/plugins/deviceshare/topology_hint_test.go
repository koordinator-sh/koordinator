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

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/schedulingphase"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/topologymanager"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
)

func newBitMask(bits ...int) bitmask.BitMask {
	mask, _ := bitmask.NewBitMask(bits...)
	return mask
}

func TestPlugin_GetPodTopologyHints(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
	}

	gpuRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
	}
	largeGPURequets := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("1700"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("1700"),
	}
	rdmaRequests := corev1.ResourceList{
		apiext.ResourceRDMA: resource.MustParse("2"),
	}
	tests := []struct {
		name            string
		podRequests     map[schedulingv1alpha1.DeviceType]corev1.ResourceList
		hints           apiext.DeviceAllocateHints
		jointAllocate   *apiext.DeviceJointAllocate
		assignedDevices apiext.DeviceAllocations
		want            map[string][]topologymanager.NUMATopologyHint
		wantErr         bool
	}{
		{
			name: "generate gpu&rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU:  gpuRequests,
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.GPU): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate gpu&rdma hints but large gpu requests",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU:  largeGPURequets,
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "generate gpu hints with assigned devices",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU: {
					apiext.ResourceGPUCore:        resource.MustParse("400"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("400"),
				},
			},
			assignedDevices: map[schedulingv1alpha1.DeviceType][]*apiext.DeviceAllocation{
				schedulingv1alpha1.GPU: {
					{
						Minor:     0,
						Resources: gpuRequests,
					},
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.GPU): {
					{NUMANodeAffinity: newBitMask(1), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate fpga empty hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.FPGA: {
					apiext.ResourceFPGA: resource.MustParse("100"),
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "generate 2 rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate rdma 2 vf hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector:       &metav1.LabelSelector{},
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate rdma 4 vf hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: {
					apiext.ResourceRDMA: resource.MustParse("4"),
				},
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector:       &metav1.LabelSelector{},
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: true, Score: defaultNUMAScore},
				},
			},
		},
		{
			name: "generate joint-allocate gpu&rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
				schedulingv1alpha1.GPU:  gpuRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector: &metav1.LabelSelector{},
				},
			},
			jointAllocate: &apiext.DeviceJointAllocate{
				DeviceTypes: []schedulingv1alpha1.DeviceType{schedulingv1alpha1.GPU, schedulingv1alpha1.RDMA},
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.RDMA): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
				string(schedulingv1alpha1.GPU): {
					{NUMANodeAffinity: newBitMask(0), Preferred: true, Score: defaultNUMAScore},
					{NUMANodeAffinity: newBitMask(1), Preferred: true},
					{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{node})
			deviceCR := fakeDeviceCR.DeepCopy()
			deviceCR.Name = node.Name
			deviceCR.ResourceVersion = "1"
			_, err := suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
			assert.NoError(t, err)

			p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)

			suit.koordinatorSharedInformerFactory.Start(nil)
			suit.SharedInformerFactory().Start(nil)
			suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)
			suit.SharedInformerFactory().WaitForCacheSync(nil)

			pl := p.(*Plugin)
			if tt.assignedDevices != nil {
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "assigned-pod",
						UID:       "123456",
					},
					Spec: corev1.PodSpec{
						NodeName: node.Name,
					},
				}
				assert.NoError(t, apiext.SetDeviceAllocations(pod, tt.assignedDevices))
				pl.nodeDeviceCache.updatePod(nil, pod)
			}

			hintSelectors, err := newHintSelectors(tt.hints)
			assert.NoError(t, err)

			pod := &corev1.Pod{}
			state := &preFilterState{
				skip:          false,
				podRequests:   tt.podRequests,
				hints:         tt.hints,
				hintSelectors: hintSelectors,
				jointAllocate: tt.jointAllocate,
			}
			state.gpuRequirements, _ = parseGPURequirements(pod, state.podRequests, state.hints[schedulingv1alpha1.GPU], nil, nil)
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, state)

			got, status := pl.GetPodTopologyHints(context.TODO(), cycleState, pod, node)
			assert.Equal(t, tt.want, got)
			if !tt.wantErr != status.IsSuccess() {
				t.Errorf("expect tt.wantErr=%v, but got %v", tt.wantErr, status)
				return
			}
		})
	}
}

// TestPlugin_TopologyHintsWithNominatedReservation reproduces the production incident where the NUMA hints were
// generated without knowing the reservation nominated for the pod: the feasibility of a NUMA node mask was probed
// against every matched reservation and even against the node unallocated resources, while the Reserve phase is
// only allowed to allocate the devices reserved by the nominated reservation. A NUMA node holding no reserved
// device was therefore admitted and the pod got rejected with ErrInsufficientDevicesInNominatedReservation.
func TestPlugin_TopologyHintsWithNominatedReservation(t *testing.T) {
	f := newReservedGPUHintFixture(t)
	// the pod is scheduled with the best-effort NUMA policy, so the hints are calculated during the Reserve phase,
	// where the reservation has already been nominated for the pod
	schedulingphase.RecordPhase(f.cycleState, schedulingphase.Reserve)
	f.pl.handle.GetReservationNominator().AddNominatedReservation(f.pod, f.node.Name, f.rInfo)

	got, status := f.pl.GetPodTopologyHints(context.TODO(), f.cycleState, f.pod, f.node)
	assert.True(t, status.IsSuccess())
	// NUMA node 0 holds no device reserved by the nominated reservation, so it must not be hinted at all
	assert.Equal(t, reservedGPUHints(), got)

	// the hint provider only admits the chosen affinity, the Reserve phase redoes the allocation with the same
	// constraints and it has to stay within the nominated reservation
	status = f.pl.Allocate(context.TODO(), f.cycleState, topologymanager.NUMATopologyHint{NUMANodeAffinity: newBitMask(1)}, f.pod, f.node)
	assert.True(t, status.IsSuccess())
	assert.Nil(t, f.state.allocationResult)

	status = f.pl.Reserve(context.TODO(), f.cycleState, f.pod, f.node.Name)
	assert.True(t, status.IsSuccess())
	f.assertReservedGPUAllocated(t, f.state.allocationResult[schedulingv1alpha1.GPU])
}

// TestPlugin_TopologyHintsWithoutNominatedReservation covers the Restricted and the SingleNUMANode policies, whose
// NUMA affinity is admitted during the Filter phase. A reservation is nominated no earlier than the PreScore phase,
// so the scope has to be resolved with the matched reservations there: the pod is required to allocate from one of
// them, hence the NUMA nodes holding none of the reserved devices must not be hinted.
func TestPlugin_TopologyHintsWithoutNominatedReservation(t *testing.T) {
	f := newReservedGPUHintFixture(t)
	// no phase is recorded during the Filter phase, and no reservation has been nominated for the pod yet
	f.state.isReservationRequired = true

	got, status := f.pl.GetPodTopologyHints(context.TODO(), f.cycleState, f.pod, f.node)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, reservedGPUHints(), got)

	// the hint provider never keeps its allocation, the Reserve phase is the only one filling the result
	status = f.pl.Allocate(context.TODO(), f.cycleState, topologymanager.NUMATopologyHint{NUMANodeAffinity: newBitMask(1)}, f.pod, f.node)
	assert.True(t, status.IsSuccess())
	assert.Nil(t, f.state.allocationResult)
}

// reservedGPUHintFixture holds a pod requesting one whole GPU on a node whose GPU 0~3 belong to NUMA node 0 and GPU
// 4~7 belong to NUMA node 1, with one Restricted reservation matched for the pod reserving reservedGPUMinor. Only
// the NUMA node masks including NUMA node 1 are able to allocate the reserved GPU.
type reservedGPUHintFixture struct {
	pl         *Plugin
	node       *corev1.Node
	pod        *corev1.Pod
	rInfo      *frameworkext.ReservationInfo
	state      *preFilterState
	cycleState *framework.CycleState
}

const reservedGPUMinor = 6

func wholeGPUResources() corev1.ResourceList {
	return corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
		apiext.ResourceGPUMemory:      resource.MustParse("83201216Ki"),
	}
}

// reservedGPUHints are the hints allocating the reserved GPU: NUMA node 0 holds none of the reserved devices, so it
// is not hinted alone, and the mask of all the NUMA nodes allocates the same GPU as the preferred one.
func reservedGPUHints() map[string][]topologymanager.NUMATopologyHint {
	return map[string][]topologymanager.NUMATopologyHint{
		string(schedulingv1alpha1.GPU): {
			{NUMANodeAffinity: newBitMask(1), Preferred: true, Score: defaultNUMAScore},
			{NUMANodeAffinity: newBitMask(0, 1), Preferred: false, Score: defaultNUMAScore},
		},
	}
}

func newReservedGPUHintFixture(t *testing.T) *reservedGPUHintFixture {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	reservedGPU := func() map[schedulingv1alpha1.DeviceType]deviceResources {
		return map[schedulingv1alpha1.DeviceType]deviceResources{
			schedulingv1alpha1.GPU: {reservedGPUMinor: wholeGPUResources()},
		}
	}
	rInfo := frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "gen-0", UID: "gen-0-uid"},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template:       &corev1.PodTemplateSpec{},
			AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
		},
		Status: schedulingv1alpha1.ReservationStatus{NodeName: node.Name},
	})

	suit := newPluginTestSuit(t, []*corev1.Node{node})
	deviceCR := fakeDeviceCR.DeepCopy()
	deviceCR.Name = node.Name
	deviceCR.ResourceVersion = "1"
	_, err := suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
	assert.NoError(t, err)

	p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)

	suit.koordinatorSharedInformerFactory.Start(nil)
	suit.SharedInformerFactory().Start(nil)
	suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)
	suit.SharedInformerFactory().WaitForCacheSync(nil)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "default", Name: "gpu-pod", UID: "gpu-pod-uid",
	}}
	state := &preFilterState{
		podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
			schedulingv1alpha1.GPU: {
				apiext.ResourceGPUCore:        resource.MustParse("100"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
		},
	}
	state.gpuRequirements, err = parseGPURequirements(pod, state.podRequests, nil, nil, nil)
	assert.NoError(t, err)

	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)
	cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{
		nodeToState: frameworkext.NodeReservationRestoreStates{
			node.Name: &nodeReservationRestoreStateData{
				matched:                  []reusableAlloc{{rInfo: rInfo, allocatable: reservedGPU(), remained: reservedGPU()}},
				mergedMatchedAllocatable: reservedGPU(),
			},
		},
	})

	return &reservedGPUHintFixture{
		pl:         p.(*Plugin),
		node:       node,
		pod:        pod,
		rInfo:      rInfo,
		state:      state,
		cycleState: cycleState,
	}
}

func (f *reservedGPUHintFixture) assertReservedGPUAllocated(t *testing.T, allocations []*apiext.DeviceAllocation) {
	t.Helper()
	if !assert.Len(t, allocations, 1) {
		return
	}
	assert.Equal(t, int32(reservedGPUMinor), allocations[0].Minor)
	wholeGPU := wholeGPUResources()
	assert.True(t, quotav1.Equals(wholeGPU, allocations[0].Resources),
		"expected the whole reserved GPU %v, but got %v", wholeGPU, allocations[0].Resources)
}

// TestPlugin_TopologyHintsWithReservationOnAnotherNUMANode is the case a single matched reservation cannot cover: the
// NUMA node masks the nominated reservation cannot satisfy have to be dropped even when another reservation matched for
// the pod does satisfy them. It also runs the hints through the topology manager merge, which is where dropping them
// matters: a narrower preferred hint wins over a wider non preferred one whatever the scores are, so a leaked hint for
// the NUMA node of the other reservation would be the admitted affinity and the Reserve phase would then reject the pod
// with ErrInsufficientDevicesInNominatedReservation, over and over.
func TestPlugin_TopologyHintsWithReservationOnAnotherNUMANode(t *testing.T) {
	f := newCrossNUMAReservationHintFixture(t)
	// the pod is scheduled with the best-effort NUMA policy, so the hints are calculated during the Reserve phase,
	// where the reservation has already been nominated for the pod
	schedulingphase.RecordPhase(f.cycleState, schedulingphase.Reserve)
	f.pl.handle.GetReservationNominator().AddNominatedReservation(f.pod, f.node.Name, f.rInfo)

	hints, status := f.pl.GetPodTopologyHints(context.TODO(), f.cycleState, f.pod, f.node)
	assert.True(t, status.IsSuccess())
	// The nominated reservation holds a single GPU on each NUMA node, so neither NUMA node alone can host the two
	// GPUs of the pod and only the mask of both of them is hinted. NUMA node 0 does host the two GPUs of the other
	// matched reservation, which is exactly the hint that must not be produced.
	assert.Equal(t, map[string][]topologymanager.NUMATopologyHint{
		string(schedulingv1alpha1.GPU): {
			{NUMANodeAffinity: newBitMask(0, 1), Preferred: true, Score: defaultNUMAScore},
		},
	}, hints)

	affinity, admit, _ := topologymanager.NewBestEffortPolicy(crossNUMANodes).Merge(
		[]map[string][]topologymanager.NUMATopologyHint{hints},
		apiext.NumaTopologyExclusivePreferred,
		[]apiext.NumaNodeStatus{apiext.NumaNodeStatusIdle, apiext.NumaNodeStatusIdle},
	)
	assert.True(t, admit)
	assert.Equal(t, newBitMask(0, 1), affinity.NUMANodeAffinity)

	topologymanager.InitStore(f.cycleState)
	topologymanager.GetStore(f.cycleState).SetAffinity(f.node.Name, affinity)
	status = f.pl.Allocate(context.TODO(), f.cycleState, affinity, f.pod, f.node)
	assert.True(t, status.IsSuccess())
	assert.Nil(t, f.state.allocationResult)

	status = f.pl.Reserve(context.TODO(), f.cycleState, f.pod, f.node.Name)
	assert.True(t, status.IsSuccess())
	assertGPUMinorsAllocated(t, f.state.allocationResult[schedulingv1alpha1.GPU], crossNUMAReservedGPUMinors)
}

var (
	// crossNUMANodes are the NUMA nodes of the node built by newCrossNUMAReservationHintFixture.
	crossNUMANodes = []int{0, 1}

	// crossNUMAReservedGPUMinors are the GPUs of the nominated reservation, one on each NUMA node, and
	// singleNUMAReservedGPUMinors are the ones of the other matched reservation, both on NUMA node 0.
	crossNUMAReservedGPUMinors  = []int{2, 4}
	singleNUMAReservedGPUMinors = []int{0, 1}
)

// newCrossNUMAReservationHintFixture holds a pod requesting two whole GPUs on a node whose GPU 0~3 belong to NUMA node
// 0 and GPU 4~7 belong to NUMA node 1, with two Restricted reservations matched for the pod: the nominated one reserves
// crossNUMAReservedGPUMinors and the other one reserves singleNUMAReservedGPUMinors.
func newCrossNUMAReservationHintFixture(t *testing.T) *reservedGPUHintFixture {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	reserved := func(minors []int) map[schedulingv1alpha1.DeviceType]deviceResources {
		gpus := deviceResources{}
		for _, minor := range minors {
			gpus[minor] = wholeGPUResources()
		}
		return map[schedulingv1alpha1.DeviceType]deviceResources{schedulingv1alpha1.GPU: gpus}
	}
	newRInfo := func(name string) *frameworkext.ReservationInfo {
		return frameworkext.NewReservationInfo(&schedulingv1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(name + "-uid")},
			Spec: schedulingv1alpha1.ReservationSpec{
				Template:       &corev1.PodTemplateSpec{},
				AllocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			},
			Status: schedulingv1alpha1.ReservationStatus{NodeName: node.Name},
		})
	}
	crossNUMARInfo, singleNUMARInfo := newRInfo("cross-numa"), newRInfo("single-numa")

	suit := newPluginTestSuit(t, []*corev1.Node{node})
	deviceCR := fakeDeviceCR.DeepCopy()
	deviceCR.Name = node.Name
	deviceCR.ResourceVersion = "1"
	_, err := suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
	assert.NoError(t, err)

	p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
	assert.NoError(t, err)

	suit.koordinatorSharedInformerFactory.Start(nil)
	suit.SharedInformerFactory().Start(nil)
	suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)
	suit.SharedInformerFactory().WaitForCacheSync(nil)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "default", Name: "gpu-pod", UID: "gpu-pod-uid",
	}}
	state := &preFilterState{
		podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
			schedulingv1alpha1.GPU: {
				apiext.ResourceGPUCore:        resource.MustParse("200"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("200"),
			},
		},
	}
	state.gpuRequirements, err = parseGPURequirements(pod, state.podRequests, nil, nil, nil)
	assert.NoError(t, err)

	cycleState := framework.NewCycleState()
	cycleState.Write(stateKey, state)
	cycleState.Write(reservationRestoreStateKey, &reservationRestoreStateData{
		nodeToState: frameworkext.NodeReservationRestoreStates{
			node.Name: &nodeReservationRestoreStateData{
				matched: []reusableAlloc{
					{rInfo: singleNUMARInfo, allocatable: reserved(singleNUMAReservedGPUMinors), remained: reserved(singleNUMAReservedGPUMinors)},
					{rInfo: crossNUMARInfo, allocatable: reserved(crossNUMAReservedGPUMinors), remained: reserved(crossNUMAReservedGPUMinors)},
				},
				mergedMatchedAllocatable: reserved(append(append([]int{}, singleNUMAReservedGPUMinors...), crossNUMAReservedGPUMinors...)),
			},
		},
	})

	return &reservedGPUHintFixture{
		pl:         p.(*Plugin),
		node:       node,
		pod:        pod,
		rInfo:      crossNUMARInfo,
		state:      state,
		cycleState: cycleState,
	}
}

func assertGPUMinorsAllocated(t *testing.T, allocations []*apiext.DeviceAllocation, minors []int) {
	t.Helper()
	if !assert.Len(t, allocations, len(minors)) {
		return
	}
	var gotMinors []int
	for _, allocation := range allocations {
		gotMinors = append(gotMinors, int(allocation.Minor))
		wholeGPU := wholeGPUResources()
		assert.True(t, quotav1.Equals(wholeGPU, allocation.Resources),
			"expected the whole GPU %v, but got %v", wholeGPU, allocation.Resources)
	}
	assert.ElementsMatch(t, minors, gotMinors)
}

func TestPlugin_Allocate(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
	}

	gpuRequests := corev1.ResourceList{
		apiext.ResourceGPUCore:   resource.MustParse("100"),
		apiext.ResourceGPUMemory: resource.MustParse("8Gi"),
	}
	rdmaRequests := corev1.ResourceList{
		apiext.ResourceRDMA: resource.MustParse("2"),
	}
	tests := []struct {
		name          string
		podRequests   map[schedulingv1alpha1.DeviceType]corev1.ResourceList
		hints         apiext.DeviceAllocateHints
		affinity      topologymanager.NUMATopologyHint
		jointAllocate *apiext.DeviceJointAllocate
		wantErr       bool
	}{
		{
			name: "allocate gpu&rdma by affinity",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.GPU:  gpuRequests,
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
		},
		{
			name: "generate fpga empty hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.FPGA: {
					apiext.ResourceFPGA: resource.MustParse("100"),
				},
			},
			affinity: topologymanager.NUMATopologyHint{},
			wantErr:  true,
		},
		{
			name: "generate 2 rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
		},
		{
			name: "generate rdma 2 vf hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector:       &metav1.LabelSelector{},
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
		},
		{
			name: "generate rdma 4 vf hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: {
					apiext.ResourceRDMA: resource.MustParse("4"),
				},
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector:       &metav1.LabelSelector{},
					AllocateStrategy: apiext.RequestsAsCountAllocateStrategy,
				},
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
			wantErr: true,
		},
		{
			name: "generate joint-allocate gpu&rdma hints",
			podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
				schedulingv1alpha1.RDMA: rdmaRequests,
				schedulingv1alpha1.GPU:  gpuRequests,
			},
			hints: apiext.DeviceAllocateHints{
				schedulingv1alpha1.RDMA: {
					VFSelector: &metav1.LabelSelector{},
				},
			},
			jointAllocate: &apiext.DeviceJointAllocate{
				DeviceTypes: []schedulingv1alpha1.DeviceType{schedulingv1alpha1.GPU, schedulingv1alpha1.RDMA},
			},
			affinity: topologymanager.NUMATopologyHint{
				NUMANodeAffinity: newBitMask(0),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{node})
			deviceCR := fakeDeviceCR.DeepCopy()
			deviceCR.Name = node.Name
			deviceCR.ResourceVersion = "1"
			_, err := suit.koordClientSet.SchedulingV1alpha1().Devices().Create(context.TODO(), deviceCR, metav1.CreateOptions{})
			assert.NoError(t, err)
			p, err := suit.proxyNew(context.TODO(), getDefaultArgs(), suit.Framework)
			assert.NoError(t, err)

			suit.koordinatorSharedInformerFactory.Start(nil)
			suit.SharedInformerFactory().Start(nil)
			suit.koordinatorSharedInformerFactory.WaitForCacheSync(nil)
			suit.SharedInformerFactory().WaitForCacheSync(nil)

			hintSelectors, err := newHintSelectors(tt.hints)
			assert.NoError(t, err)

			pod := &corev1.Pod{}
			state := &preFilterState{
				skip:          false,
				podRequests:   tt.podRequests,
				hints:         tt.hints,
				hintSelectors: hintSelectors,
				jointAllocate: tt.jointAllocate,
			}
			state.gpuRequirements, _ = parseGPURequirements(pod, tt.podRequests, state.hints[schedulingv1alpha1.GPU], nil, nil)
			cycleState := framework.NewCycleState()
			cycleState.Write(stateKey, state)
			pl := p.(*Plugin)
			status := pl.Allocate(context.TODO(), cycleState, tt.affinity, pod, node)
			if !tt.wantErr != status.IsSuccess() {
				t.Errorf("expect tt.wantErr=%v, but got %v", tt.wantErr, status)
				return
			}
		})
	}
}

func Test_generateDesignatedHints(t *testing.T) {
	type args struct {
		allocations apiext.DeviceAllocations
		topology    *NUMATopology
	}
	affinityNode1, _ := bitmask.NewBitMask(1)
	tests := []struct {
		name  string
		args  args
		want  map[string][]topologymanager.NUMATopologyHint
		want1 *fwktype.Status
	}{
		{
			name: "generate hints",
			args: args{
				allocations: apiext.DeviceAllocations{
					schedulingv1alpha1.GPU: {
						{
							Minor: 5,
						},
					},
				},
				topology: newNUMATopology(fakeDeviceCR),
			},
			want: map[string][]topologymanager.NUMATopologyHint{
				string(schedulingv1alpha1.GPU): {topologymanager.NUMATopologyHint{NUMANodeAffinity: affinityNode1, Unsatisfied: false, Preferred: true, Score: 500}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := generateDesignatedHints(tt.args.allocations, tt.args.topology)
			assert.Equalf(t, tt.want, got, "generateDesignatedHints(%v, %v)", tt.args.allocations, tt.args.topology)
			assert.Equalf(t, tt.want1, got1, "generateDesignatedHints(%v, %v)", tt.args.allocations, tt.args.topology)
		})
	}
}
