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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/clock"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/batchresource"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/midresource"
	"github.com/koordinator-sh/koordinator/pkg/util/testutil"
)

func init() {
	addPlugins(func(s string) bool {
		return s == midresource.PluginName || s == batchresource.PluginName
	})
}

type FakeCfgCache struct {
	cfg         configuration.ColocationCfg
	available   bool
	errorStatus bool
}

func (f *FakeCfgCache) GetCfgCopy() *configuration.ColocationCfg {
	return &f.cfg
}

func (f *FakeCfgCache) IsCfgAvailable() bool {
	return f.available
}

func (f *FakeCfgCache) IsErrorStatus() bool {
	return f.errorStatus
}

var _ framework.NodeMetaCheckPlugin = (*fakeNodeMetaCheckPlugin)(nil)

type fakeNodeMetaCheckPlugin struct {
	CheckLabels []string
	AlwaysSync  bool
}

func (p *fakeNodeMetaCheckPlugin) Name() string {
	return "fakeNodeMetaCheckPlugin"
}

func (p *fakeNodeMetaCheckPlugin) NeedSyncMeta(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	if p.AlwaysSync {
		return true, "always sync"
	}
	if oldNode.Labels == nil && newNode.Labels == nil {
		return false, "node has no label"
	}
	if oldNode.Labels == nil || newNode.Labels == nil {
		return true, "consider different when only one node has labels"
	}
	for _, k := range p.CheckLabels {
		oldV, oldOK := oldNode.Labels[k]
		if !oldOK {
			return true, fmt.Sprintf("old node label %s is not found", k)
		}
		newV, newOK := newNode.Labels[k]
		if !newOK {
			return true, fmt.Sprintf("new node label %s is not found", k)
		}
		if oldV != newV {
			return true, fmt.Sprintf("node label %s is different", k)
		}
	}
	return false, ""
}

func Test_calculateNodeResource(t *testing.T) {
	type args struct {
		node       *corev1.Node
		podList    *corev1.PodList
		nodeMetric *slov1alpha1.NodeMetric
	}
	tests := []struct {
		name string
		args args
		want *framework.NodeResource
	}{
		{
			name: "calculate normal result always no less than zero",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
					},
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podA",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node0",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{Time: time.Now()},
						NodeMetric: &slov1alpha1.NodeMetricInfo{},
						PodsMetric: []*slov1alpha1.PodMetricInfo{},
					},
				},
			},
			want: framework.NewNodeResource([]framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(0, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:0 = nodeCapacity:20000 - nodeSafetyMargin:7000 - systemUsageOrNodeReserved:0 - podHPUsed:20000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(6, 9),
					Message:  "batchAllocatable[Mem(GB)]:6 = nodeCapacity:40 - nodeSafetyMargin:14 - systemUsage:0 - podHPUsed:20",
				},
				{
					Name:     extension.MidCPU,
					Quantity: resource.NewQuantity(0, resource.DecimalSI),
					Message:  "midAllocatable[CPU(milli-core)]:0 = min(nodeCapacity:20000 * thresholdRatio:1, ProdReclaimable:0, NodeUnused:0) + Unallocated:0 * midUnallocatedRatio:0",
				},
				{
					Name:     extension.MidMemory,
					Quantity: resource.NewQuantity(0, resource.BinarySI),
					Message:  "midAllocatable[Memory(GB)]:0 = min(nodeCapacity:40 * thresholdRatio:1, ProdReclaimable:0, NodeUnused:0) + Unallocated:20 * midUnallocatedRatio:0",
				},
			}...),
		},
		{
			name: "calculate normal result correctly by usage",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("120G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("120G"),
						},
					},
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podA",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podB",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName:   "test-node1",
								Containers: []corev1.Container{{}},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podC",
								Namespace: "test",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									}, {
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podD",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("10G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("10G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{Time: time.Now()},
						NodeMetric: &slov1alpha1.NodeMetricInfo{
							NodeUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50"),
									corev1.ResourceMemory: resource.MustParse("55G"),
								},
							},
							SystemUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("7"),
									corev1.ResourceMemory: resource.MustParse("12G"),
								},
							},
						},
						PodsMetric: []*slov1alpha1.PodMetricInfo{
							{
								Namespace: "test",
								Name:      "podA",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("11"),
										corev1.ResourceMemory: resource.MustParse("11G"),
									},
								},
							}, {
								Namespace: "test",
								Name:      "podB",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("10"),
										corev1.ResourceMemory: resource.MustParse("10G"),
									},
								},
							},
							{
								Namespace: "test",
								Name:      "podC",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("22"),
										corev1.ResourceMemory: resource.MustParse("22G"),
									},
								},
							},
						},
					},
				},
			},
			want: framework.NewNodeResource([]framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(25000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:25000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(33, 9),
					Message:  "batchAllocatable[Mem(GB)]:33 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:12 - podHPUsed:33",
				},
				{
					Name:     extension.MidCPU,
					Quantity: resource.NewQuantity(0, resource.DecimalSI),
					Message:  "midAllocatable[CPU(milli-core)]:0 = min(nodeCapacity:100000 * thresholdRatio:1, ProdReclaimable:0, NodeUnused:50000) + Unallocated:53000 * midUnallocatedRatio:0",
				},
				{
					Name:     extension.MidMemory,
					Quantity: resource.NewQuantity(0, resource.BinarySI),
					Message:  "midAllocatable[Memory(GB)]:0 = min(nodeCapacity:120 * thresholdRatio:1, ProdReclaimable:0, NodeUnused:65) + Unallocated:48 * midUnallocatedRatio:0",
				},
			}...),
		},
		{
			name: "calculate normal result correctly with node-specified config by memory usage",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("120G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("120G"),
						},
					},
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podA",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podB",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName:   "test-node1",
								Containers: []corev1.Container{{}},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podC",
								Namespace: "test",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									}, {
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podD",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("10G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("10G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{Time: time.Now()},
						NodeMetric: &slov1alpha1.NodeMetricInfo{
							NodeUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50"),
									corev1.ResourceMemory: resource.MustParse("55G"),
								},
							},
							SystemUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("7"),
									corev1.ResourceMemory: resource.MustParse("12G"),
								},
							},
						},
						PodsMetric: []*slov1alpha1.PodMetricInfo{
							{
								Namespace: "test",
								Name:      "podA",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("11"),
										corev1.ResourceMemory: resource.MustParse("11G"),
									},
								},
							}, {
								Namespace: "test",
								Name:      "podB",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("10"),
										corev1.ResourceMemory: resource.MustParse("10G"),
									},
								},
							},
							{
								Namespace: "test",
								Name:      "podC",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("22"),
										corev1.ResourceMemory: resource.MustParse("22G"),
									},
								},
							},
						},
					},
				},
			},
			want: framework.NewNodeResource([]framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(30000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:30000 = nodeCapacity:100000 - nodeSafetyMargin:30000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(39, 9),
					Message:  "batchAllocatable[Mem(GB)]:39 = nodeCapacity:120 - nodeSafetyMargin:36 - systemUsage:12 - podHPUsed:33",
				},
				{
					Name:     extension.MidCPU,
					Quantity: resource.NewQuantity(0, resource.DecimalSI),
					Message:  "midAllocatable[CPU(milli-core)]:0 = min(nodeCapacity:100000 * thresholdRatio:1, ProdReclaimable:0, NodeUnused:50000) + Unallocated:53000 * midUnallocatedRatio:0",
				},
				{
					Name:     extension.MidMemory,
					Quantity: resource.NewQuantity(0, resource.BinarySI),
					Message:  "midAllocatable[Memory(GB)]:0 = min(nodeCapacity:120 * thresholdRatio:1, ProdReclaimable:0, NodeUnused:65) + Unallocated:48 * midUnallocatedRatio:0",
				},
			}...),
		},
		{
			name: "calculate normal result correctly with node-specified by request",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"memory-calculate-by-request": "true",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("120G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("120G"),
						},
					},
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podA",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podB",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName:   "test-node1",
								Containers: []corev1.Container{{}},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podC",
								Namespace: "test",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									}, {
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podD",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("10G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("10G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{Time: time.Now()},
						NodeMetric: &slov1alpha1.NodeMetricInfo{
							NodeUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50"),
									corev1.ResourceMemory: resource.MustParse("55G"),
								},
							},
							SystemUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("7"),
									corev1.ResourceMemory: resource.MustParse("12G"),
								},
							},
						},
						PodsMetric: []*slov1alpha1.PodMetricInfo{
							{
								Namespace: "test",
								Name:      "podA",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("11"),
										corev1.ResourceMemory: resource.MustParse("11G"),
									},
								},
							}, {
								Namespace: "test",
								Name:      "podB",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("10"),
										corev1.ResourceMemory: resource.MustParse("10G"),
									},
								},
							},
							{
								Namespace: "test",
								Name:      "podC",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("22"),
										corev1.ResourceMemory: resource.MustParse("22G"),
									},
								},
							},
						},
					},
				},
			},
			want: framework.NewNodeResource([]framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(30000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:30000 = nodeCapacity:100000 - nodeSafetyMargin:30000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(36, 9),
					Message:  "batchAllocatable[Mem(GB)]:36 = nodeCapacity:120 - nodeSafetyMargin:24 - nodeReserved:0 - podHPRequest:60",
				},
				{
					Name:     extension.MidCPU,
					Quantity: resource.NewQuantity(0, resource.DecimalSI),
					Message:  "midAllocatable[CPU(milli-core)]:0 = min(nodeCapacity:100000 * thresholdRatio:1, ProdReclaimable:0, NodeUnused:50000) + Unallocated:53000 * midUnallocatedRatio:0",
				},
				{
					Name:     extension.MidMemory,
					Quantity: resource.NewQuantity(0, resource.BinarySI),
					Message:  "midAllocatable[Memory(GB)]:0 = min(nodeCapacity:120 * thresholdRatio:1, ProdReclaimable:0, NodeUnused:65) + Unallocated:48 * midUnallocatedRatio:0",
				},
			}...),
		},
		{
			name: "calculate normal result correctly with node prediction",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("120G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("120G"),
						},
					},
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podA",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("20"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podB",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName:   "test-node1",
								Containers: []corev1.Container{{}},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podC",
								Namespace: "test",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									}, {
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("20G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podD",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("10G"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("10"),
												corev1.ResourceMemory: resource.MustParse("10G"),
											},
										},
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				nodeMetric: &slov1alpha1.NodeMetric{
					Status: slov1alpha1.NodeMetricStatus{
						UpdateTime: &metav1.Time{Time: time.Now()},
						NodeMetric: &slov1alpha1.NodeMetricInfo{
							NodeUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50"),
									corev1.ResourceMemory: resource.MustParse("55G"),
								},
							},
							SystemUsage: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("7"),
									corev1.ResourceMemory: resource.MustParse("12G"),
								},
							},
						},
						PodsMetric: []*slov1alpha1.PodMetricInfo{
							{
								Namespace: "test",
								Name:      "podA",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("11"),
										corev1.ResourceMemory: resource.MustParse("11G"),
									},
								},
							}, {
								Namespace: "test",
								Name:      "podB",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("10"),
										corev1.ResourceMemory: resource.MustParse("10G"),
									},
								},
							},
							{
								Namespace: "test",
								Name:      "podC",
								PodUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("22"),
										corev1.ResourceMemory: resource.MustParse("22G"),
									},
								},
							},
						},
						ProdReclaimableMetric: &slov1alpha1.ReclaimableMetric{
							Resource: slov1alpha1.ResourceMap{
								ResourceList: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10"),
									corev1.ResourceMemory: resource.MustParse("20G"),
								},
							},
						},
					},
				},
			},
			want: framework.NewNodeResource([]framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(25000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:25000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(33, 9),
					Message:  "batchAllocatable[Mem(GB)]:33 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:12 - podHPUsed:33",
				},
				{
					Name:     extension.MidCPU,
					Quantity: resource.NewQuantity(10000, resource.DecimalSI),
					Message:  "midAllocatable[CPU(milli-core)]:10000 = min(nodeCapacity:100000 * thresholdRatio:1, ProdReclaimable:10000, NodeUnused:50000) + Unallocated:53000 * midUnallocatedRatio:0",
				},
				{
					Name:     extension.MidMemory,
					Quantity: resource.NewQuantity(20000000000, resource.BinarySI),
					Message:  "midAllocatable[Memory(GB)]:20 = min(nodeCapacity:120 * thresholdRatio:1, ProdReclaimable:20, NodeUnused:65) + Unallocated:48 * midUnallocatedRatio:0",
				},
			}...),
		},
	}

	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	slov1alpha1.AddToScheme(scheme)
	schedulingv1alpha1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeBuilder := builder.ControllerManagedBy(&testutil.FakeManager{})
	opt := framework.NewOption().WithClient(client).WithScheme(scheme).WithControllerBuilder(fakeBuilder)
	framework.RunSetupExtenders(opt)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memoryCalculateByReq := configuration.CalculateByPodRequest
			r := NodeResourceReconciler{cfgCache: &FakeCfgCache{
				cfg: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                        pointer.Bool(true),
						CPUReclaimThresholdPercent:    pointer.Int64(65),
						MemoryReclaimThresholdPercent: pointer.Int64(65),
						DegradeTimeMinutes:            pointer.Int64(15),
						UpdateTimeThresholdSeconds:    pointer.Int64(300),
						ResourceDiffThreshold:         pointer.Float64(0.1),
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
								Name: "xxx-yyy",
							},
							ColocationStrategy: configuration.ColocationStrategy{
								CPUReclaimThresholdPercent:    pointer.Int64(70),
								MemoryReclaimThresholdPercent: pointer.Int64(70),
							},
						},
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"memory-calculate-by-request": "true",
									},
								},
								Name: "memory-calculate-by-request-true",
							},
							ColocationStrategy: configuration.ColocationStrategy{
								CPUReclaimThresholdPercent:    pointer.Int64(70),
								MemoryReclaimThresholdPercent: pointer.Int64(80),
								MemoryCalculatePolicy:         &memoryCalculateByReq,
							},
						},
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"abc": "def",
									},
								},
								Name: "abc-def",
							},
							ColocationStrategy: configuration.ColocationStrategy{
								CPUReclaimThresholdPercent:    pointer.Int64(60),
								MemoryReclaimThresholdPercent: pointer.Int64(60),
							},
						},
					},
				},
			}}
			got := r.calculateNodeResource(tt.args.node, tt.args.nodeMetric, tt.args.podList)
			for _, resourceName := range []corev1.ResourceName{
				extension.BatchCPU,
				extension.BatchMemory,
				extension.MidCPU,
				extension.MidMemory,
			} {
				v, ok := tt.want.Resources[resourceName]
				if !ok || v == nil {
					assert.Nil(t, got.Resources[resourceName], resourceName)
				} else {
					assert.NotNil(t, got.Resources[resourceName], resourceName)
					assert.Equal(t, tt.want.Resources[resourceName].Value(), got.Resources[resourceName].Value(), resourceName)
				}
			}
			assert.Equal(t, tt.want.Messages, got.Messages)
			assert.Equal(t, tt.want.Resets, got.Resets)
		})
	}
}

func Test_isColocationCfgDisabled(t *testing.T) {
	type fields struct {
		config configuration.ColocationCfg
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
			fields: fields{config: configuration.ColocationCfg{}},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want: true,
		},
		{
			name: "use cluster config when nil node",
			fields: fields{
				config: configuration.ColocationCfg{
					ColocationStrategy: configuration.ColocationStrategy{
						Enable:                        pointer.Bool(false),
						CPUReclaimThresholdPercent:    pointer.Int64(65),
						MemoryReclaimThresholdPercent: pointer.Int64(65),
						DegradeTimeMinutes:            pointer.Int64(15),
						UpdateTimeThresholdSeconds:    pointer.Int64(300),
						ResourceDiffThreshold:         pointer.Float64(0.1),
					},
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
							},
							ColocationStrategy: configuration.ColocationStrategy{
								Enable: pointer.Bool(true),
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

func Test_updateNodeResource(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	enabledCfg := &configuration.ColocationCfg{
		ColocationStrategy: configuration.ColocationStrategy{
			Enable:                        pointer.Bool(true),
			CPUReclaimThresholdPercent:    pointer.Int64(65),
			MemoryReclaimThresholdPercent: pointer.Int64(65),
			DegradeTimeMinutes:            pointer.Int64(15),
			UpdateTimeThresholdSeconds:    pointer.Int64(300),
			ResourceDiffThreshold:         pointer.Float64(0.1),
		},
	}
	disableCfg := &configuration.ColocationCfg{
		ColocationStrategy: configuration.ColocationStrategy{
			Enable:                        pointer.Bool(false),
			CPUReclaimThresholdPercent:    pointer.Int64(65),
			MemoryReclaimThresholdPercent: pointer.Int64(65),
			DegradeTimeMinutes:            pointer.Int64(15),
			UpdateTimeThresholdSeconds:    pointer.Int64(300),
			ResourceDiffThreshold:         pointer.Float64(0.1),
		},
	}
	type fields struct {
		Client                     client.Client
		config                     *configuration.ColocationCfg
		SyncContext                *framework.SyncContext
		prepareNodeMetaCheckPlugin []framework.NodeMetaCheckPlugin
	}
	type args struct {
		oldNode *corev1.Node
		nr      *framework.NodeResource
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
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: enabledCfg,
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				nr: framework.NewNodeResource([]framework.ResourceItem{
					{
						Name:     extension.BatchCPU,
						Quantity: resource.NewQuantity(20, resource.DecimalSI),
					},
					{
						Name:     extension.BatchMemory,
						Quantity: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
				}...),
			},
			want: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node0",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						extension.BatchCPU:    resource.MustParse("20"),
						extension.BatchMemory: resource.MustParse("40G"),
					},
					Capacity: corev1.ResourceList{
						extension.BatchCPU:    resource.MustParse("20"),
						extension.BatchMemory: resource.MustParse("40G"),
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
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: enabledCfg,
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				nr: framework.NewNodeResource([]framework.ResourceItem{
					{
						Name:     extension.BatchCPU,
						Quantity: resource.NewQuantity(30, resource.DecimalSI),
					},
					{
						Name:     extension.BatchMemory,
						Quantity: resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					},
				}...),
			},
			want: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node0",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						extension.BatchCPU:    *resource.NewQuantity(30, resource.DecimalSI),
						extension.BatchMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					},
					Capacity: corev1.ResourceList{
						extension.BatchCPU:    *resource.NewQuantity(30, resource.DecimalSI),
						extension.BatchMemory: *resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "abort update for the node that no longer exists",
			fields: fields{
				Client: fake.NewClientBuilder().Build(),
				config: enabledCfg,
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				nr: framework.NewNodeResource([]framework.ResourceItem{
					{
						Name:     extension.BatchCPU,
						Quantity: resource.NewQuantity(20, resource.DecimalSI),
					},
					{
						Name:     extension.BatchMemory,
						Quantity: resource.NewQuantity(40*1024*1024*1024, resource.BinarySI),
					},
				}...),
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
			name: "notice the update for invalid be resource",
			fields: fields{
				Client: fake.NewClientBuilder().WithRuntimeObjects(&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: enabledCfg,
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				nr: framework.NewNodeResource([]framework.ResourceItem{
					{
						Name:     extension.BatchCPU,
						Quantity: resource.NewMilliQuantity(22200, resource.DecimalSI),
					},
					{
						Name:     extension.BatchMemory,
						Quantity: resource.NewMilliQuantity(40*1001*1023*1024*1024, resource.BinarySI),
					},
				}...),
			},
			want: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node0",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						extension.BatchCPU:    resource.MustParse("23"),
						extension.BatchMemory: resource.MustParse("42950637650"),
					},
					Capacity: corev1.ResourceList{
						extension.BatchCPU:    resource.MustParse("23"),
						extension.BatchMemory: resource.MustParse("42950637650"),
					},
				},
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
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: &configuration.ColocationCfg{
					ColocationStrategy: enabledCfg.ColocationStrategy,
					NodeConfigs: []configuration.NodeColocationCfg{
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"xxx": "yyy",
									},
								},
							},
							ColocationStrategy: configuration.ColocationStrategy{
								CPUReclaimThresholdPercent:    pointer.Int64(65),
								MemoryReclaimThresholdPercent: pointer.Int64(65),
								ResourceDiffThreshold:         pointer.Float64(0.6),
							},
						},
						{
							NodeCfgProfile: configuration.NodeCfgProfile{
								NodeSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"abc": "def",
									},
								},
							},
							ColocationStrategy: configuration.ColocationStrategy{
								CPUReclaimThresholdPercent:    pointer.Int64(60),
								MemoryReclaimThresholdPercent: pointer.Int64(60),
							},
						},
					},
				},
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
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
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				nr: framework.NewNodeResource([]framework.ResourceItem{
					{
						Name:     extension.BatchCPU,
						Quantity: resource.NewQuantity(30, resource.DecimalSI),
					},
					{
						Name:     extension.BatchMemory,
						Quantity: resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					},
				}...),
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
						extension.BatchCPU:    resource.MustParse("20"),
						extension.BatchMemory: resource.MustParse("40G"),
					},
					Capacity: corev1.ResourceList{
						extension.BatchCPU:    resource.MustParse("20"),
						extension.BatchMemory: resource.MustParse("40G"),
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
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: disableCfg,
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				nr: framework.NewNodeResource([]framework.ResourceItem{
					{
						Name:  extension.BatchCPU,
						Reset: true,
					},
					{
						Name:  extension.BatchMemory,
						Reset: true,
					},
				}...),
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
		{
			name: "failed to update for node not found",
			fields: fields{
				Client: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
				config: enabledCfg,
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				nr: framework.NewNodeResource([]framework.ResourceItem{
					{
						Name:     extension.BatchCPU,
						Quantity: resource.NewQuantity(30, resource.DecimalSI),
					},
					{
						Name:     extension.BatchMemory,
						Quantity: resource.NewQuantity(50*1024*1024*1024, resource.BinarySI),
					},
				}...),
			},
			wantErr: true,
		},
		{
			name: "update node meta",
			fields: fields{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40G"),
						},
					},
				}).Build(),
				config: enabledCfg,
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
				prepareNodeMetaCheckPlugin: []framework.NodeMetaCheckPlugin{
					&fakeNodeMetaCheckPlugin{
						AlwaysSync: true,
					},
				},
			},
			args: args{
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40Gi"),
						},
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("20"),
							corev1.ResourceMemory: resource.MustParse("40Gi"),
						},
					},
				},
				nr: framework.NewNodeResource([]framework.ResourceItem{
					{
						Labels: map[string]string{
							"xxx": "yyy",
						},
					},
				}...),
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
						corev1.ResourceCPU:    resource.MustParse("20"),
						corev1.ResourceMemory: resource.MustParse("40Gi"),
					},
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("20"),
						corev1.ResourceMemory: resource.MustParse("40Gi"),
					},
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
				NodeSyncContext: tt.fields.SyncContext,
				Clock:           clock.RealClock{},
			}
			oldNodeCopy := tt.args.oldNode.DeepCopy()
			if len(tt.fields.prepareNodeMetaCheckPlugin) > 0 {
				framework.RegisterNodeMetaCheckExtender(framework.AllPass, tt.fields.prepareNodeMetaCheckPlugin...)
				defer func() {
					for _, p := range tt.fields.prepareNodeMetaCheckPlugin {
						framework.UnregisterNodeMetaCheckExtender(p.Name())
					}
				}()
			}

			got := r.updateNodeResource(tt.args.oldNode, tt.args.nr)
			assert.Equal(t, tt.wantErr, got != nil, got)
			if !tt.wantErr {
				gotNode := &corev1.Node{}
				_ = r.Client.Get(context.TODO(), types.NamespacedName{Name: tt.args.oldNode.Name}, gotNode)

				wantCPU := tt.want.Status.Allocatable[extension.BatchCPU]
				gotCPU := gotNode.Status.Allocatable[extension.BatchCPU]
				assert.Equal(t, wantCPU.Value(), gotCPU.Value())

				wantMem := tt.want.Status.Allocatable[extension.BatchMemory]
				gotMem := gotNode.Status.Allocatable[extension.BatchMemory]
				assert.Equal(t, wantMem.Value(), gotMem.Value())
			}
			assert.Equal(t, oldNodeCopy, tt.args.oldNode) // must not change the node object in cache
		})
	}
}

func Test_isNodeResourceSyncNeeded(t *testing.T) {
	type fields struct {
		SyncContext                *framework.SyncContext
		prepareNodeMetaCheckPlugin []framework.NodeMetaCheckPlugin
	}
	type args struct {
		strategy *configuration.ColocationStrategy
		oldNode  *corev1.Node
		newNode  *corev1.Node
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
		want1  bool
	}{
		{
			name:   "cannot update an invalid new node",
			fields: fields{SyncContext: &framework.SyncContext{}},
			args:   args{strategy: &configuration.ColocationStrategy{}},
			want:   false,
			want1:  false,
		},
		{
			name: "needSync for expired node resource",
			fields: fields{
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now().Add(0 - 10*time.Minute)},
				),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want:  true,
			want1: false,
		},
		{
			name: "needSync for cpu diff larger than 0.1",
			fields: fields{
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("15"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("15"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want:  true,
			want1: false,
		},
		{
			name: "needSync for cpu diff larger than 0.1",
			fields: fields{
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("70G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("70G"),
						},
					},
				},
			},
			want:  true,
			want1: false,
		},
		{
			name: "no need to sync, everything's ok.",
			fields: fields{
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
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
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want:  false,
			want1: false,
		},
		{
			name: "need to sync meta",
			fields: fields{
				SyncContext: framework.NewSyncContext().WithContext(
					map[string]time.Time{"/test-node0": time.Now()},
				),
				prepareNodeMetaCheckPlugin: []framework.NodeMetaCheckPlugin{
					&fakeNodeMetaCheckPlugin{
						CheckLabels: []string{"expect-to-change-label"},
					},
				},
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
						Labels: map[string]string{
							"expect-to-change-label": "xxx",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
				newNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node0",
						Labels: map[string]string{
							"test-label":             "test",
							"expect-to-change-label": "yyy",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
						Capacity: corev1.ResourceList{
							extension.BatchCPU:    resource.MustParse("20"),
							extension.BatchMemory: resource.MustParse("40G"),
						},
					},
				},
			},
			want:  false,
			want1: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &NodeResourceReconciler{
				cfgCache:        &FakeCfgCache{},
				NodeSyncContext: tt.fields.SyncContext,
				Clock:           clock.RealClock{},
			}
			if len(tt.fields.prepareNodeMetaCheckPlugin) > 0 {
				framework.RegisterNodeMetaCheckExtender(framework.AllPass, tt.fields.prepareNodeMetaCheckPlugin...)
				defer func() {
					for _, p := range tt.fields.prepareNodeMetaCheckPlugin {
						framework.UnregisterNodeMetaCheckExtender(p.Name())
					}
				}()
			}

			got, got1 := r.isNodeResourceSyncNeeded(tt.args.strategy, tt.args.oldNode, tt.args.newNode)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}
