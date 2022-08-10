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

package reporter

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/pointer"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	clientbeta1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/slo/v1alpha1"
	fakeclientslov1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/slo/v1alpha1/fake"
	listerbeta1 "github.com/koordinator-sh/koordinator/pkg/client/listers/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
)

var _ listerbeta1.NodeMetricLister = &fakeNodeMetricLister{}

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
			r := &reporter{
				nodeMetric: tt.fields.nodeMetric,
			}
			if got := r.isNodeMetricInited(); got != tt.want {
				t.Errorf("isNodeMetricInited() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getNodeMetricReportInterval(t *testing.T) {
	nodeMetricLister := &fakeNodeMetricLister{
		nodeMetrics: &slov1alpha1.NodeMetric{
			Spec: slov1alpha1.NodeMetricSpec{
				CollectPolicy: &slov1alpha1.NodeMetricCollectPolicy{
					ReportIntervalSeconds: pointer.Int64(666),
				},
			},
		},
	}
	r := &reporter{
		config:           NewDefaultConfig(),
		nodeMetricLister: nodeMetricLister,
	}
	reportInterval := r.getNodeMetricReportInterval()
	expectReportInterval := 666 * time.Second
	if reportInterval != expectReportInterval {
		t.Errorf("expect reportInterval %d but got %d", expectReportInterval, reportInterval)
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

func Test_reporter_sync(t *testing.T) {
	type fields struct {
		nodeName         string
		nodeMetric       *slov1alpha1.NodeMetric
		metricCache      func(ctrl *gomock.Controller) metriccache.MetricCache
		statesInformer   func(ctrl *gomock.Controller) statesinformer.StatesInformer
		nodeMetricLister listerbeta1.NodeMetricLister
		nodeMetricClient clientbeta1.NodeMetricInterface
	}
	tests := []struct {
		name    string
		fields  fields
		want    *slov1alpha1.NodeMetric
		wantErr bool
	}{
		{
			name: "nodeMetric not initialized",
			fields: fields{
				nodeName:   "test",
				nodeMetric: nil,
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					return nil
				},
				statesInformer: func(ctrl *gomock.Controller) statesinformer.StatesInformer {
					return nil
				},
				nodeMetricLister: nil,
				nodeMetricClient: &fakeNodeMetricClient{},
			},
			want:    &slov1alpha1.NodeMetric{},
			wantErr: true,
		},
		{
			name: "collect nil nodeMetric",
			fields: fields{
				nodeName: "test",
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
				},
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					c := mock_metriccache.NewMockMetricCache(ctrl)
					c.EXPECT().GetNodeResourceMetric(gomock.Any()).Return(metriccache.NodeResourceQueryResult{}).Times(1)
					return c
				},
				statesInformer: func(ctrl *gomock.Controller) statesinformer.StatesInformer {
					i := mock_statesinformer.NewMockStatesInformer(ctrl)
					i.EXPECT().GetAllPods().Return(nil).Times(1)
					return i
				},
				nodeMetricLister: nil,
				nodeMetricClient: &fakeNodeMetricClient{},
			},
			want:    &slov1alpha1.NodeMetric{},
			wantErr: true,
		},
		{
			name: "successfully report nodeMetric",
			fields: fields{
				nodeName: "test",
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
				},
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					c := mock_metriccache.NewMockMetricCache(ctrl)
					c.EXPECT().GetNodeResourceMetric(gomock.Any()).Return(metriccache.NodeResourceQueryResult{
						Metric: &metriccache.NodeResourceMetric{
							CPUUsed: metriccache.CPUMetric{
								CPUUsed: resource.MustParse("1000"),
							},
							MemoryUsed: metriccache.MemoryMetric{
								MemoryWithoutCache: resource.MustParse("1Gi"),
							},
							GPUs: []metriccache.GPUMetric{
								{
									DeviceUUID:  "1",
									Minor:       0,
									SMUtil:      80,
									MemoryUsed:  *resource.NewQuantity(30, resource.BinarySI),
									MemoryTotal: *resource.NewQuantity(100, resource.BinarySI),
								},
								{
									DeviceUUID:  "2",
									Minor:       1,
									SMUtil:      40,
									MemoryUsed:  *resource.NewQuantity(50, resource.BinarySI),
									MemoryTotal: *resource.NewQuantity(200, resource.BinarySI),
								},
							},
						},
					}).Times(1)
					c.EXPECT().GetPodResourceMetric(gomock.Any(), gomock.Any()).Return(metriccache.PodResourceQueryResult{
						Metric: &metriccache.PodResourceMetric{
							PodUID: "test-pod",
							CPUUsed: metriccache.CPUMetric{
								CPUUsed: resource.MustParse("1000"),
							},
							MemoryUsed: metriccache.MemoryMetric{
								MemoryWithoutCache: resource.MustParse("1Gi"),
							},
							GPUs: []metriccache.GPUMetric{
								{
									DeviceUUID:  "1",
									Minor:       0,
									SMUtil:      80,
									MemoryUsed:  *resource.NewQuantity(30, resource.BinarySI),
									MemoryTotal: *resource.NewQuantity(100, resource.BinarySI),
								},
								{
									DeviceUUID:  "2",
									Minor:       1,
									SMUtil:      40,
									MemoryUsed:  *resource.NewQuantity(50, resource.BinarySI),
									MemoryTotal: *resource.NewQuantity(200, resource.BinarySI),
								},
							},
						},
					}).Times(1)
					return c
				},
				statesInformer: func(ctrl *gomock.Controller) statesinformer.StatesInformer {
					i := mock_statesinformer.NewMockStatesInformer(ctrl)
					i.EXPECT().GetAllPods().Return(
						[]*statesinformer.PodMeta{
							{
								Pod: &v1.Pod{
									ObjectMeta: metav1.ObjectMeta{
										Name:      "test-pod",
										Namespace: "default",
										UID:       "test-pod",
									},
								},
							},
						}).Times(1)
					return i
				},
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
			want: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
				Status: slov1alpha1.NodeMetricStatus{
					NodeMetric: &slov1alpha1.NodeMetricInfo{
						NodeUsage: *convertNodeMetricToResourceMap(&metriccache.NodeResourceMetric{
							CPUUsed: metriccache.CPUMetric{
								CPUUsed: resource.MustParse("1000"),
							},
							MemoryUsed: metriccache.MemoryMetric{
								MemoryWithoutCache: resource.MustParse("1Gi"),
							},
							GPUs: []metriccache.GPUMetric{
								{
									DeviceUUID:  "1",
									Minor:       0,
									SMUtil:      80,
									MemoryUsed:  *resource.NewQuantity(30, resource.BinarySI),
									MemoryTotal: *resource.NewQuantity(100, resource.BinarySI),
								},
								{
									DeviceUUID:  "2",
									Minor:       1,
									SMUtil:      40,
									MemoryUsed:  *resource.NewQuantity(50, resource.BinarySI),
									MemoryTotal: *resource.NewQuantity(200, resource.BinarySI),
								},
							},
						}),
					},
					PodsMetric: []*slov1alpha1.PodMetricInfo{
						{
							Name:      "test-pod",
							Namespace: "default",
							PodUsage: *convertPodMetricToResourceMap(&metriccache.PodResourceMetric{
								PodUID: "test-pod",
								CPUUsed: metriccache.CPUMetric{
									CPUUsed: resource.MustParse("1000"),
								},
								MemoryUsed: metriccache.MemoryMetric{
									MemoryWithoutCache: resource.MustParse("1Gi"),
								},
								GPUs: []metriccache.GPUMetric{
									{
										DeviceUUID:  "1",
										Minor:       0,
										SMUtil:      80,
										MemoryUsed:  *resource.NewQuantity(30, resource.BinarySI),
										MemoryTotal: *resource.NewQuantity(100, resource.BinarySI),
									},
									{
										DeviceUUID:  "2",
										Minor:       1,
										SMUtil:      40,
										MemoryUsed:  *resource.NewQuantity(50, resource.BinarySI),
										MemoryTotal: *resource.NewQuantity(200, resource.BinarySI),
									},
								},
							}),
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
				},
				metricCache: func(ctrl *gomock.Controller) metriccache.MetricCache {
					c := mock_metriccache.NewMockMetricCache(ctrl)
					c.EXPECT().GetNodeResourceMetric(gomock.Any()).Return(metriccache.NodeResourceQueryResult{
						Metric: &metriccache.NodeResourceMetric{
							CPUUsed: metriccache.CPUMetric{
								CPUUsed: resource.MustParse("1000"),
							},
							MemoryUsed: metriccache.MemoryMetric{
								MemoryWithoutCache: resource.MustParse("1Gi"),
							},
						},
					}).Times(1)
					return c
				},
				statesInformer: func(ctrl *gomock.Controller) statesinformer.StatesInformer {
					i := mock_statesinformer.NewMockStatesInformer(ctrl)
					i.EXPECT().GetAllPods().Return(nil).Times(1)
					return i
				},
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
			want: &slov1alpha1.NodeMetric{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			r := &reporter{
				nodeMetric:       tt.fields.nodeMetric,
				metricCache:      tt.fields.metricCache(ctrl),
				statesInformer:   tt.fields.statesInformer(ctrl),
				nodeMetricLister: tt.fields.nodeMetricLister,
				statusUpdater:    newStatusUpdater(tt.fields.nodeMetricClient),
			}
			r.sync()

			nodeMetric, err := r.statusUpdater.nodeMetricClient.Get(context.TODO(), tt.fields.nodeName, metav1.GetOptions{})
			assert.Equal(t, tt.wantErr, err != nil)
			if tt.wantErr {
				assert.Equal(t, tt.want, nodeMetric)
			} else {
				assert.NotNil(t, nodeMetric)
				assert.Equal(t, tt.want.Status.NodeMetric, nodeMetric.Status.NodeMetric)
				assert.Equal(t, tt.want.Status.PodsMetric, nodeMetric.Status.PodsMetric)
			}
		})
	}
}
