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

package noderesource

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/config"
)

func Test_isColocationCfgDisabled(t *testing.T) {
	type fields struct {
		config config.ColocationCfg
	}
	type args struct {
		node *corev1.Node
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name:   "set as disabled when no config",
			fields: fields{config: config.ColocationCfg{}},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want: true,
		},
		{
			name: "use cluster config when nil node",
			fields: fields{
				config: config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(false),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
					NodeConfigs: []config.NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "yyy",
								},
							},
							ColocationStrategy: config.ColocationStrategy{
								Enable: pointer.BoolPtr(true),
							},
						},
					},
				},
			},
			args: args{},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NodeResourceReconciler{cfgCache: &FakeCfgCache{
				cfg: tt.fields.config,
			}}
			got := r.isColocationCfgDisabled(tt.args.node)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_isDegradeNeeded(t *testing.T) {
	const degradeTimeoutMinutes = 10
	type args struct {
		nodeMetric *slov1alpha1.NodeMetric
		node       *corev1.Node
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "empty NodeMetric should degrade",
			args: args{
				nodeMetric: nil,
			},
			want: true,
		},
		{
			name: "empty NodeMetric status should degrade",
			args: args{
				nodeMetric: &slov1alpha1.NodeMetric{},
			},
			want: true,
		},
		{
			name: "outdated NodeMetric status should degrade",
			args: args{
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
						Labels: map[string]string{
							"xxx": "yy",
						},
					},
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{
							Time: time.Now().Add(time.Minute * -(degradeTimeoutMinutes + 1)),
						},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
					Status: corev1.NodeStatus{},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NodeResourceReconciler{
				cfgCache: &FakeCfgCache{
					cfg: config.ColocationCfg{
						ColocationStrategy: config.ColocationStrategy{
							Enable: pointer.BoolPtr(true),
						},
						NodeConfigs: []config.NodeColocationCfg{
							{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
								ColocationStrategy: config.ColocationStrategy{
									DegradeTimeMinutes: pointer.Int64Ptr(degradeTimeoutMinutes),
								},
							},
						},
					},
				},
				Clock: clock.RealClock{},
			}
			assert.Equal(t, tt.want, r.isDegradeNeeded(tt.args.nodeMetric, tt.args.node))
		})
	}
}

func Test_updateNodeBEResource(t *testing.T) {
	type fields struct {
		Client      client.Client
		config      *config.ColocationCfg
		SyncContext *SyncContext
	}
	type args struct {
		oldNode    *corev1.Node
		beResource *nodeBEResource
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *corev1.Node
		wantErr bool
	}{
		{
			name: "no need to sync, update nothing",
			fields: fields{
				Client: fake.NewClientBuilder().WithRuntimeObjects(&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(true),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now()},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				beResource: &nodeBEResource{
					CPU:    resource.NewQuantity(20, resource.DecimalSI),
					Memory: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
				},
			},
			want: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node0",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("20"),
						apiext.BatchMemory: resource.MustParse("40G"),
					},
					Capacity: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("20"),
						apiext.BatchMemory: resource.MustParse("40G"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "update be resource successfully",
			fields: fields{
				Client: fake.NewClientBuilder().WithRuntimeObjects(&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(true),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now()},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				beResource: &nodeBEResource{
					CPU:    resource.NewQuantity(30, resource.DecimalSI),
					Memory: resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				},
			},
			want: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node0",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						apiext.BatchCPU:    *resource.NewQuantity(30, resource.DecimalSI),
						apiext.BatchMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					},
					Capacity: corev1.ResourceList{
						apiext.BatchCPU:    *resource.NewQuantity(30, resource.DecimalSI),
						apiext.BatchMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "abort update for the node that no longer exists",
			fields: fields{
				Client: fake.NewClientBuilder().Build(),
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(true),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now()},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				beResource: &nodeBEResource{
					CPU:    resource.NewQuantity(20, resource.DecimalSI),
					Memory: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
				},
			},
			want: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node0",
				},
				Status: corev1.NodeStatus{},
			},
			wantErr: false,
		},
		{
			name: "not update be resource with node-specified config",
			fields: fields{
				Client: fake.NewClientBuilder().WithRuntimeObjects(&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(true),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(70),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(70),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
					NodeConfigs: []config.NodeColocationCfg{
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"xxx": "yyy",
								},
							},
							ColocationStrategy: config.ColocationStrategy{
								CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
								MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
								ResourceDiffThreshold:         pointer.Float64Ptr(0.6),
							},
						},
						{
							NodeSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"abc": "def",
								},
							},
							ColocationStrategy: config.ColocationStrategy{
								CPUReclaimThresholdPercent:    pointer.Int64Ptr(60),
								MemoryReclaimThresholdPercent: pointer.Int64Ptr(60),
							},
						},
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now()},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				beResource: &nodeBEResource{
					CPU:    resource.NewQuantity(30, resource.DecimalSI),
					Memory: resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
				},
			},
			want: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node0",
					Labels: map[string]string{
						"xxx": "yyy",
					},
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("20"),
						apiext.BatchMemory: resource.MustParse("40G"),
					},
					Capacity: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("20"),
						apiext.BatchMemory: resource.MustParse("40G"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "reset be resource with enable=false config",
			fields: fields{
				Client: fake.NewClientBuilder().WithRuntimeObjects(&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(false),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now()},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				beResource: &nodeBEResource{
					CPU:    nil,
					Memory: nil,
				},
			},
			want: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node0",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{},
					Capacity:    corev1.ResourceList{},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &NodeResourceReconciler{
				Client: tt.fields.Client,
				cfgCache: &FakeCfgCache{
					cfg: *tt.fields.config,
				},
				SyncContext: SyncContext{contextMap: tt.fields.SyncContext.contextMap},
				Clock:       clock.RealClock{},
			}
			got := r.updateNodeBEResource(tt.args.oldNode, tt.args.beResource)
			assert.Equal(t, tt.wantErr, got != nil, got)
			gotNode := &corev1.Node{}
			_ = r.Client.Get(context.TODO(), types.NamespacedName{Name: tt.args.oldNode.Name}, gotNode)

			wantCPU := tt.want.Status.Allocatable[apiext.BatchCPU]
			gotCPU := gotNode.Status.Allocatable[apiext.BatchCPU]
			assert.Equal(t, wantCPU.Value(), gotCPU.Value())

			wantMem := tt.want.Status.Allocatable[apiext.BatchMemory]
			gotMem := gotNode.Status.Allocatable[apiext.BatchMemory]
			assert.Equal(t, wantMem.Value(), gotMem.Value())
		})
	}
}

func Test_isBEResourceSyncNeeded(t *testing.T) {
	type fields struct {
		config      *config.ColocationCfg
		SyncContext *SyncContext
	}
	type args struct {
		oldNode *corev1.Node
		newNode *corev1.Node
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
		want1  string
	}{
		{
			name:   "cannot update an invalid new node",
			fields: fields{config: &config.ColocationCfg{}, SyncContext: &SyncContext{}},
			args:   args{},
			want:   false,
		},
		{
			name: "needSync for expired node resource",
			fields: fields{
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(true),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now().Add(0 - 10*time.Minute)},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want: true,
		},
		{
			name: "needSync for cpu diff larger than 0.1",
			fields: fields{
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(true),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now()},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("15"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("15"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want: true,
		},
		{
			name: "needSync for cpu diff larger than 0.1",
			fields: fields{
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(true),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now()},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("70G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("70G"),
						},
					},
				},
			},
			want: true,
		},
		{
			name: "no need to sync, everything's ok.",
			fields: fields{
				config: &config.ColocationCfg{
					ColocationStrategy: config.ColocationStrategy{
						Enable:                        pointer.BoolPtr(true),
						CPUReclaimThresholdPercent:    pointer.Int64Ptr(65),
						MemoryReclaimThresholdPercent: pointer.Int64Ptr(65),
						DegradeTimeMinutes:            pointer.Int64Ptr(15),
						UpdateTimeThresholdSeconds:    pointer.Int64Ptr(300),
						ResourceDiffThreshold:         pointer.Float64Ptr(0.1),
					},
				},
				SyncContext: &SyncContext{
					contextMap: map[string]time.Time{"/test-node0": time.Now()},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"test-label": "test"},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("20"),
							apiext.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &NodeResourceReconciler{
				cfgCache: &FakeCfgCache{
					cfg: *tt.fields.config,
				},
				SyncContext: SyncContext{
					contextMap: tt.fields.SyncContext.contextMap,
				},
				Clock: clock.RealClock{},
			}
			got := r.isBEResourceSyncNeeded(tt.args.oldNode, tt.args.newNode)
			assert.Equal(t, tt.want, got)
		})
	}
}
