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

package impl

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	faketopologyclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned/fake"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/impl/mock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	fakekoordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	clientsetv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/slo/v1alpha1"
	fakeclientslov1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/slo/v1alpha1/fake"
	listerv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mockmetriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/prediction"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

var _ listerv1alpha1.NodeMetricLister = &fakeNodeMetricLister{}

type fakeNodeMetricLister struct {
	nodeMetrics *slov1alpha1.NodeMetric
	getErr      error
}

func (f *fakeNodeMetricLister) List(selector labels.Selector) (ret []*slov1alpha1.NodeMetric, err error) {
	return []*slov1alpha1.NodeMetric{f.nodeMetrics}, nil
}

func (f *fakeNodeMetricLister) Get(name string) (*slov1alpha1.NodeMetric, error) {
	return f.nodeMetrics, f.getErr
}

func Test_reporter_isNodeMetricInited(t *testing.T) {
	type fields struct {
		nodeMetric *slov1alpha1.NodeMetric
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "is-node-metric-inited",
			fields: fields{
				nodeMetric: &slov1alpha1.NodeMetric{},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &nodeMetricInformer{
				nodeMetric: tt.fields.nodeMetric,
			}
			if got := r.isNodeMetricInited(); got != tt.want {
				t.Errorf("isNodeMetricInited() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_nodeMetricInformer_getNodeMetricReportInterval(t *testing.T) {
	type fields struct {
		nodeMetric *slov1alpha1.NodeMetric
	}
	tests := []struct {
		name   string
		fields fields
		want   time.Duration
	}{
		{
			name: "get report interval from node metric",
			fields: fields{
				nodeMetric: &slov1alpha1.NodeMetric{
					Spec: slov1alpha1.NodeMetricSpec{
						CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
							ReportIntervalSeconds: pointer.Int64(666),
						},
					},
				},
			},
			want: 666 * time.Second,
		},
		{
			name: "get default interval from nil",
			fields: fields{
				nodeMetric: nil,
			},
			want: defaultReportIntervalSeconds * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &nodeMetricInformer{
				nodeMetric: tt.fields.nodeMetric,
			}
			if got := r.getNodeMetricReportInterval(); got != tt.want {
				t.Errorf("getNodeMetricReportInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeNodeMetricClient struct {
	fakeclientslov1alpha1.FakeNodeMetrics
	nodeMetrics map[string]*slov1alpha1.NodeMetric
}

func (c *fakeNodeMetricClient) Get(ctx context.Context, name string, opts metav1.GetOptions) (*slov1alpha1.NodeMetric, error) {
	nodeMetric, ok := c.nodeMetrics[name]
	if !ok {
		return &slov1alpha1.NodeMetric{}, errors.NewNotFound(schema.GroupResource{Group: "slo.koordinator.sh", Resource: "nodemetrics"}, name)
	}
	return nodeMetric, nil
}

func (c *fakeNodeMetricClient) UpdateStatus(ctx context.Context, nodeMetric *slov1alpha1.NodeMetric, opts metav1.UpdateOptions) (*slov1alpha1.NodeMetric, error) {
	currentNodeMetric, ok := c.nodeMetrics[nodeMetric.Name]
	if !ok {
		return &slov1alpha1.NodeMetric{}, errors.NewNotFound(schema.GroupResource{Group: "slo.koordinator.sh", Resource: "nodemetrics"}, nodeMetric.Name)
	}
	currentNodeMetric.Status = nodeMetric.Status
	c.nodeMetrics[nodeMetric.Name] = currentNodeMetric
	return currentNodeMetric, nil
}

// check sync with single node metric in metric cache
func Test_reporter_sync_with_single_node_metric(t *testing.T) {
	endTime := time.Now()
	startTime := endTime.Add(-30 * time.Second)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testNode",
		},
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("4"),
				v1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
	mockStatesInformer := mock.NewMockStatesInformer(ctrl)
	mockStatesInformer.EXPECT().GetNode().Return(node).AnyTimes()
	testNodeSLOInformer := &nodeSLOInformer{
		nodeSLO: &slov1alpha1.NodeSLO{
			Spec: slov1alpha1.NodeSLOSpec{},
		},
		callbackRunner: &callbackRunner{
			statesInformer: mockStatesInformer,
		},
	}

	type fields struct {
		nodeName         string
		nodeMetric       *slov1alpha1.NodeMetric
		metricCache      func(ctrl *gomock.Controller) metriccache.MetricCache
		podsInformer     *podsInformer
		nodeSLOInformer  *nodeSLOInformer
		nodeMetricLister listerv1alpha1.NodeMetricLister
		nodeMetricClient clientsetv1alpha1.NodeMetricInterface
	}
	tests := []struct {
		name               string
		fields             fields
		wantNilStatus      bool
		wantNodeResource   slov1alpha1.ResourceMap
		wantSystemResource slov1alpha1.ResourceMap
		wantPodsMetric     []*slov1alpha1.PodMetricInfo
		wantErr            bool
	}{
		{
			name: "nodeMetric not initialized",
			fields: fields{
				nodeName:   "test",
				nodeMetric: nil,
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					return nil
				},
				podsInformer:     NewPodsInformer(),
				nodeSLOInformer:  NewNodeSLOInformer(),
				nodeMetricLister: nil,
				nodeMetricClient: &fakeNodeMetricClient{},
			},

			wantNilStatus:      true,
			wantNodeResource:   slov1alpha1.ResourceMap{},
			wantSystemResource: slov1alpha1.ResourceMap{},
			wantPodsMetric:     nil,
			wantErr:            true,
		},
		{
			name: "successfully report nodeMetric - sum of pods request < node.capacity",
			fields: fields{
				nodeName: "test",
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: slov1alpha1.NodeMetricSpec{
						CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
							AggregateDurationSeconds: defaultNodeMetricSpec.CollectPolicy.AggregateDurationSeconds,
							NodeAggregatePolicy: &slov1alpha1.AggregatePolicy{
								Durations: []metav1.Duration{
									{
										Duration: 5 * time.Minute,
									},
								},
							},
							NodeMemoryCollectPolicy: defaultNodeMetricSpec.CollectPolicy.NodeMemoryCollectPolicy,
						},
					},
				},
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)

					mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
					metriccache.DefaultAggregateResultFactory = mockResultFactory
					mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
					mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

					duration := endTime.Sub(startTime)
					cpuQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, cpuQueryMeta, 3, duration)

					memQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, memQueryMeta, 3*1024*1024*1024, duration)

					sysCPUQueryMeta, err := metriccache.SystemCPUUsageMetric.BuildQueryMeta(nil)
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, sysCPUQueryMeta, 2, duration)

					sysMemQueryMeta, err := metriccache.SystemMemoryUsageMetric.BuildQueryMeta(nil)
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, sysMemQueryMeta, 2*1024*1024*1024, duration)

					mockMetricCache.EXPECT().Get(gomock.Any()).Return(util.GPUDevices{
						{UUID: "1", Minor: 0, MemoryTotal: 100},
						{UUID: "2", Minor: 1, MemoryTotal: 200},
					}, true).AnyTimes()

					gpu1Core, err := metriccache.NodeGPUCoreUsageMetric.BuildQueryMeta(
						metriccache.MetricPropertiesFunc.GPU("0", "1"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, gpu1Core, 80, endTime.Sub(startTime))
					gpu1Mem, err := metriccache.NodeGPUMemUsageMetric.BuildQueryMeta(
						metriccache.MetricPropertiesFunc.GPU("0", "1"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, gpu1Mem, 30, endTime.Sub(startTime))

					gpu2Core, err := metriccache.NodeGPUCoreUsageMetric.BuildQueryMeta(
						metriccache.MetricPropertiesFunc.GPU("1", "2"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, gpu2Core, 40, endTime.Sub(startTime))
					gpu2Mem, err := metriccache.NodeGPUMemUsageMetric.BuildQueryMeta(
						metriccache.MetricPropertiesFunc.GPU("1", "2"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, gpu2Mem, 50, endTime.Sub(startTime))

					podCPUQueryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod("test-pod"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, podCPUQueryMeta, 1, duration)

					podMemQueryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod("test-pod"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, podMemQueryMeta, 1*1024*1024*1024, duration)

					podGPU1Core, err := metriccache.PodGPUCoreUsageMetric.BuildQueryMeta(
						metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "1"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, podGPU1Core, 80, endTime.Sub(startTime))
					podGPU1Mem, err := metriccache.PodGPUMemUsageMetric.BuildQueryMeta(
						metriccache.MetricPropertiesFunc.PodGPU("test-pod", "0", "1"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, podGPU1Mem, 30, endTime.Sub(startTime))

					podGPU2Core, err := metriccache.PodGPUCoreUsageMetric.BuildQueryMeta(
						metriccache.MetricPropertiesFunc.PodGPU("test-pod", "1", "2"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, podGPU2Core, 40, endTime.Sub(startTime))
					podGPU2Mem, err := metriccache.PodGPUMemUsageMetric.BuildQueryMeta(
						metriccache.MetricPropertiesFunc.PodGPU("test-pod", "1", "2"))
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, podGPU2Mem, 50, endTime.Sub(startTime))
					return mockMetricCache
				},
				podsInformer: &podsInformer{
					podMap: map[string]*statesinformer.PodMeta{
						"default/test-pod": {
							Pod: &v1.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test-pod",
									Namespace: "default",
									UID:       "test-pod",
								},
								Status: v1.PodStatus{
									QOSClass: v1.PodQOSBurstable,
								},
							},
						},
					},
				},
				nodeSLOInformer: testNodeSLOInformer,
				nodeMetricLister: &fakeNodeMetricLister{
					nodeMetrics: &slov1alpha1.NodeMetric{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test",
						},
					},
				},
				nodeMetricClient: &fakeNodeMetricClient{
					nodeMetrics: map[string]*slov1alpha1.NodeMetric{
						"test": {
							ObjectMeta: metav1.ObjectMeta{
								Name: "test",
							},
						},
					},
				},
			},
			wantNilStatus: false,
			wantNodeResource: slov1alpha1.ResourceMap{
				ResourceList: v1.ResourceList{
					v1.ResourceCPU:    *resource.NewMilliQuantity(3000, resource.DecimalSI),
					v1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI),
				},
				Devices: []schedulingv1alpha1.DeviceInfo{
					{UUID: "1", Minor: pointer.Int32(0), Type: schedulingv1alpha1.GPU, Resources: map[v1.ResourceName]resource.Quantity{
						apiext.ResourceGPUCore:        *resource.NewQuantity(80, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(30, resource.BinarySI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(30, resource.DecimalSI),
					}},
					{UUID: "2", Minor: pointer.Int32(1), Type: schedulingv1alpha1.GPU, Resources: map[v1.ResourceName]resource.Quantity{
						apiext.ResourceGPUCore:        *resource.NewQuantity(40, resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(50, resource.BinarySI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(25, resource.DecimalSI),
					}},
				},
			},
			wantSystemResource: slov1alpha1.ResourceMap{
				ResourceList: v1.ResourceList{
					v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
					v1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
				},
			},
			wantPodsMetric: []*slov1alpha1.PodMetricInfo{
				{
					Name:      "test-pod",
					Namespace: "default",
					Priority:  apiext.PriorityProd,
					QoS:       apiext.QoSLS,
					PodUsage: slov1alpha1.ResourceMap{
						ResourceList: v1.ResourceList{
							v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
							v1.ResourceMemory: *resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
						},
						Devices: []schedulingv1alpha1.DeviceInfo{
							{UUID: "1", Minor: pointer.Int32(0), Type: schedulingv1alpha1.GPU, Resources: map[v1.ResourceName]resource.Quantity{
								apiext.ResourceGPUCore:        *resource.NewQuantity(80, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(30, resource.BinarySI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(30, resource.DecimalSI),
							}},
							{UUID: "2", Minor: pointer.Int32(1), Type: schedulingv1alpha1.GPU, Resources: map[v1.ResourceName]resource.Quantity{
								apiext.ResourceGPUCore:        *resource.NewQuantity(40, resource.DecimalSI),
								apiext.ResourceGPUMemory:      *resource.NewQuantity(50, resource.BinarySI),
								apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(25, resource.DecimalSI),
							}},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "skip for nodeMetric not found",
			fields: fields{
				nodeName: "test",
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: defaultNodeMetricSpec,
				},
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					c := mockmetriccache.NewMockMetricCache(ctrl)
					mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
					metriccache.DefaultAggregateResultFactory = mockResultFactory
					mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
					c.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

					duration := endTime.Sub(startTime)
					cpuQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, cpuQueryMeta, 2000, duration)

					memQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, memQueryMeta, 2*1024*1024*1024, duration)

					sysCPUQueryMeta, err := metriccache.SystemCPUUsageMetric.BuildQueryMeta(nil)
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, sysCPUQueryMeta, 2, duration)

					sysMemQueryMeta, err := metriccache.SystemMemoryUsageMetric.BuildQueryMeta(nil)
					assert.NoError(t, err)
					buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, sysMemQueryMeta, 2*1024*1024*1024, duration)

					c.EXPECT().Get(gomock.Any()).Return(nil, false).AnyTimes()
					return c
				},
				podsInformer:    NewPodsInformer(),
				nodeSLOInformer: testNodeSLOInformer,
				nodeMetricLister: &fakeNodeMetricLister{
					nodeMetrics: &slov1alpha1.NodeMetric{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test",
						},
					},
					getErr: errors.NewNotFound(schema.GroupResource{Group: "slo.koordinator.sh", Resource: "nodemetrics"}, "test"),
				},
				nodeMetricClient: &fakeNodeMetricClient{
					nodeMetrics: map[string]*slov1alpha1.NodeMetric{
						"test": {
							ObjectMeta: metav1.ObjectMeta{
								Name: "test",
							},
						},
					},
				},
			},
			wantNilStatus:    true,
			wantPodsMetric:   nil,
			wantNodeResource: slov1alpha1.ResourceMap{},
			wantErr:          false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			r := &nodeMetricInformer{
				nodeName:         tt.fields.nodeName,
				nodeMetric:       tt.fields.nodeMetric,
				metricCache:      tt.fields.metricCache(ctrl),
				podsInformer:     tt.fields.podsInformer,
				nodeSLOInformer:  tt.fields.nodeSLOInformer,
				nodeMetricLister: tt.fields.nodeMetricLister,
				statusUpdater:    newStatusUpdater(tt.fields.nodeMetricClient),
				predictorFactory: prediction.NewEmptyPredictorFactory(),
			}

			r.sync()

			nodeMetric, err := r.statusUpdater.nodeMetricClient.Get(context.TODO(), tt.fields.nodeName, metav1.GetOptions{})
			assert.Equal(t, tt.wantErr, err != nil)
			if !tt.wantErr {
				assert.NotNil(t, nodeMetric)
				if tt.wantNilStatus {
					assert.Nil(t, nodeMetric.Status.NodeMetric)
					assert.Nil(t, nodeMetric.Status.PodsMetric)
				} else {
					assert.Equal(t, tt.wantNodeResource, nodeMetric.Status.NodeMetric.NodeUsage)
					assert.Equal(t, tt.wantSystemResource, nodeMetric.Status.NodeMetric.SystemUsage)
					assert.Equal(t, tt.wantPodsMetric, nodeMetric.Status.PodsMetric)
				}
			}
		})
	}
}

func Test_nodeMetricInformer_collectNodeAggregateMetric(t *testing.T) {
	end := time.Now()
	start := end.Add(-defaultAggregateDurationSeconds * time.Second)
	type fields struct {
		nodeResultAVG slov1alpha1.ResourceMap
		nodeResultP50 slov1alpha1.ResourceMap
		nodeResultP90 slov1alpha1.ResourceMap
		nodeResultP95 slov1alpha1.ResourceMap
		nodeResultP99 slov1alpha1.ResourceMap
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "merge node metric",
			fields: fields{
				nodeResultAVG: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(1, resource.BinarySI),
					},
				},
				nodeResultP50: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(2, resource.BinarySI),
					},
				},
				nodeResultP90: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(3000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(3, resource.BinarySI),
					},
				},
				nodeResultP95: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(4, resource.BinarySI),
					},
				},
				nodeResultP99: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(5000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(5, resource.BinarySI),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)
			mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()
			mockMetricCache.EXPECT().Get(gomock.Any()).Return(nil, false).AnyTimes()
			result := mockmetriccache.NewMockAggregateResult(ctrl)
			result.EXPECT().Value(metriccache.AggregationTypeAVG).Return(float64(1), nil).AnyTimes()
			result.EXPECT().Value(metriccache.AggregationTypeP50).Return(float64(2), nil).AnyTimes()
			result.EXPECT().Value(metriccache.AggregationTypeP90).Return(float64(3), nil).AnyTimes()
			result.EXPECT().Value(metriccache.AggregationTypeP95).Return(float64(4), nil).AnyTimes()
			result.EXPECT().Value(metriccache.AggregationTypeP99).Return(float64(5), nil).AnyTimes()
			result.EXPECT().Count().Return(1).AnyTimes()
			result.EXPECT().TimeRangeDuration().Return(end.Sub(start)).AnyTimes()
			mockResultFactory.EXPECT().New(gomock.Any()).Return(result).AnyTimes()
			mockQuerier.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			mockQuerier.EXPECT().Close().AnyTimes()
			r := &nodeMetricInformer{
				metricCache: mockMetricCache,
				nodeMetric: &slov1alpha1.NodeMetric{
					Spec: slov1alpha1.NodeMetricSpec{
						CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
							AggregateDurationSeconds: defaultNodeMetricSpec.CollectPolicy.AggregateDurationSeconds,
							ReportIntervalSeconds:    defaultNodeMetricSpec.CollectPolicy.ReportIntervalSeconds,
							NodeAggregatePolicy: &slov1alpha1.AggregatePolicy{
								Durations: []metav1.Duration{
									{Duration: 5 * time.Minute},
								},
							},
							NodeMemoryCollectPolicy: defaultNodeMetricSpec.CollectPolicy.NodeMemoryCollectPolicy,
						},
					},
				},
			}
			want := &slov1alpha1.NodeMetricInfo{
				NodeUsage: tt.fields.nodeResultAVG,
				AggregatedNodeUsages: []slov1alpha1.AggregatedUsage{
					{
						Usage: map[apiext.AggregationType]slov1alpha1.ResourceMap{
							apiext.P50: tt.fields.nodeResultP50,
							apiext.P90: tt.fields.nodeResultP90,
							apiext.P95: tt.fields.nodeResultP95,
							apiext.P99: tt.fields.nodeResultP99,
						},
						Duration: metav1.Duration{
							Duration: end.Sub(start),
						},
					},
				},
			}
			got := r.collectNodeAggregateMetric(end, r.nodeMetric.Spec.CollectPolicy.NodeAggregatePolicy)
			assert.Equal(t, want.AggregatedNodeUsages, got)
		})
	}
}

func Test_nodeMetricInformer_updateMetricSpec(t *testing.T) {
	type fields struct {
		nodeMetric *slov1alpha1.NodeMetric
	}
	type args struct {
		newNodeMetric *slov1alpha1.NodeMetric
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   *slov1alpha1.NodeMetricSpec
	}{
		{
			name: "old and new are nil, do nothing for old",
			fields: fields{
				nodeMetric: &slov1alpha1.NodeMetric{
					Spec: slov1alpha1.NodeMetricSpec{
						CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{},
					},
				},
			},
			args: args{
				newNodeMetric: nil,
			},
			want: &slov1alpha1.NodeMetricSpec{
				CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{},
			},
		},
		{
			name: "new is empty, set old as default",
			fields: fields{
				nodeMetric: nil,
			},
			args: args{
				newNodeMetric: &slov1alpha1.NodeMetric{
					Spec: slov1alpha1.NodeMetricSpec{
						CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{},
					},
				},
			},
			want: &defaultNodeMetricSpec,
		},
		{
			name: "new is defined, merge default and set to old",
			fields: fields{
				nodeMetric: nil,
			},
			args: args{
				newNodeMetric: &slov1alpha1.NodeMetric{
					Spec: slov1alpha1.NodeMetricSpec{
						CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
							ReportIntervalSeconds: pointer.Int64(180),
						},
					},
				},
			},
			want: &slov1alpha1.NodeMetricSpec{
				CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
					AggregateDurationSeconds: defaultNodeMetricSpec.CollectPolicy.AggregateDurationSeconds,
					ReportIntervalSeconds:    pointer.Int64(180),
					NodeAggregatePolicy:      defaultNodeMetricSpec.CollectPolicy.NodeAggregatePolicy,
					NodeMemoryCollectPolicy:  defaultNodeMetricSpec.CollectPolicy.NodeMemoryCollectPolicy,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &nodeMetricInformer{
				nodeMetric: tt.fields.nodeMetric,
			}
			r.updateMetricSpec(tt.args.newNodeMetric)
			assert.Equal(t, &r.nodeMetric.Spec, tt.want, "node metric spec should equal")
		})
	}
}

func Test_nodeMetricInformer_NewAndSetup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	type args struct {
		ctx   *PluginOption
		state *PluginState
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "new and setup node metric",
			args: args{
				ctx: &PluginOption{
					config:      NewDefaultConfig(),
					KubeClient:  fakeclientset.NewSimpleClientset(),
					KoordClient: fakekoordclientset.NewSimpleClientset(),
					TopoClient:  faketopologyclientset.NewSimpleClientset(),
					NodeName:    "test-node",
				},
				state: &PluginState{
					metricCache: mockmetriccache.NewMockMetricCache(ctrl),
					informerPlugins: map[PluginName]informerPlugin{
						podsInformerName:    NewPodsInformer(),
						nodeSLOInformerName: NewNodeSLOInformer(),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewNodeMetricInformer()
			assert.NotPanics(t, func() {
				r.Setup(tt.args.ctx, tt.args.state)
			})
		})
	}
}

func Test_metricsInColdStart(t *testing.T) {
	queryEnd := time.Now()
	queryDuration := time.Minute * 10
	queryStart := queryEnd.Add(-queryDuration)
	shortDuration := time.Duration(int64(float64(queryDuration) * float64(validateTimeRangeRatio) / 2))
	shortStart := queryEnd.Add(-shortDuration)
	type args struct {
		queryStart time.Time
		queryEnd   time.Time
		duration   time.Duration
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "metric in cold start",
			args: args{
				queryStart: queryStart,
				queryEnd:   queryEnd,
				duration:   queryEnd.Sub(shortStart),
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := metricsInColdStart(queryStart, queryEnd, tt.args.duration); got != tt.want {
				t.Errorf("metricsInColdStart() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_nodeMetricInformer_collectNodeGPUMetric(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	now := time.Now()
	startTime := now.Add(-time.Second * 120)
	type args struct {
		queryparam metriccache.QueryParam
		gpus       util.GPUDevices
	}
	type samples struct {
		UUID      string
		Minor     int32
		CoreUsage int64
		MemUsage  int64
	}
	tests := []struct {
		name    string
		args    args
		samples map[string]samples
		want    []schedulingv1alpha1.DeviceInfo
	}{
		{
			name: "test-1",
			args: args{
				queryparam: metriccache.QueryParam{
					Aggregate: metriccache.AggregationTypeAVG,
					End:       &now,
					Start:     &startTime,
				},
				gpus: util.GPUDevices{
					{Minor: 0, UUID: "1", MemoryTotal: 8000},
					{Minor: 1, UUID: "2", MemoryTotal: 10000},
				},
			},
			samples: map[string]samples{
				"1": {Minor: 0, UUID: "1", CoreUsage: 80, MemUsage: 800},
				"2": {Minor: 1, UUID: "2", CoreUsage: 10, MemUsage: 100},
			},
			want: []schedulingv1alpha1.DeviceInfo{
				{
					UUID:  "1",
					Minor: pointer.Int32(0),
					Type:  schedulingv1alpha1.GPU,
					Resources: map[v1.ResourceName]resource.Quantity{
						apiext.ResourceGPUCore:        *resource.NewQuantity(int64(80), resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(int64(800), resource.BinarySI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(int64(10), resource.DecimalSI),
					},
				},
				{
					UUID:  "2",
					Minor: pointer.Int32(1),
					Type:  schedulingv1alpha1.GPU,
					Resources: map[v1.ResourceName]resource.Quantity{
						apiext.ResourceGPUCore:        *resource.NewQuantity(int64(10), resource.DecimalSI),
						apiext.ResourceGPUMemory:      *resource.NewQuantity(int64(100), resource.BinarySI),
						apiext.ResourceGPUMemoryRatio: *resource.NewQuantity(int64(1), resource.DecimalSI),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)

			mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			duration := tt.args.queryparam.End.Sub(*tt.args.queryparam.Start)
			for _, g := range tt.samples {
				coreQueryMeta, err := metriccache.NodeGPUCoreUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.GPU(fmt.Sprintf("%d", g.Minor), g.UUID))
				assert.NoError(t, err)
				buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, coreQueryMeta, float64(g.CoreUsage), duration)

				memQueryMeta, err := metriccache.NodeGPUMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.GPU(fmt.Sprintf("%d", g.Minor), g.UUID))
				assert.NoError(t, err)
				buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, memQueryMeta, float64(g.MemUsage), duration)
			}

			r := &nodeMetricInformer{
				metricCache: mockMetricCache,
			}
			got, err := r.collectNodeGPUMetric(tt.args.queryparam, tt.args.gpus)
			assert.NoError(t, err)
			assert.Equalf(t, tt.want, got, "collectNodeGPUMetric(%v, %v)", tt.args.queryparam, tt.args.gpus)
		})
	}
}

func Test_nodeMetricInformer_collectNodeMetric(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	now := time.Now()
	startTime := now.Add(-time.Second * 120)

	type args struct {
		queryparam          metriccache.QueryParam
		memoryCollectPolicy slov1alpha1.NodeMemoryCollectPolicy
	}
	type samples struct {
		CPUUsed float64
		MemUsed float64
	}
	tests := []struct {
		name    string
		args    args
		samples samples
		want    v1.ResourceList
		want1   time.Duration
	}{
		{
			name: "test-1 report usageWithoutPageCache",
			args: args{
				queryparam:          metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
				memoryCollectPolicy: "usageWithoutPageCache",
			},
			samples: samples{
				CPUUsed: 2,
				MemUsed: 10 * 1024 * 1024 * 1024,
			},
			want: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
			},
			want1: now.Sub(startTime),
		},
		{
			name: "test-2 report usageWithHotPageCache",
			args: args{
				queryparam:          metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
				memoryCollectPolicy: "usageWithHotPageCache",
			},
			samples: samples{
				CPUUsed: 2,
				MemUsed: 10 * 1024 * 1024 * 1024,
			},
			want: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
			},
			want1: now.Sub(startTime),
		},
		{
			name: "test-3 report usageWithPageCache",
			args: args{
				queryparam:          metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
				memoryCollectPolicy: "usageWithPageCache",
			},
			samples: samples{
				CPUUsed: 2,
				MemUsed: 10 * 1024 * 1024 * 1024,
			},
			want: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
			},
			want1: now.Sub(startTime),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.args.memoryCollectPolicy == slov1alpha1.UsageWithHotPageCache {
				system.SetIsStartColdMemory(true)
			}
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)
			mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			duration := tt.args.queryparam.End.Sub(*tt.args.queryparam.Start)
			cpuQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, cpuQueryMeta, tt.samples.CPUUsed, duration)

			memQueryMeta, err := metriccache.NodeMemoryUsageMetric.BuildQueryMeta(nil)
			if tt.args.memoryCollectPolicy == slov1alpha1.UsageWithHotPageCache {
				memQueryMeta, err = metriccache.NodeMemoryWithHotPageUsageMetric.BuildQueryMeta(nil)
			} else if tt.args.memoryCollectPolicy == slov1alpha1.UsageWithPageCache {
				memQueryMeta, err = metriccache.NodeMemoryUsageWithPageCacheMetric.BuildQueryMeta(nil)
			}
			assert.NoError(t, err)
			buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, memQueryMeta, tt.samples.MemUsed, duration)
			r := &nodeMetricInformer{
				metricCache: mockMetricCache,
			}
			r.getNodeMetricSpec().CollectPolicy.NodeMemoryCollectPolicy = &tt.args.memoryCollectPolicy
			got, got1, err := r.collectNodeMetric(tt.args.queryparam)
			assert.NoError(t, err)
			assert.Equalf(t, tt.want, got, "collectNodeMetric(%v)", tt.args.queryparam)
			assert.Equalf(t, tt.want1, got1, "collectNodeMetric(%v)", tt.args.queryparam)
		})
	}
}

func Test_nodeMetricInformer_collectPodMetric(t *testing.T) {
	now := time.Now()
	startTime := now.Add(-time.Second * 120)

	type args struct {
		queryparam          metriccache.QueryParam
		memoryCollectPolicy slov1alpha1.NodeMemoryCollectPolicy
		pod                 *statesinformer.PodMeta
	}
	type samples struct {
		CPUUsed float64
		MemUsed float64
	}
	tests := []struct {
		name    string
		args    args
		samples samples
		wantErr bool
		want    *slov1alpha1.PodMetricInfo
	}{
		{
			name: "report usageWithoutPageCache",
			args: args{
				queryparam:          metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
				memoryCollectPolicy: slov1alpha1.UsageWithoutPageCache,
				pod: &statesinformer.PodMeta{
					Pod: &v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod",
							Namespace: "default",
							UID:       "test-pod",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSLSR),
							},
						},
						Spec: v1.PodSpec{
							Priority: pointer.Int32(apiext.PriorityProdValueMax),
						},
					},
				},
			},
			samples: samples{
				CPUUsed: 2,
				MemUsed: 10 * 1024 * 1024 * 1024,
			},
			want: &slov1alpha1.PodMetricInfo{
				Name:      "test-pod",
				Namespace: "default",
				Priority:  apiext.PriorityProd,
				QoS:       apiext.QoSLSR,
				PodUsage: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
					},
				},
			},
		},
		{
			name: "report usageWithHotPageCache",
			args: args{
				queryparam:          metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
				memoryCollectPolicy: slov1alpha1.UsageWithHotPageCache,
				pod: &statesinformer.PodMeta{
					Pod: &v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod",
							Namespace: "default",
							UID:       "test-pod",
							Labels: map[string]string{
								apiext.LabelPodQoS: string(apiext.QoSLS),
							},
						},
						Spec: v1.PodSpec{
							Priority: pointer.Int32(apiext.PriorityBatchValueMin),
						},
					},
				},
			},
			samples: samples{
				CPUUsed: 2,
				MemUsed: 10 * 1024 * 1024 * 1024,
			},
			want: &slov1alpha1.PodMetricInfo{
				Name:      "test-pod",
				Namespace: "default",
				Priority:  apiext.PriorityBatch,
				QoS:       apiext.QoSLS,
				PodUsage: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
					},
				},
			},
		},
		{
			name: "report usageWithPageCache",
			args: args{
				queryparam:          metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
				memoryCollectPolicy: slov1alpha1.UsageWithPageCache,
				pod: &statesinformer.PodMeta{
					Pod: &v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod",
							Namespace: "default",
							UID:       "test-pod",
						},
					},
				},
			},
			samples: samples{
				CPUUsed: 2,
				MemUsed: 10 * 1024 * 1024 * 1024,
			},
			want: &slov1alpha1.PodMetricInfo{
				Name:      "test-pod",
				Namespace: "default",
				Priority:  apiext.PriorityBatch,
				QoS:       apiext.QoSBE,
				PodUsage: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			if tt.args.memoryCollectPolicy == slov1alpha1.UsageWithHotPageCache {
				system.SetIsStartColdMemory(true)
			}
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)
			mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			duration := tt.args.queryparam.End.Sub(*tt.args.queryparam.Start)
			cpuQueryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(tt.args.pod.Pod.UID)))
			assert.NoError(t, err)
			buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, cpuQueryMeta, tt.samples.CPUUsed, duration)

			memQueryMeta, err := metriccache.PodMemUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(tt.args.pod.Pod.UID)))
			if tt.args.memoryCollectPolicy == slov1alpha1.UsageWithHotPageCache {
				memQueryMeta, err = metriccache.PodMemoryWithHotPageUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(tt.args.pod.Pod.UID)))
			} else if tt.args.memoryCollectPolicy == slov1alpha1.UsageWithPageCache {
				memQueryMeta, err = metriccache.PodMemoryUsageWithPageCacheMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(tt.args.pod.Pod.UID)))
			}
			assert.NoError(t, err)
			buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, memQueryMeta, tt.samples.MemUsed, duration)

			r := &nodeMetricInformer{
				metricCache: mockMetricCache,
				nodeMetric: &slov1alpha1.NodeMetric{
					Spec: slov1alpha1.NodeMetricSpec{
						CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
							NodeMemoryCollectPolicy: &tt.args.memoryCollectPolicy,
						},
					},
				},
			}
			got, err := r.collectPodMetric(tt.args.pod, tt.args.queryparam)
			assert.Equal(t, tt.wantErr, err != nil, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func buildMockQueryResult(ctrl *gomock.Controller, querier *mockmetriccache.MockQuerier, factory *mockmetriccache.MockAggregateResultFactory,
	queryMeta metriccache.MetricMeta, value float64, duration time.Duration) {
	result := mockmetriccache.NewMockAggregateResult(ctrl)
	result.EXPECT().Value(gomock.Any()).Return(value, nil).AnyTimes()
	result.EXPECT().Count().Return(1).AnyTimes()
	result.EXPECT().TimeRangeDuration().Return(duration).AnyTimes()
	factory.EXPECT().New(queryMeta).Return(result).AnyTimes()
	querier.EXPECT().Query(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()
	querier.EXPECT().QueryAndClose(queryMeta, gomock.Any(), result).SetArg(2, *result).Return(nil).AnyTimes()
	querier.EXPECT().Close().AnyTimes()
}

func Test_nodeMetricInformer_collectSystemAggregateMetric(t *testing.T) {
	end := time.Now()
	start := end.Add(-defaultAggregateDurationSeconds * time.Second)
	type fields struct {
		sysResultAVG slov1alpha1.ResourceMap
		sysResultP50 slov1alpha1.ResourceMap
		sysResultP90 slov1alpha1.ResourceMap
		sysResultP95 slov1alpha1.ResourceMap
		sysResultP99 slov1alpha1.ResourceMap
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "merge system metric",
			fields: fields{
				sysResultAVG: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(1, resource.BinarySI),
					},
				},
				sysResultP50: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(2, resource.BinarySI),
					},
				},
				sysResultP90: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(3000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(3, resource.BinarySI),
					},
				},
				sysResultP95: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(4, resource.BinarySI),
					},
				},
				sysResultP99: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(5000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(5, resource.BinarySI),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)
			mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()
			mockMetricCache.EXPECT().Get(gomock.Any()).Return(nil, false).AnyTimes()
			result := mockmetriccache.NewMockAggregateResult(ctrl)
			result.EXPECT().Value(metriccache.AggregationTypeAVG).Return(float64(1), nil).AnyTimes()
			result.EXPECT().Value(metriccache.AggregationTypeP50).Return(float64(2), nil).AnyTimes()
			result.EXPECT().Value(metriccache.AggregationTypeP90).Return(float64(3), nil).AnyTimes()
			result.EXPECT().Value(metriccache.AggregationTypeP95).Return(float64(4), nil).AnyTimes()
			result.EXPECT().Value(metriccache.AggregationTypeP99).Return(float64(5), nil).AnyTimes()
			result.EXPECT().Count().Return(1).AnyTimes()
			result.EXPECT().TimeRangeDuration().Return(end.Sub(start)).AnyTimes()
			mockResultFactory.EXPECT().New(gomock.Any()).Return(result).AnyTimes()
			mockQuerier.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			mockQuerier.EXPECT().Close().AnyTimes()
			r := &nodeMetricInformer{
				metricCache: mockMetricCache,
				nodeMetric: &slov1alpha1.NodeMetric{
					Spec: slov1alpha1.NodeMetricSpec{
						CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
							AggregateDurationSeconds: defaultNodeMetricSpec.CollectPolicy.AggregateDurationSeconds,
							ReportIntervalSeconds:    defaultNodeMetricSpec.CollectPolicy.ReportIntervalSeconds,
							NodeAggregatePolicy: &slov1alpha1.AggregatePolicy{
								Durations: []metav1.Duration{
									{Duration: 5 * time.Minute},
								},
							},
						},
					},
				},
			}
			want := &slov1alpha1.NodeMetricInfo{
				NodeUsage: tt.fields.sysResultAVG,
				AggregatedNodeUsages: []slov1alpha1.AggregatedUsage{
					{
						Usage: map[apiext.AggregationType]slov1alpha1.ResourceMap{
							apiext.P50: tt.fields.sysResultP50,
							apiext.P90: tt.fields.sysResultP90,
							apiext.P95: tt.fields.sysResultP95,
							apiext.P99: tt.fields.sysResultP99,
						},
						Duration: metav1.Duration{
							Duration: end.Sub(start),
						},
					},
				},
			}
			got := r.collectSystemAggregateMetric(end, r.nodeMetric.Spec.CollectPolicy.NodeAggregatePolicy)
			assert.Equal(t, want.AggregatedNodeUsages, got)
		})
	}
}

func Test_nodeMetricInformer_collectSystemMetric(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	now := time.Now()
	startTime := now.Add(-time.Second * 120)

	type args struct {
		queryparam metriccache.QueryParam
	}
	type samples struct {
		CPUUsed float64
		MemUsed float64
	}
	tests := []struct {
		name    string
		args    args
		samples samples
		want    v1.ResourceList
		want1   time.Duration
	}{
		{
			name: "test-1",
			args: args{
				queryparam: metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
			},
			samples: samples{
				CPUUsed: 2,
				MemUsed: 10 * 1024 * 1024 * 1024,
			},
			want: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
			},
			want1: now.Sub(startTime),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)
			mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			duration := tt.args.queryparam.End.Sub(*tt.args.queryparam.Start)
			cpuQueryMeta, err := metriccache.SystemCPUUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, cpuQueryMeta, tt.samples.CPUUsed, duration)

			memQueryMeta, err := metriccache.SystemMemoryUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, memQueryMeta, tt.samples.MemUsed, duration)
			r := &nodeMetricInformer{
				metricCache: mockMetricCache,
			}
			got, got1, err := r.collectSystemMetric(tt.args.queryparam)
			assert.NoError(t, err)
			assert.Equalf(t, tt.want, got, "collectSystemMetric(%v)", tt.args.queryparam)
			assert.Equalf(t, tt.want1, got1, "collectSystemMetric(%v)", tt.args.queryparam)
		})
	}
}

func Test_nodeMetricInformer_collectHostAppMetric(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	now := time.Now()
	startTime := now.Add(-time.Second * 120)

	type fields struct {
		memoryCollectPolicy slov1alpha1.NodeMemoryCollectPolicy
		hostAppCPU          float64
		hostAppMemory       float64
	}
	type args struct {
		hostApp    *slov1alpha1.HostApplicationSpec
		queryParam metriccache.QueryParam
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *slov1alpha1.HostApplicationMetricInfo
		wantErr bool
	}{
		{
			name:   "return error for nil host app",
			fields: fields{},
			args: args{
				queryParam: metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "get host app metric with memory without page cache",
			fields: fields{
				memoryCollectPolicy: slov1alpha1.UsageWithoutPageCache,
				hostAppCPU:          1,
				hostAppMemory:       1024,
			},
			args: args{
				hostApp: &slov1alpha1.HostApplicationSpec{
					Name:     "test-host-app",
					Priority: apiext.PriorityBatch,
					QoS:      apiext.QoSBE,
				},
				queryParam: metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
			},
			want: &slov1alpha1.HostApplicationMetricInfo{
				Name: "test-host-app",
				Usage: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(int64(1000), resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(int64(1024), resource.BinarySI),
					},
				},
				Priority: apiext.PriorityBatch,
				QoS:      apiext.QoSBE,
			},
			wantErr: false,
		},
		{
			name: "get host app metric with memory with page cache",
			fields: fields{
				memoryCollectPolicy: slov1alpha1.UsageWithPageCache,
				hostAppCPU:          1,
				hostAppMemory:       1024,
			},
			args: args{
				hostApp: &slov1alpha1.HostApplicationSpec{
					Name:     "test-host-app",
					Priority: apiext.PriorityBatch,
					QoS:      apiext.QoSBE,
				},
				queryParam: metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
			},
			want: &slov1alpha1.HostApplicationMetricInfo{
				Name: "test-host-app",
				Usage: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(int64(1000), resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(int64(1024), resource.BinarySI),
					},
				},
				Priority: apiext.PriorityBatch,
				QoS:      apiext.QoSBE,
			},
			wantErr: false,
		},
		{
			name: "get host app metric with memory with hot page cache",
			fields: fields{
				memoryCollectPolicy: slov1alpha1.UsageWithHotPageCache,
				hostAppCPU:          1,
				hostAppMemory:       1024,
			},
			args: args{
				hostApp: &slov1alpha1.HostApplicationSpec{
					Name:     "test-host-app",
					Priority: apiext.PriorityBatch,
					QoS:      apiext.QoSBE,
				},
				queryParam: metriccache.QueryParam{Start: &startTime, End: &now, Aggregate: metriccache.AggregationTypeAVG},
			},
			want: &slov1alpha1.HostApplicationMetricInfo{
				Name: "test-host-app",
				Usage: slov1alpha1.ResourceMap{
					ResourceList: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(int64(1000), resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(int64(1024), resource.BinarySI),
					},
				},
				Priority: apiext.PriorityBatch,
				QoS:      apiext.QoSBE,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.fields.memoryCollectPolicy == slov1alpha1.UsageWithHotPageCache {
				system.SetIsStartColdMemory(true)
			}
			mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)
			mockResultFactory := mockmetriccache.NewMockAggregateResultFactory(ctrl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mockmetriccache.NewMockQuerier(ctrl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			if tt.args.hostApp != nil {

				duration := tt.args.queryParam.End.Sub(*tt.args.queryParam.Start)
				cpuQueryMeta, err := metriccache.HostAppCPUUsageMetric.BuildQueryMeta(
					metriccache.MetricPropertiesFunc.HostApplication(tt.args.hostApp.Name))
				assert.NoError(t, err)
				buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, cpuQueryMeta, tt.fields.hostAppCPU, duration)

				memQueryMeta, err := metriccache.HostAppMemoryUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.HostApplication(tt.args.hostApp.Name))
				if tt.fields.memoryCollectPolicy == slov1alpha1.UsageWithHotPageCache {
					memQueryMeta, err = metriccache.HostAppMemoryWithHotPageUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.HostApplication(tt.args.hostApp.Name))
				} else if tt.fields.memoryCollectPolicy == slov1alpha1.UsageWithPageCache {
					memQueryMeta, err = metriccache.HostAppMemoryUsageWithPageCacheMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.HostApplication(tt.args.hostApp.Name))
				}
				assert.NoError(t, err)
				buildMockQueryResult(ctrl, mockQuerier, mockResultFactory, memQueryMeta, tt.fields.hostAppMemory, duration)
			}

			r := &nodeMetricInformer{
				metricCache: mockMetricCache,
			}

			r.getNodeMetricSpec().CollectPolicy.NodeMemoryCollectPolicy = &tt.fields.memoryCollectPolicy

			got, err := r.collectHostAppMetric(tt.args.hostApp, tt.args.queryParam)
			if (err != nil) != tt.wantErr {
				t.Errorf("collectHostAppMetric() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("collectHostAppMetric() got = %v, want %v", got, tt.want)
			}
		})
	}
}
