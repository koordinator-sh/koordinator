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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	schedulerconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

func TestScore(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	gpuResources := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
		apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
	}

	deviceInfoGenerator := func(deviceType schedulingv1alpha1.DeviceType, minor int, resources corev1.ResourceList) *schedulingv1alpha1.DeviceInfo {
		return &schedulingv1alpha1.DeviceInfo{
			Type:      deviceType,
			Health:    true,
			UUID:      fmt.Sprintf("%s-123456-%d", deviceType, minor),
			Minor:     pointer.Int32(int32(minor)),
			Resources: resources,
		}
	}

	tests := []struct {
		name            string
		strategy        schedulerconfig.ScoringStrategyType
		state           *preFilterState
		reserved        apiext.DeviceAllocations
		nodeDeviceCache *nodeDeviceCache
		wantScore       int64
		wantStatus      *framework.Status
	}{
		{
			name:       "error missing preFilterState",
			wantStatus: framework.AsStatus(framework.ErrNotFound),
		},
		{
			name:       "skip == true",
			state:      &preFilterState{skip: true},
			wantStatus: nil,
		},
		{
			name:            "empty node info",
			state:           &preFilterState{skip: false},
			nodeDeviceCache: newNodeDeviceCache(),
			wantScore:       0,
			wantStatus:      nil,
		},
		{
			name:            "error missing nodecache",
			state:           &preFilterState{skip: false},
			nodeDeviceCache: newNodeDeviceCache(),
			wantScore:       0,
			wantStatus:      nil,
		},
		{
			name: "no device resources",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": newNodeDevice(),
				},
			},
			wantScore:  0,
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, "Insufficient gpu devices"),
		},
		{
			name: "completely idle node",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("100"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {0: gpuResources},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {0: gpuResources},
						},
						numaTopology: &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {deviceInfoGenerator(schedulingv1alpha1.GPU, 0, gpuResources)},
						},
					},
				},
			},
			wantScore:  0,
			wantStatus: nil,
		},
		{
			name: "multiple GPU devices and completely idle",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("50"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: gpuResources,
								1: gpuResources,
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: gpuResources,
								1: gpuResources,
							},
						},
						numaTopology: &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								deviceInfoGenerator(schedulingv1alpha1.GPU, 0, gpuResources),
								deviceInfoGenerator(schedulingv1alpha1.GPU, 1, gpuResources),
							},
						},
					},
				},
			},
			wantScore:  75,
			wantStatus: nil,
		},
		{
			name: "remaining device resources",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("50"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("75"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
									apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {0: gpuResources},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
							},
						},
						numaTopology: &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {deviceInfoGenerator(schedulingv1alpha1.GPU, 0, gpuResources)},
						},
					},
				},
			},
			wantScore:  25,
			wantStatus: nil,
		},
		{
			name:     "remaining device resources with MostAllocated strategy",
			strategy: schedulerconfig.MostAllocated,
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("50"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("75"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
									apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {0: gpuResources},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
							},
						},
						numaTopology: &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {deviceInfoGenerator(schedulingv1alpha1.GPU, 0, gpuResources)},
						},
					},
				},
			},
			wantScore:  75,
			wantStatus: nil,
		},
		{
			name: "requested multiple resources on the remaining resources of the node",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("50"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
					},
					schedulingv1alpha1.RDMA: {
						apiext.ResourceRDMA: resource.MustParse("25"),
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("975"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("975"),
									apiext.ResourceGPUMemory:      resource.MustParse("156Gi"),
								},
							},
							schedulingv1alpha1.RDMA: {
								0: corev1.ResourceList{
									apiext.ResourceRDMA: resource.MustParse("950"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("1000"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("1000"),
									apiext.ResourceGPUMemory:      resource.MustParse("160Gi"),
								},
							},
							schedulingv1alpha1.RDMA: {
								0: corev1.ResourceList{
									apiext.ResourceRDMA: resource.MustParse("1000"),
								},
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
							},
							schedulingv1alpha1.RDMA: {
								0: corev1.ResourceList{
									apiext.ResourceRDMA: resource.MustParse("50"),
								},
							},
						},
						numaTopology: &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {
								deviceInfoGenerator(schedulingv1alpha1.GPU, 0, corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("1000"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("1000"),
									apiext.ResourceGPUMemory:      resource.MustParse("160Gi"),
								}),
							},
							schedulingv1alpha1.RDMA: {
								deviceInfoGenerator(schedulingv1alpha1.RDMA, 0, corev1.ResourceList{apiext.ResourceRDMA: resource.MustParse("50")}),
							},
						},
					},
				},
			},
			wantScore:  184,
			wantStatus: nil,
		},
		{
			name: "score with preemptible",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("50"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
					},
				},
				preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{
					"test-node": {
						schedulingv1alpha1.GPU: {
							0: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("0"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
									apiext.ResourceGPUMemory:      resource.MustParse("0Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {0: gpuResources},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {0: gpuResources},
						},
						numaTopology: &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {deviceInfoGenerator(schedulingv1alpha1.GPU, 0, gpuResources)},
						},
					},
				},
			},
			wantScore:  50,
			wantStatus: nil,
		},
		{
			name: "score with reserved",
			state: &preFilterState{
				skip: false,
				podRequests: map[schedulingv1alpha1.DeviceType]corev1.ResourceList{
					schedulingv1alpha1.GPU: {
						apiext.ResourceGPUCore:        resource.MustParse("50"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
					},
				},
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{Minor: 0, Resources: gpuResources},
				},
			},
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						allocateSet: map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("0"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
									apiext.ResourceGPUMemory:      resource.MustParse("0Gi"),
								},
							},
						},
						deviceTotal: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {0: gpuResources},
						},
						deviceUsed:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos: map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
							schedulingv1alpha1.GPU: {deviceInfoGenerator(schedulingv1alpha1.GPU, 0, gpuResources)},
						},
					},
				},
			},
			wantScore:  50,
			wantStatus: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{testNode})
			args := getDefaultArgs()
			if tt.strategy != "" {
				args.ScoringStrategy.Type = tt.strategy
			}
			p, err := suit.proxyNew(args, suit)
			assert.NoError(t, err)

			pl := p.(*Plugin)
			pl.nodeDeviceCache = tt.nodeDeviceCache

			cycleState := framework.NewCycleState()
			if tt.state != nil {
				cycleState.Write(stateKey, tt.state)
			}
			if tt.reserved != nil {
				reservation := &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						UID:  uuid.NewUUID(),
						Name: "reservation-1",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Template: &corev1.PodTemplateSpec{},
					},
					Status: schedulingv1alpha1.ReservationStatus{
						NodeName: "test-node",
					},
				}
				err := apiext.SetDeviceAllocations(reservation, tt.reserved)
				assert.NoError(t, err)

				tt.nodeDeviceCache.updatePod(nil, reservationutil.NewReservePod(reservation))

				namespacedName := reservationutil.GetReservePodNamespacedName(reservation)
				allocatable := tt.nodeDeviceCache.getNodeDevice("test-node", false).getUsed(namespacedName.Namespace, namespacedName.Name)

				restoreState := &reservationRestoreStateData{
					skip: false,
					nodeToState: frameworkext.NodeReservationRestoreStates{
						"test-node": &nodeReservationRestoreStateData{
							mergedMatchedAllocatable: allocatable,
							matched: []reservationAlloc{
								{
									rInfo:       frameworkext.NewReservationInfo(reservation),
									allocatable: allocatable,
									remained:    allocatable,
								},
							},
						},
					},
				}
				cycleState.Write(reservationRestoreStateKey, restoreState)
			}
			score, status := pl.Score(context.TODO(), cycleState, &corev1.Pod{}, "test-node")
			assert.Equal(t, tt.wantScore, score)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}

func TestScoreExtension(t *testing.T) {
	tests := []struct {
		name          string
		nodeScoreList framework.NodeScoreList
		want          framework.NodeScoreList
	}{
		{
			name: "node score 0",
			nodeScoreList: framework.NodeScoreList{
				{
					Name:  "test-node",
					Score: 0,
				},
			},
			want: framework.NodeScoreList{
				{
					Name:  "test-node",
					Score: 0,
				},
			},
		},
		{
			name: "only one node has score",
			nodeScoreList: framework.NodeScoreList{
				{
					Name:  "test-node",
					Score: 10,
				},
			},
			want: framework.NodeScoreList{
				{
					Name:  "test-node",
					Score: 100,
				},
			},
		},
		{
			name: "node score exceeded maxScore",
			nodeScoreList: framework.NodeScoreList{
				{
					Name:  "test-node",
					Score: 200,
				},
				{
					Name:  "other-test-node",
					Score: 10,
				},
			},
			want: framework.NodeScoreList{
				{
					Name:  "test-node",
					Score: 100,
				},
				{
					Name:  "other-test-node",
					Score: 5,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{nodeDeviceCache: newNodeDeviceCache()}
			status := p.ScoreExtensions().NormalizeScore(context.TODO(), framework.NewCycleState(), &corev1.Pod{}, tt.nodeScoreList)
			assert.True(t, status.IsSuccess())
			assert.Equal(t, tt.want, tt.nodeScoreList)
		})
	}
}

func TestScoreReservation(t *testing.T) {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	gpuResources := corev1.ResourceList{
		apiext.ResourceGPUCore:        resource.MustParse("100"),
		apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
		apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
	}
	rdmaResources := corev1.ResourceList{
		apiext.ResourceRDMA: resource.MustParse("100"),
	}

	deviceTotal := map[schedulingv1alpha1.DeviceType]deviceResources{
		schedulingv1alpha1.GPU:  {0: gpuResources},
		schedulingv1alpha1.RDMA: {0: rdmaResources},
	}

	deviceUsed := func() map[schedulingv1alpha1.DeviceType]deviceResources {
		return map[schedulingv1alpha1.DeviceType]deviceResources{
			schedulingv1alpha1.GPU: {
				0: corev1.ResourceList{
					apiext.ResourceGPUCore:        resource.MustParse("25"),
					apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
					apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
				},
			},
		}
	}

	deviceInfos := map[schedulingv1alpha1.DeviceType][]*schedulingv1alpha1.DeviceInfo{
		schedulingv1alpha1.GPU: {
			{
				Type:      schedulingv1alpha1.GPU,
				Health:    true,
				UUID:      "gpu-123456-0",
				Minor:     pointer.Int32(0),
				Resources: gpuResources,
			},
		},
		schedulingv1alpha1.RDMA: {
			{
				Type:      schedulingv1alpha1.RDMA,
				Health:    true,
				UUID:      "rdma-123456-0",
				Minor:     pointer.Int32(0),
				Resources: rdmaResources,
			},
		},
	}

	tests := []struct {
		name               string
		podRequests        corev1.ResourceList
		preemptibleDevices map[string]map[schedulingv1alpha1.DeviceType]deviceResources
		preemptibleInRRs   map[string]map[types.UID]map[schedulingv1alpha1.DeviceType]deviceResources
		reserved           apiext.DeviceAllocations
		allocatePolicy     schedulingv1alpha1.ReservationAllocatePolicy
		scoreStrategy      schedulerconfig.ScoringStrategyType
		nodeDeviceCache    *nodeDeviceCache
		wantScore          int64
		wantNormalize      *int64
		wantStatus         *framework.Status
	}{
		{
			name: "score reservation with default allocate policy",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal:  deviceTotal,
						deviceUsed:   deviceUsed(),
						deviceFree:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:  25,
			wantStatus: nil,
		},
		{
			name: "score reservation with default allocate policy and MostAllocated",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyDefault,
			scoreStrategy:  schedulerconfig.MostAllocated,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal:  deviceTotal,
						deviceUsed:   deviceUsed(),
						deviceFree:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:  75,
			wantStatus: nil,
		},
		{
			name: "score reservation with aligned allocate policy, some resources of node has allocated, LeastAllocated",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			scoreStrategy:  schedulerconfig.LeastAllocated,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal:  deviceTotal,
						deviceFree:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						deviceUsed:   deviceUsed(),
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:  25,
			wantStatus: nil,
		},
		{
			name: "score reservation with aligned allocate policy, some resources of node has allocated, MostAllocated",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			scoreStrategy:  schedulerconfig.MostAllocated,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal:  deviceTotal,
						deviceFree:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						deviceUsed:   deviceUsed(),
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:  75,
			wantStatus: nil,
		},
		{
			name: "score reservation with restricted allocate policy and LeastAllocated",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			scoreStrategy:  schedulerconfig.LeastAllocated,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal:  deviceTotal,
						deviceFree:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						deviceUsed:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:  0,
			wantStatus: nil,
		},
		{
			name: "score reservation with restricted allocate policy and MostAllocated",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			scoreStrategy:  schedulerconfig.MostAllocated,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal:  deviceTotal,
						deviceFree:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						deviceUsed:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:  100,
			wantStatus: nil,
		},
		{
			name: "score reservation with aligned policy and preemptible",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{
				"test-node": {
					schedulingv1alpha1.GPU: {
						0: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("25"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
						},
					},
				},
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("75"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
							apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal:  deviceTotal,
						deviceFree:   map[schedulingv1alpha1.DeviceType]deviceResources{},
						deviceUsed:   deviceUsed(),
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:  50,
			wantStatus: nil,
		},
		{
			name: "score reservation with restricted policy and preemptible",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			preemptibleDevices: map[string]map[schedulingv1alpha1.DeviceType]deviceResources{
				"test-node": {
					schedulingv1alpha1.GPU: {
						0: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("25"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
							apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
						},
					},
				},
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("75"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("75"),
							apiext.ResourceGPUMemory:      resource.MustParse("12Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyAligned,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal: deviceTotal,
						deviceFree:  map[schedulingv1alpha1.DeviceType]deviceResources{},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("25"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("25"),
									apiext.ResourceGPUMemory:      resource.MustParse("4Gi"),
								},
							},
						},
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:  50,
			wantStatus: nil,
		},
		{
			name: "score reservation with multi resources and MostAllocated",
			podRequests: corev1.ResourceList{
				apiext.ResourceGPUCore:        resource.MustParse("50"),
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
				apiext.ResourceRDMA:           resource.MustParse("20"),
			},
			reserved: apiext.DeviceAllocations{
				schedulingv1alpha1.GPU: []*apiext.DeviceAllocation{
					{
						Minor: 0,
						Resources: corev1.ResourceList{
							apiext.ResourceGPUCore:        resource.MustParse("50"),
							apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
							apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
						},
					},
				},
			},
			allocatePolicy: schedulingv1alpha1.ReservationAllocatePolicyRestricted,
			scoreStrategy:  schedulerconfig.MostAllocated,
			nodeDeviceCache: &nodeDeviceCache{
				nodeDeviceInfos: map[string]*nodeDevice{
					"test-node": {
						deviceTotal: deviceTotal,
						deviceFree: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.RDMA: {
								0: rdmaResources,
							},
						},
						deviceUsed: map[schedulingv1alpha1.DeviceType]deviceResources{
							schedulingv1alpha1.GPU: {
								0: {
									apiext.ResourceGPUCore:        resource.MustParse("50"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
									apiext.ResourceGPUMemory:      resource.MustParse("8Gi"),
								},
							},
						},
						allocateSet:  map[schedulingv1alpha1.DeviceType]map[types.NamespacedName]deviceResources{},
						numaTopology: &NUMATopology{},
						deviceInfos:  deviceInfos,
					},
				},
			},
			wantScore:     120,
			wantNormalize: pointer.Int64(100),
			wantStatus:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, []*corev1.Node{testNode})
			args := getDefaultArgs()
			if tt.scoreStrategy != "" {
				args.ScoringStrategy.Type = tt.scoreStrategy
			}
			p, err := suit.proxyNew(args, suit)
			assert.NoError(t, err)
			pl := p.(*Plugin)
			pl.nodeDeviceCache = tt.nodeDeviceCache

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-pod",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Limits:   tt.podRequests,
								Requests: tt.podRequests,
							},
						},
					},
				},
			}

			cycleState := framework.NewCycleState()
			_, status := pl.PreFilter(context.TODO(), cycleState, pod)
			assert.True(t, status.IsSuccess())

			state, status := getPreFilterState(cycleState)
			assert.True(t, status.IsSuccess())
			if tt.preemptibleDevices != nil {
				state.preemptibleDevices = tt.preemptibleDevices
			}

			var rInfo *frameworkext.ReservationInfo
			if tt.reserved != nil {
				reservation := &schedulingv1alpha1.Reservation{
					ObjectMeta: metav1.ObjectMeta{
						UID:  uuid.NewUUID(),
						Name: "reservation-1",
					},
					Spec: schedulingv1alpha1.ReservationSpec{
						Template:       &corev1.PodTemplateSpec{},
						AllocatePolicy: tt.allocatePolicy,
					},
					Status: schedulingv1alpha1.ReservationStatus{
						NodeName: "test-node",
					},
				}
				err := apiext.SetDeviceAllocations(reservation, tt.reserved)
				assert.NoError(t, err)

				tt.nodeDeviceCache.updatePod(nil, reservationutil.NewReservePod(reservation))

				rInfo = frameworkext.NewReservationInfo(reservation)
				status := pl.PreRestoreReservation(context.TODO(), cycleState, pod)
				assert.True(t, status.IsSuccess())

				nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(testNode.Name)
				assert.NoError(t, err)

				nodeToState, status := pl.RestoreReservation(context.TODO(), cycleState, pod, []*frameworkext.ReservationInfo{rInfo}, nil, nodeInfo)
				assert.True(t, status.IsSuccess())
				status = pl.FinalRestoreReservation(context.TODO(), cycleState, pod, frameworkext.NodeReservationRestoreStates{testNode.Name: nodeToState})
				assert.True(t, status.IsSuccess())
			}
			score, status := pl.ScoreReservation(context.TODO(), cycleState, pod, rInfo, "test-node")
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantScore, score)
			if tt.wantNormalize != nil {
				scoreList := frameworkext.ReservationScoreList{
					{
						Score: score,
					},
				}
				status = pl.ReservationScoreExtensions().NormalizeReservationScore(context.TODO(), cycleState, pod, scoreList)
				assert.True(t, status.IsSuccess())
				assert.Equal(t, *tt.wantNormalize, scoreList[0].Score)
			}
		})
	}
}

func Test_resourceAllocationScorer_scoreDevice(t *testing.T) {
	tests := []struct {
		name      string
		requests  corev1.ResourceList
		total     corev1.ResourceList
		free      corev1.ResourceList
		strategy  schedulerconfig.ScoringStrategyType
		wantScore int64
	}{
		{
			name: "completely idle",
			requests: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			total: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			free: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			wantScore: 50,
		},
		{
			name: "completely used",
			requests: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			total: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			free: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("0"),
			},
			wantScore: 0,
		},
		{
			name: "remaining resources",
			requests: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("30"),
			},
			total: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			free: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			wantScore: 20,
		},
		{
			name: "remaining resources with MostAllocated",
			requests: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("30"),
			},
			total: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
			},
			free: corev1.ResourceList{
				apiext.ResourceGPUMemoryRatio: resource.MustParse("50"),
			},
			strategy:  schedulerconfig.MostAllocated,
			wantScore: 80,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := getDefaultArgs()
			if tt.strategy != "" {
				args.ScoringStrategy.Type = tt.strategy
			}
			scorerFn := deviceResourceStrategyTypeMap[args.ScoringStrategy.Type]
			scorer := scorerFn(args)
			score := scorer.scoreDevice(tt.requests, tt.total, tt.free)
			assert.Equal(t, tt.wantScore, score)
		})
	}
}
