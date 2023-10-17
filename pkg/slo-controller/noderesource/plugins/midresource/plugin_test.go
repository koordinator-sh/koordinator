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

package midresource

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
)

func TestPlugin(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		p := &Plugin{}
		assert.Equal(t, PluginName, p.Name())
	})
}

func TestPluginNeedSync(t *testing.T) {
	testNode := getTestNode(nil)
	testNodeMidNotChange := getTestNode(corev1.ResourceList{
		extension.BatchCPU:    resource.MustParse("50000"),
		extension.BatchMemory: resource.MustParse("90Gi"),
		extension.MidCPU:      resource.MustParse("20000"),
		extension.MidMemory:   resource.MustParse("40Gi"),
	})
	testNodeMidChanged := getTestNode(corev1.ResourceList{
		extension.BatchCPU:    resource.MustParse("40000"),
		extension.BatchMemory: resource.MustParse("80Gi"),
		extension.MidCPU:      resource.MustParse("10000"),
		extension.MidMemory:   resource.MustParse("30Gi"),
	})
	type args struct {
		strategy *configuration.ColocationStrategy
		oldNode  *corev1.Node
		newNode  *corev1.Node
	}
	tests := []struct {
		name  string
		args  args
		want  bool
		want1 string
	}{
		{
			name: "no need to sync for no mid resource changed",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                pointer.Bool(true),
					ResourceDiffThreshold: pointer.Float64(0.05),
				},
				oldNode: testNode,
				newNode: testNodeMidNotChange,
			},
			want:  false,
			want1: "",
		},
		{
			name: "need to sync for mid resource changed",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                pointer.Bool(true),
					ResourceDiffThreshold: pointer.Float64(0.05),
				},
				oldNode: testNode,
				newNode: testNodeMidChanged,
			},
			want:  true,
			want1: "mid resource diff is big than threshold",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			got, got1 := p.NeedSync(tt.args.strategy, tt.args.oldNode, tt.args.newNode)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestPluginPrepare(t *testing.T) {
	testNode := getTestNode(nil)
	testWantNodeMidChange := getTestNode(corev1.ResourceList{
		extension.MidCPU:    *resource.NewQuantity(30000, resource.DecimalSI),
		extension.MidMemory: *resource.NewQuantity(55<<30, resource.BinarySI),
	})
	testWantNodeMidReset := getTestNode(nil, []corev1.ResourceName{
		extension.MidCPU,
		extension.MidMemory,
	}...)
	type args struct {
		strategy *configuration.ColocationStrategy
		node     *corev1.Node
		nr       *framework.NodeResource
	}
	tests := []struct {
		name      string
		args      args
		wantErr   bool
		wantField *corev1.Node
	}{
		{
			name: "prepare mid resources",
			args: args{
				node: testNode,
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.MidCPU:      resource.NewQuantity(30000, resource.DecimalSI),
						extension.MidMemory:   resource.NewQuantity(55<<30, resource.BinarySI),
						extension.BatchCPU:    resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: resource.NewQuantity(70<<30, resource.BinarySI),
					},
				},
			},
			wantErr:   false,
			wantField: testWantNodeMidChange,
		},
		{
			name: "reset mid resources",
			args: args{
				node: testNode,
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{},
					Resets: map[corev1.ResourceName]bool{
						extension.MidCPU:    true,
						extension.MidMemory: true,
					},
				},
			},
			wantErr:   false,
			wantField: testWantNodeMidReset,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			gotErr := p.Prepare(tt.args.strategy, tt.args.node, tt.args.nr)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			assert.Equal(t, tt.wantField, tt.args.node)
		})
	}
}

func TestPluginReset(t *testing.T) {
	testMsg := "test reset node resources"
	type args struct {
		node    *corev1.Node
		message string
	}
	tests := []struct {
		name string
		args args
		want []framework.ResourceItem
	}{
		{
			name: "reset mid resources",
			args: args{
				message: testMsg,
			},
			want: []framework.ResourceItem{
				{
					Name:    extension.MidCPU,
					Message: testMsg,
					Reset:   true,
				},
				{
					Name:    extension.MidMemory,
					Message: testMsg,
					Reset:   true,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			got := p.Reset(tt.args.node, tt.args.message)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPluginCalculate(t *testing.T) {
	testNode := getTestNode(nil)
	testProdLSPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "podA",
			Namespace: "test",
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSLS),
			},
		},
		Spec: corev1.PodSpec{
			PriorityClassName: string(extension.PriorityProd),
			Priority:          pointer.Int32(extension.PriorityProdValueMin),
			NodeName:          "test-node",
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	testBatchBEPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "podB",
			Namespace: "test",
			Labels: map[string]string{
				extension.LabelPodQoS: string(extension.QoSBE),
			},
		},
		Spec: corev1.PodSpec{
			PriorityClassName: string(extension.PriorityBatch),
			Priority:          pointer.Int32(extension.PriorityBatchValueMax),
			NodeName:          "test-node",
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("15"),
							corev1.ResourceMemory: resource.MustParse("30Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("15"),
							corev1.ResourceMemory: resource.MustParse("30Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	testCPUQuant := resource.MustParse("10000")
	_ = testCPUQuant.String()
	testMemoryQuant := resource.MustParse("15Gi")
	testMemoryQuant2 := resource.MustParse("30Gi")
	type args struct {
		strategy *configuration.ColocationStrategy
		node     *corev1.Node
		podList  *corev1.PodList
		metrics  *framework.ResourceMetrics
	}
	tests := []struct {
		name    string
		args    args
		want    []framework.ResourceItem
		wantErr bool
	}{
		{
			name:    "throw an error when some args are invalid",
			args:    args{},
			want:    nil,
			wantErr: true,
		},
		{
			name: "degrade when node metric is expired",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:             pointer.Bool(true),
					DegradeTimeMinutes: pointer.Int64(5),
				},
				node: testNode,
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						*testProdLSPod,
						*testBatchBEPod,
					},
				},
				metrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-node",
						},
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now().Add(-30 * time.Minute)},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("20"),
										corev1.ResourceMemory: resource.MustParse("45Gi"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								{
									Name:      testProdLSPod.Name,
									Namespace: testProdLSPod.Namespace,
									PodUsage: slov1alpha1.ResourceMap{
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("10"),
											corev1.ResourceMemory: resource.MustParse("20Gi"),
										},
									},
								},
								{
									Name:      testBatchBEPod.Name,
									Namespace: testBatchBEPod.Namespace,
									PodUsage: slov1alpha1.ResourceMap{
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("15"),
											corev1.ResourceMemory: resource.MustParse("20Gi"),
										},
									},
								},
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:    extension.MidCPU,
					Message: "degrade node Mid resource because of abnormal nodeMetric, reason: degradedByMidResource",
					Reset:   true,
				},
				{
					Name:    extension.MidMemory,
					Message: "degrade node Mid resource because of abnormal nodeMetric, reason: degradedByMidResource",
					Reset:   true,
				},
			},
			wantErr: false,
		},
		{
			name: "calculate correctly when node metric is valid",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:             pointer.Bool(true),
					DegradeTimeMinutes: pointer.Int64(10),
				},
				node: testNode,
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						*testProdLSPod,
						*testBatchBEPod,
					},
				},
				metrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-node",
						},
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now().Add(-20 * time.Second)},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("20"),
										corev1.ResourceMemory: resource.MustParse("45Gi"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								{
									Name:      testProdLSPod.Name,
									Namespace: testProdLSPod.Namespace,
									PodUsage: slov1alpha1.ResourceMap{
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("10"),
											corev1.ResourceMemory: resource.MustParse("20Gi"),
										},
									},
								},
								{
									Name:      testBatchBEPod.Name,
									Namespace: testBatchBEPod.Namespace,
									PodUsage: slov1alpha1.ResourceMap{
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("15"),
											corev1.ResourceMemory: resource.MustParse("20Gi"),
										},
									},
								},
							},
							ProdReclaimableMetric: &slov1alpha1.ReclaimableMetric{
								Resource: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("10"),
										corev1.ResourceMemory: resource.MustParse("15Gi"),
									},
								},
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.MidCPU,
					Message:  "midAllocatable[CPU(milli-core)]:10000 = min(nodeAllocatable:100000 * thresholdRatio:1, ProdReclaimable:10000)",
					Quantity: &testCPUQuant,
				},
				{
					Name:     extension.MidMemory,
					Message:  "midAllocatable[Memory(byte)]:15Gi = min(nodeAllocatable:200Gi * thresholdRatio:1, ProdReclaimable:15Gi)",
					Quantity: &testMemoryQuant,
				},
			},
			wantErr: false,
		},
		{
			name: "calculate correctly where the prod reclaimable exceeds the mid threshold",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                    pointer.Bool(true),
					DegradeTimeMinutes:        pointer.Int64(10),
					MidCPUThresholdPercent:    pointer.Int64(10),
					MidMemoryThresholdPercent: pointer.Int64(20),
				},
				node: testNode,
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						*testProdLSPod,
						*testBatchBEPod,
					},
				},
				metrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-node",
						},
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now().Add(-20 * time.Second)},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("30"),
										corev1.ResourceMemory: resource.MustParse("50Gi"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								{
									Name:      testProdLSPod.Name,
									Namespace: testProdLSPod.Namespace,
									PodUsage: slov1alpha1.ResourceMap{
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("5"),
											corev1.ResourceMemory: resource.MustParse("10Gi"),
										},
									},
								},
								{
									Name:      testBatchBEPod.Name,
									Namespace: testBatchBEPod.Namespace,
									PodUsage: slov1alpha1.ResourceMap{
										ResourceList: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("15"),
											corev1.ResourceMemory: resource.MustParse("30Gi"),
										},
									},
								},
							},
							ProdReclaimableMetric: &slov1alpha1.ReclaimableMetric{
								Resource: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("15"),
										corev1.ResourceMemory: resource.MustParse("30Gi"),
									},
								},
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.MidCPU,
					Message:  "midAllocatable[CPU(milli-core)]:10000 = min(nodeAllocatable:100000 * thresholdRatio:0.1, ProdReclaimable:15000)",
					Quantity: &testCPUQuant,
				},
				{
					Name:     extension.MidMemory,
					Message:  "midAllocatable[Memory(byte)]:30Gi = min(nodeAllocatable:200Gi * thresholdRatio:0.2, ProdReclaimable:30Gi)",
					Quantity: &testMemoryQuant2,
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			got, gotErr := p.Calculate(tt.args.strategy, tt.args.node, tt.args.podList, tt.args.metrics)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestPlugin_isDegradeNeeded(t *testing.T) {
	const degradeTimeoutMinutes = 10
	type fields struct {
		Clock clock.Clock
	}
	type args struct {
		strategy   *configuration.ColocationStrategy
		nodeMetric *slov1alpha1.NodeMetric
		node       *corev1.Node
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
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
				strategy: &configuration.ColocationStrategy{
					Enable:             pointer.Bool(true),
					DegradeTimeMinutes: pointer.Int64(degradeTimeoutMinutes),
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{
							Time: time.Now().Add(-(degradeTimeoutMinutes + 1) * time.Minute),
						},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{},
				},
			},
			want: true,
		},
		{
			name: "outdated NodeMetric status should degrade 1",
			fields: fields{
				Clock: clock.NewFakeClock(time.Now().Add(time.Minute * (degradeTimeoutMinutes + 1))),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:             pointer.Bool(true),
					DegradeTimeMinutes: pointer.Int64(degradeTimeoutMinutes),
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
						Labels: map[string]string{
							"xxx": "yy",
						},
					},
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{
							Time: time.Now(),
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
		{
			name: "NodeMetric without prod reclaimable should degrade",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:             pointer.Bool(true),
					DegradeTimeMinutes: pointer.Int64(degradeTimeoutMinutes),
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{
							Time: time.Now(),
						},
						NodeMetric: &slov1alpha1.NodeMetricInfo{
							NodeUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("20"),
									corev1.ResourceMemory: resource.MustParse("40Gi"),
								},
							},
						},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{},
				},
			},
			want: true,
		},
		{
			name: "valid NodeMetric status should not degrade",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:             pointer.Bool(true),
					DegradeTimeMinutes: pointer.Int64(degradeTimeoutMinutes),
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{
							Time: time.Now(),
						},
						NodeMetric: &slov1alpha1.NodeMetricInfo{
							NodeUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("20"),
									corev1.ResourceMemory: resource.MustParse("40Gi"),
								},
							},
						},
						ProdReclaimableMetric: &slov1alpha1.ReclaimableMetric{
							Resource: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10"),
									corev1.ResourceMemory: resource.MustParse("20Gi"),
								},
							},
						},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.fields.Clock != nil {
				oldClock := clk
				clk = tt.fields.Clock
				defer func() {
					clk = oldClock
				}()
			}

			p := &Plugin{}
			assert.Equal(t, tt.want, p.isDegradeNeeded(tt.args.strategy, tt.args.nodeMetric, tt.args.node))
		})
	}
}

func getTestNode(resourceList corev1.ResourceList, resetResources ...corev1.ResourceName) *corev1.Node {
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("200Gi"),
				extension.BatchCPU:    resource.MustParse("40000"),
				extension.BatchMemory: resource.MustParse("80Gi"),
				extension.MidCPU:      resource.MustParse("20000"),
				extension.MidMemory:   resource.MustParse("40Gi"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("210Gi"),
				extension.BatchCPU:    resource.MustParse("40000"),
				extension.BatchMemory: resource.MustParse("80Gi"),
				extension.MidCPU:      resource.MustParse("20000"),
				extension.MidMemory:   resource.MustParse("40Gi"),
			},
		},
	}
	for resourceName, q := range resourceList {
		testNode.Status.Allocatable[resourceName] = q
		testNode.Status.Capacity[resourceName] = q
	}
	for _, resourceName := range resetResources {
		delete(testNode.Status.Allocatable, resourceName)
		delete(testNode.Status.Capacity, resourceName)
	}
	return testNode
}
