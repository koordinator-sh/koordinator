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

package batchresource

import (
	"context"
	"fmt"
	"testing"
	"time"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	fakeclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/testutil"
)

func makeResourceList(cpu, memory string) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
	}
}

func makeNodeStat(cpu, memory string) corev1.NodeStatus {
	return corev1.NodeStatus{
		Capacity:    makeResourceList(cpu, memory),
		Allocatable: makeResourceList(cpu, memory),
	}
}

func makeNodeStatWithKubeletReserved(allocCPU, allocMemory, capCPU, capMemory string) corev1.NodeStatus {
	return corev1.NodeStatus{
		Capacity:    makeResourceList(capCPU, capMemory),
		Allocatable: makeResourceList(allocCPU, allocMemory),
	}
}

func makeResourceReq(cpu, memory string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: makeResourceList(cpu, memory),
		Limits:   makeResourceList(cpu, memory),
	}
}

func getTestPodList() *corev1.PodList {
	return &corev1.PodList{
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
							Resources: makeResourceReq("20", "20G"),
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
							Resources: makeResourceReq("10", "20G"),
						}, {
							Resources: makeResourceReq("10", "20G"),
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
							Resources: makeResourceReq("10", "10G"),
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodSucceeded,
				},
			},
		},
	}
}

func getTestResourceMetrics() *framework.ResourceMetrics {
	return &framework.ResourceMetrics{
		NodeMetric: &slov1alpha1.NodeMetric{
			Status: slov1alpha1.NodeMetricStatus{
				UpdateTime: &metav1.Time{Time: time.Now()},
				NodeMetric: &slov1alpha1.NodeMetricInfo{
					NodeUsage: slov1alpha1.ResourceMap{
						ResourceList: makeResourceList("50", "55G"),
					},
					SystemUsage: slov1alpha1.ResourceMap{
						ResourceList: makeResourceList("7", "12G"),
					},
				},
				PodsMetric: []*slov1alpha1.PodMetricInfo{
					genPodMetric("test", "podA", "11", "11G"),
					genPodMetric("test", "podB", "10", "10G"),
					genPodMetric("test", "podC", "22", "22G"),
				},
			},
		},
	}
}

func genPodMetric(namespace string, name string, cpu string, memory string) *slov1alpha1.PodMetricInfo {
	return &slov1alpha1.PodMetricInfo{
		Name:      name,
		Namespace: namespace,
		PodUsage: slov1alpha1.ResourceMap{
			ResourceList: makeResourceList(cpu, memory),
		},
	}
}

func genPodMetricWithSLO(namespace string, name string, cpu string, memory string,
	priority extension.PriorityClass, qos extension.QoSClass) *slov1alpha1.PodMetricInfo {
	podMetric := genPodMetric(namespace, name, cpu, memory)
	podMetric.Priority = priority
	podMetric.QoS = qos
	return podMetric
}

func TestPlugin(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		testScheme := runtime.NewScheme()
		err := clientgoscheme.AddToScheme(testScheme)
		assert.NoError(t, err)
		err = slov1alpha1.AddToScheme(testScheme)
		assert.NoError(t, err)
		err = topov1alpha1.AddToScheme(testScheme)
		assert.NoError(t, err)

		defer testPluginCleanup()
		p := &Plugin{}
		assert.Equal(t, PluginName, p.Name())
		testOpt := &framework.Option{
			Scheme:   testScheme,
			Client:   fake.NewClientBuilder().WithScheme(testScheme).Build(),
			Builder:  builder.ControllerManagedBy(&testutil.FakeManager{}),
			Recorder: &record.FakeRecorder{},
		}
		err = p.Setup(testOpt)
		assert.NoError(t, err)
	})
}

func TestPreUpdate(t *testing.T) {
	testScheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = slov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = topov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	type fields struct {
		client ctrlclient.Client
	}
	type args struct {
		strategy *configuration.ColocationStrategy
		node     *corev1.Node
		nr       *framework.NodeResource
	}
	tests := []struct {
		name      string
		fields    fields
		args      args
		wantErr   bool
		checkFunc func(t *testing.T, client ctrlclient.Client)
	}{
		{
			name: "update batch resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: resource.NewScaledQuantity(120, 9),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "reset batch resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
					},
				},
				nr: &framework.NodeResource{
					Resets: map[corev1.ResourceName]bool{
						extension.BatchCPU:    true,
						extension.BatchMemory: true,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "add NUMA-level batch resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					ResourceDiffThreshold: pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: resource.NewScaledQuantity(120, 9),
					},
					ZoneResources: map[string]corev1.ResourceList{
						util.GenNodeZoneName(0): {
							extension.BatchCPU:    resource.MustParse("25000"),
							extension.BatchMemory: resource.MustParse("62G"),
						},
						util.GenNodeZoneName(1): {
							extension.BatchCPU:    resource.MustParse("25000"),
							extension.BatchMemory: resource.MustParse("58G"),
						},
					},
				},
			},
			wantErr: false,
			checkFunc: func(t *testing.T, client ctrlclient.Client) {
				nrt := &topov1alpha1.NodeResourceTopology{}
				err := client.Get(context.TODO(), types.NamespacedName{Name: "test-node"}, nrt)
				assert.NoError(t, err)
				expectedNRT := &topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}
				assert.Equal(t, len(expectedNRT.Zones), len(nrt.Zones))
				for i := range expectedNRT.Zones {
					assert.Equal(t, expectedNRT.Zones[i].Name, nrt.Zones[i].Name, fmt.Sprintf("zone %v", i))
					assert.Equal(t, len(expectedNRT.Zones[i].Resources), len(nrt.Zones[i].Resources), fmt.Sprintf("zone %v", i))
					for j := range expectedNRT.Zones[i].Resources {
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Capacity.Value(), nrt.Zones[i].Resources[j].Capacity.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Allocatable.Value(), nrt.Zones[i].Resources[j].Allocatable.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Available.Value(), nrt.Zones[i].Resources[j].Available.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
					}
				}
			},
		},
		{
			name: "update NUMA-level batch resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					ResourceDiffThreshold: pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(25000, resource.DecimalSI),
						extension.BatchMemory: resource.NewScaledQuantity(50, 9),
					},
					ZoneResources: map[string]corev1.ResourceList{
						util.GenNodeZoneName(0): {
							extension.BatchCPU:    resource.MustParse("15000"),
							extension.BatchMemory: resource.MustParse("30G"),
						},
						util.GenNodeZoneName(1): {
							extension.BatchCPU:    resource.MustParse("10000"),
							extension.BatchMemory: resource.MustParse("20G"),
						},
					},
				},
			},
			wantErr: false,
			checkFunc: func(t *testing.T, client ctrlclient.Client) {
				nrt := &topov1alpha1.NodeResourceTopology{}
				err := client.Get(context.TODO(), types.NamespacedName{Name: "test-node"}, nrt)
				assert.NoError(t, err)
				expectedNRT := &topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("15000"),
									Allocatable: resource.MustParse("15000"),
									Available:   resource.MustParse("15000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("30G"),
									Allocatable: resource.MustParse("30G"),
									Available:   resource.MustParse("30G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("10000"),
									Allocatable: resource.MustParse("10000"),
									Available:   resource.MustParse("10000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("20G"),
									Allocatable: resource.MustParse("20G"),
									Available:   resource.MustParse("20G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}
				assert.Equal(t, len(expectedNRT.Zones), len(nrt.Zones))
				for i := range expectedNRT.Zones {
					assert.Equal(t, expectedNRT.Zones[i].Name, nrt.Zones[i].Name, fmt.Sprintf("zone %v", i))
					assert.Equal(t, len(expectedNRT.Zones[i].Resources), len(nrt.Zones[i].Resources), fmt.Sprintf("zone %v", i))
					for j := range expectedNRT.Zones[i].Resources {
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Capacity.Value(), nrt.Zones[i].Resources[j].Capacity.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Allocatable.Value(), nrt.Zones[i].Resources[j].Allocatable.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Available.Value(), nrt.Zones[i].Resources[j].Available.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
					}
				}
			},
		},
		{
			name: "update NUMA-level batch resources with cpu-normalization ratio",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					ResourceDiffThreshold: pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(25000, resource.DecimalSI),
						extension.BatchMemory: resource.NewScaledQuantity(50, 9),
					},
					ZoneResources: map[string]corev1.ResourceList{
						util.GenNodeZoneName(0): {
							extension.BatchCPU:    resource.MustParse("15000"),
							extension.BatchMemory: resource.MustParse("30G"),
						},
						util.GenNodeZoneName(1): {
							extension.BatchCPU:    resource.MustParse("10000"),
							extension.BatchMemory: resource.MustParse("20G"),
						},
					},
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.20",
					},
				},
			},
			wantErr: false,
			checkFunc: func(t *testing.T, client ctrlclient.Client) {
				nrt := &topov1alpha1.NodeResourceTopology{}
				err := client.Get(context.TODO(), types.NamespacedName{Name: "test-node"}, nrt)
				assert.NoError(t, err)
				expectedNRT := &topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("18000"),
									Allocatable: resource.MustParse("18000"),
									Available:   resource.MustParse("18000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("30G"),
									Allocatable: resource.MustParse("30G"),
									Available:   resource.MustParse("30G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("12000"),
									Allocatable: resource.MustParse("12000"),
									Available:   resource.MustParse("12000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("20G"),
									Allocatable: resource.MustParse("20G"),
									Available:   resource.MustParse("20G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}
				assert.Equal(t, len(expectedNRT.Zones), len(nrt.Zones))
				for i := range expectedNRT.Zones {
					assert.Equal(t, expectedNRT.Zones[i].Name, nrt.Zones[i].Name, fmt.Sprintf("zone %v", i))
					assert.Equal(t, len(expectedNRT.Zones[i].Resources), len(nrt.Zones[i].Resources), fmt.Sprintf("zone %v", i))
					for j := range expectedNRT.Zones[i].Resources {
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Capacity.Value(), nrt.Zones[i].Resources[j].Capacity.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Allocatable.Value(), nrt.Zones[i].Resources[j].Allocatable.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
						assert.Equal(t, expectedNRT.Zones[i].Resources[j].Available.Value(), nrt.Zones[i].Resources[j].Available.Value(), fmt.Sprintf("zone %v, resource %v", i, j))
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer testPluginCleanup()
			p := &Plugin{}
			assert.Equal(t, PluginName, p.Name())
			testOpt := &framework.Option{
				Scheme:   testScheme,
				Client:   fake.NewClientBuilder().WithScheme(testScheme).Build(),
				Builder:  builder.ControllerManagedBy(&testutil.FakeManager{}),
				Recorder: &record.FakeRecorder{},
			}
			if tt.fields.client != nil {
				testOpt.Client = tt.fields.client
			}
			err = p.Setup(testOpt)
			assert.NoError(t, err)

			gotErr := p.PreUpdate(tt.args.strategy, tt.args.node, tt.args.nr)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			if tt.checkFunc != nil {
				tt.checkFunc(t, testOpt.Client)
			}
		})
	}
}

func TestPrepare(t *testing.T) {
	testScheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = slov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = topov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	type fields struct {
		client ctrlclient.Client
	}
	type args struct {
		strategy *configuration.ColocationStrategy
		node     *corev1.Node
		nr       *framework.NodeResource
	}
	tests := []struct {
		name      string
		fields    fields
		args      args
		wantErr   bool
		wantField *corev1.Node
	}{
		{
			name: "update batch resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: resource.NewScaledQuantity(120, 9),
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("400Gi"),
						extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("380Gi"),
						extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
					},
				},
			},
		},
		{
			name: "reset batch resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
					},
				},
				nr: &framework.NodeResource{
					Resets: map[corev1.ResourceName]bool{
						extension.BatchCPU:    true,
						extension.BatchMemory: true,
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("400Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("380Gi"),
					},
				},
			},
		},
		{
			name: "add NUMA-level batch resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					ResourceDiffThreshold: pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: resource.NewScaledQuantity(120, 9),
					},
					ZoneResources: map[string]corev1.ResourceList{
						util.GenNodeZoneName(0): {
							extension.BatchCPU:    resource.MustParse("25000"),
							extension.BatchMemory: resource.MustParse("62G"),
						},
						util.GenNodeZoneName(1): {
							extension.BatchCPU:    resource.MustParse("25000"),
							extension.BatchMemory: resource.MustParse("58G"),
						},
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("400Gi"),
						extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("380Gi"),
						extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
					},
				},
			},
		},
		{
			name: "update NUMA-level batch resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					ResourceDiffThreshold: pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(25000, resource.DecimalSI),
						extension.BatchMemory: resource.NewScaledQuantity(50, 9),
					},
					ZoneResources: map[string]corev1.ResourceList{
						util.GenNodeZoneName(0): {
							extension.BatchCPU:    resource.MustParse("15000"),
							extension.BatchMemory: resource.MustParse("30G"),
						},
						util.GenNodeZoneName(1): {
							extension.BatchCPU:    resource.MustParse("10000"),
							extension.BatchMemory: resource.MustParse("20G"),
						},
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("400Gi"),
						extension.BatchCPU:    *resource.NewQuantity(25000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewScaledQuantity(50, 9),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("380Gi"),
						extension.BatchCPU:    *resource.NewQuantity(25000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewScaledQuantity(50, 9),
					},
				},
			},
		},
		{
			name: "update NUMA-level batch resources with cpu-normalization ratio",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("200Gi"),
									Allocatable: resource.MustParse("200Gi"),
									Available:   resource.MustParse("200Gi"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(extension.BatchCPU),
									Capacity:    resource.MustParse("25000"),
									Allocatable: resource.MustParse("25000"),
									Available:   resource.MustParse("25000"),
								},
								{
									Name:        string(extension.BatchMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("180Gi"),
									Allocatable: resource.MustParse("180Gi"),
									Available:   resource.MustParse("180Gi"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					ResourceDiffThreshold: pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(25000, resource.DecimalSI),
						extension.BatchMemory: resource.NewScaledQuantity(50, 9),
					},
					ZoneResources: map[string]corev1.ResourceList{
						util.GenNodeZoneName(0): {
							extension.BatchCPU:    resource.MustParse("15000"),
							extension.BatchMemory: resource.MustParse("30G"),
						},
						util.GenNodeZoneName(1): {
							extension.BatchCPU:    resource.MustParse("10000"),
							extension.BatchMemory: resource.MustParse("20G"),
						},
					},
					Annotations: map[string]string{
						extension.AnnotationCPUNormalizationRatio: "1.20",
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("400Gi"),
						extension.BatchCPU:    *resource.NewQuantity(30000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewScaledQuantity(50, 9),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("380Gi"),
						extension.BatchCPU:    *resource.NewQuantity(30000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewScaledQuantity(50, 9),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer testPluginCleanup()
			p := &Plugin{}
			assert.Equal(t, PluginName, p.Name())
			testOpt := &framework.Option{
				Scheme:   testScheme,
				Client:   fake.NewClientBuilder().WithScheme(testScheme).Build(),
				Builder:  builder.ControllerManagedBy(&testutil.FakeManager{}),
				Recorder: &record.FakeRecorder{},
			}
			if tt.fields.client != nil {
				testOpt.Client = tt.fields.client
			}
			err = p.Setup(testOpt)
			assert.NoError(t, err)

			gotErr := p.Prepare(tt.args.strategy, tt.args.node, tt.args.nr)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			testingCorrectResourceList(t, &tt.wantField.Status.Capacity, &tt.args.node.Status.Capacity)
			testingCorrectResourceList(t, &tt.wantField.Status.Allocatable, &tt.args.node.Status.Allocatable)
		})
	}
}

func TestPrepareWithThirdParty(t *testing.T) {
	testScheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = slov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = topov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	type fields struct {
		client ctrlclient.Client
	}
	type args struct {
		strategy *configuration.ColocationStrategy
		node     *corev1.Node
		nr       *framework.NodeResource
	}
	tests := []struct {
		name                string
		fields              fields
		args                args
		wantErr             bool
		wantField           *corev1.Node
		wantOriginAllocated *slov1alpha1.OriginAllocatable
	}{
		{
			name: "update batch resources with third party",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							slov1alpha1.NodeThirdPartyAllocationsAnnotationKey: "{\"allocations\":[{\"name\":\"hadoop-yarn\",\"priority\":\"koord-batch\",\"resources\":{\"kubernetes.io/batch-cpu\":\"10000\",\"kubernetes.io/batch-memory\":\"10Gi\"}}]}",
						},
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: resource.NewQuantity(120*1024*1024*1024, resource.BinarySI),
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("400Gi"),
						extension.BatchCPU:    *resource.NewQuantity(40000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewQuantity(110*1024*1024*1024, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("380Gi"),
						extension.BatchCPU:    *resource.NewQuantity(40000, resource.DecimalSI),
						extension.BatchMemory: *resource.NewQuantity(110*1024*1024*1024, resource.BinarySI),
					},
				},
			},
			wantOriginAllocated: &slov1alpha1.OriginAllocatable{
				Resources: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("50k"),
					extension.BatchMemory: resource.MustParse("120Gi"),
				},
			},
		},
		{
			name: "update batch resources with third party max zero",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							slov1alpha1.NodeThirdPartyAllocationsAnnotationKey: "{\"allocations\":[{\"name\":\"hadoop-yarn\",\"priority\":\"koord-batch\",\"resources\":{\"kubernetes.io/batch-cpu\":\"60000\",\"kubernetes.io/batch-memory\":\"120Gi\"}}]}",
						},
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
						},
					},
				},
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.BatchCPU:    resource.NewQuantity(50000, resource.DecimalSI),
						extension.BatchMemory: resource.NewQuantity(120*1024*1024*1024, resource.BinarySI),
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("400Gi"),
						extension.BatchCPU:    *resource.NewQuantity(0, resource.DecimalSI),
						extension.BatchMemory: *resource.NewQuantity(0, resource.BinarySI),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("380Gi"),
						extension.BatchCPU:    *resource.NewQuantity(0, resource.DecimalSI),
						extension.BatchMemory: *resource.NewQuantity(0, resource.BinarySI),
					},
				},
			},
			wantOriginAllocated: &slov1alpha1.OriginAllocatable{
				Resources: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("50k"),
					extension.BatchMemory: resource.MustParse("120Gi"),
				},
			},
		},
		{
			name: "reset batch resources with third party allocation",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
			},
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node",
						Annotations: map[string]string{
							slov1alpha1.NodeThirdPartyAllocationsAnnotationKey: "{\"allocations\":[{\"name\":\"hadoop-yarn\",\"priority\":\"koord-batch\",\"resources\":{\"kubernetes.io/batch-cpu\":\"10000\",\"kubernetes.io/batch-memory\":\"10Gi\"}}]}",
						},
					},
					Status: corev1.NodeStatus{
						Capacity: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("400Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
						Allocatable: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100"),
							corev1.ResourceMemory: resource.MustParse("380Gi"),
							extension.BatchCPU:    *resource.NewQuantity(50000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewScaledQuantity(120, 9),
						},
					},
				},
				nr: &framework.NodeResource{
					Resets: map[corev1.ResourceName]bool{
						extension.BatchCPU:    true,
						extension.BatchMemory: true,
					},
				},
			},
			wantErr: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("400Gi"),
					},
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100"),
						corev1.ResourceMemory: resource.MustParse("380Gi"),
					},
				},
			},
			wantOriginAllocated: &slov1alpha1.OriginAllocatable{
				Resources: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("0"),
					extension.BatchMemory: resource.MustParse("0"),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer testPluginCleanup()
			p := &Plugin{}
			assert.Equal(t, PluginName, p.Name())
			testOpt := &framework.Option{
				Scheme:   testScheme,
				Client:   fake.NewClientBuilder().WithScheme(testScheme).Build(),
				Builder:  builder.ControllerManagedBy(&testutil.FakeManager{}),
				Recorder: &record.FakeRecorder{},
			}
			if tt.fields.client != nil {
				testOpt.Client = tt.fields.client
			}
			err = p.Setup(testOpt)
			assert.NoError(t, err)

			gotErr := p.Prepare(tt.args.strategy, tt.args.node, tt.args.nr)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			testingCorrectResourceList(t, &tt.wantField.Status.Capacity, &tt.args.node.Status.Capacity)
			testingCorrectResourceList(t, &tt.wantField.Status.Allocatable, &tt.args.node.Status.Allocatable)
			if tt.wantOriginAllocated != nil {
				getOriginAllocated, err := slov1alpha1.GetOriginExtendedAllocatable(tt.args.node.Annotations)
				assert.NoError(t, err)
				assert.Equal(t, tt.wantOriginAllocated, getOriginAllocated)
			}
		})
	}
}

func TestPluginCalculate(t *testing.T) {
	testScheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = slov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = topov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	memoryCalculateByReq := configuration.CalculateByPodRequest
	memoryCalculateByMaxUsageReq := configuration.CalculateByPodMaxUsageRequest
	cpuCalculateByMaxUsageReq := configuration.CalculateByPodMaxUsageRequest
	cpuCalculateByUsage := configuration.CalculateByPodUsage
	type fields struct {
		client  ctrlclient.Client
		checkFn func(t *testing.T, client ctrlclient.Client)
	}
	type args struct {
		strategy             *configuration.ColocationStrategy
		node                 *corev1.Node
		podList              *corev1.PodList
		resourceMetrics      *framework.ResourceMetrics
		nodeAnnoReservedCase []*extension.NodeReservation
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []framework.ResourceItem
		wantErr bool
	}{
		{
			name:    "error for invalid arguments",
			wantErr: true,
		},
		{
			name: "calculate with memory usage",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve nothing from node.annotation",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve specific cpus from node.annotation and left equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								ReservedCPUs: "0-1",
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve specific cpus from node.annotation and right equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								ReservedCPUs: "0-19",
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(12000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:12000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:20000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(33, 9),
					Message:  "batchAllocatable[Mem(GB)]:33 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:12 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve cpus by quantity from node.annotation and right equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20")},
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(12000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:12000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:20000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(33, 9),
					Message:  "batchAllocatable[Mem(GB)]:33 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:12 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve cpus by quantity and specific cores from node.annotation and right equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources:    corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20")},
								ReservedCPUs: "0-9",
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(22000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:22000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:10000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(33, 9),
					Message:  "batchAllocatable[Mem(GB)]:33 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:12 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve memory from node.annotation and right equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("20G")},
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(25000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:25000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(25, 9),
					Message:  "batchAllocatable[Mem(GB)]:25 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:20 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve memory from node.annotation and left equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("5G")},
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve memory and cpu from node.annotation and left equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("5G"),
									corev1.ResourceCPU:    resource.MustParse("2"),
								},
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve memory and cpu from node.annotation and right equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("20G"),
									corev1.ResourceCPU:    resource.MustParse("10"),
								},
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(22000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:22000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:10000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(25, 9),
					Message:  "batchAllocatable[Mem(GB)]:25 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:20 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve memory and specific cores from node.annotation and right equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("20G"),
								},
								ReservedCPUs: "0-9",
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(22000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:22000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:10000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(25, 9),
					Message:  "batchAllocatable[Mem(GB)]:25 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:20 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage and reserve memory and cpu which kubelet reserved is the biggest among node.annotation, sys.used and kubelet reserved",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("5G"),
									corev1.ResourceCPU:    resource.MustParse("2"),
								},
							}),
						},
					},
					Status: makeNodeStatWithKubeletReserved("90", "100G", "100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(22000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:22000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:10000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(25, 9),
					Message:  "batchAllocatable[Mem(GB)]:25 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:20 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory request",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(70),
					MemoryReclaimThresholdPercent: pointer.Int64(80),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"memory-calculate-by-request": "true",
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with memory request and reserve memory from node.annotation and left equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(70),
					MemoryReclaimThresholdPercent: pointer.Int64(80),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"memory-calculate-by-request": "true",
						},
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("5G")},
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(30000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:30000 = nodeCapacity:100000 - nodeSafetyMargin:30000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(31, 9),
					Message:  "batchAllocatable[Mem(GB)]:31 = nodeCapacity:120 - nodeSafetyMargin:24 - nodeReserved:5 - podHPRequest:60",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory request and reserve memory which kubelet reserved is the biggest among node.annotation, sys.used and kubelet reserve",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(70),
					MemoryReclaimThresholdPercent: pointer.Int64(80),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"memory-calculate-by-request": "true",
						},
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("5G")},
							}),
						},
					},
					Status: makeNodeStatWithKubeletReserved("90", "100G", "100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(27000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:27000 = nodeCapacity:100000 - nodeSafetyMargin:30000 - systemUsageOrNodeReserved:10000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(16, 9),
					Message:  "batchAllocatable[Mem(GB)]:16 = nodeCapacity:120 - nodeSafetyMargin:24 - nodeReserved:20 - podHPRequest:60",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory request and reserve memory from node.annotation and right equal sys.used",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(70),
					MemoryReclaimThresholdPercent: pointer.Int64(80),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"memory-calculate-by-request": "true",
						},
						Annotations: map[string]string{
							extension.AnnotationNodeReservation: util.GetNodeAnnoReservedJson(extension.NodeReservation{
								Resources: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("10G")},
							}),
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(30000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:30000 = nodeCapacity:100000 - nodeSafetyMargin:30000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(26, 9),
					Message:  "batchAllocatable[Mem(GB)]:26 = nodeCapacity:120 - nodeSafetyMargin:24 - nodeReserved:10 - podHPRequest:60",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with pods in multiple priority classes",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Prod by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd1",
								Namespace: "test",
								// missing qos label
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								PriorityClassName: string(extension.PriorityProd),
								Priority:          pointer.Int32(extension.PriorityProdValueMax),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podBatch",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Batch by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podMid",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "20G"),
									}, {
										Resources: makeResourceReq("10", "20G"),
									},
								},
								PriorityClassName: string(extension.PriorityMid),
								Priority:          pointer.Int32(extension.PriorityMidValueMin),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd2",
								Namespace: "test",
								Labels:    map[string]string{},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("7"),
										corev1.ResourceMemory: resource.MustParse("12G"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podProd", "5", "5G"),
								genPodMetric("test", "podProd1", "6", "6G"),
								genPodMetric("test", "podBatch", "10", "10G"),
								genPodMetric("test", "podMid", "22", "22G"),
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with pods terminated",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Prod by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd1",
								Namespace: "test",
								// missing qos label
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								PriorityClassName: string(extension.PriorityProd),
								Priority:          pointer.Int32(extension.PriorityProdValueMax),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
					},
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("25", "30G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("5"),
										corev1.ResourceMemory: resource.MustParse("10G"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podProd", "5", "5G"),
								genPodMetric("test", "podProd2", "10", "10G"),
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(35000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:35000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:5000 - podHPUsed:25000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(43, 9),
					Message:  "batchAllocatable[Mem(GB)]:43 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:10 - podHPUsed:25",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with pods QoS=LSE",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProdLSE",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Prod by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd1",
								Namespace: "test",
								// missing qos label
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								PriorityClassName: string(extension.PriorityProd),
								Priority:          pointer.Int32(extension.PriorityProdValueMax),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podBatch",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Batch by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podMid",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "20G"),
									}, {
										Resources: makeResourceReq("10", "20G"),
									},
								},
								PriorityClassName: string(extension.PriorityMid),
								Priority:          pointer.Int32(extension.PriorityMidValueMin),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd2",
								Namespace: "test",
								Labels:    map[string]string{},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("7"),
										corev1.ResourceMemory: resource.MustParse("12G"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podProdLSE", "5", "5G"),
								genPodMetric("test", "podProd1", "6", "6G"),
								genPodMetric("test", "podBatch", "10", "10G"),
								genPodMetric("test", "podMid", "22", "22G"),
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(20000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:20000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:38000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(33, 9),
					Message:  "batchAllocatable[Mem(GB)]:33 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:12 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with one NUMA-level resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					TopologyPolicies: []string{string(topov1alpha1.None)},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("100"),
									Allocatable: resource.MustParse("100"),
									Available:   resource.MustParse("100"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("120G"),
									Allocatable: resource.MustParse("120G"),
									Available:   resource.MustParse("120G"),
								},
							},
						},
					},
				}).Build(),
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
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Prod by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd1",
								Namespace: "test",
								// missing qos label
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								PriorityClassName: string(extension.PriorityProd),
								Priority:          pointer.Int32(extension.PriorityProdValueMax),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podBatch",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Batch by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podMid",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "20G"),
									}, {
										Resources: makeResourceReq("10", "20G"),
									},
								},
								PriorityClassName: string(extension.PriorityMid),
								Priority:          pointer.Int32(extension.PriorityMidValueMin),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd2",
								Namespace: "test",
								Labels:    map[string]string{},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("7"),
										corev1.ResourceMemory: resource.MustParse("12G"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podProd", "5", "5G"),
								genPodMetric("test", "podProd1", "6", "6G"),
								genPodMetric("test", "podBatch", "10", "10G"),
								genPodMetric("test", "podMid", "22", "22G"),
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(25000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:25000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewQuantity(25000, resource.DecimalSI),
					},
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(33, 9),
					Message:  "batchAllocatable[Mem(GB)]:33 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:12 - podHPUsed:33",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewScaledQuantity(33, 9),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with multiple NUMA-level resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					TopologyPolicies: []string{string(topov1alpha1.None)},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
							},
						},
					},
				}).Build(),
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
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLS),
								},
								Annotations: map[string]string{
									extension.AnnotationResourceStatus: `{
    "numaNodeResources": [
        {
            "node": 0
        }
    ]
}`,
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Prod by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd1",
								Namespace: "test",
								// missing qos label
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								PriorityClassName: string(extension.PriorityProd),
								Priority:          pointer.Int32(extension.PriorityProdValueMax),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podBatch",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Batch by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podMid",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "20G"),
									}, {
										Resources: makeResourceReq("10", "20G"),
									},
								},
								PriorityClassName: string(extension.PriorityMid),
								Priority:          pointer.Int32(extension.PriorityMidValueMin),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd2",
								Namespace: "test",
								Labels:    map[string]string{},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("7"),
										corev1.ResourceMemory: resource.MustParse("12G"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podProd", "5", "5G"),
								genPodMetric("test", "podProd1", "6", "6G"),
								genPodMetric("test", "podBatch", "10", "10G"),
								genPodMetric("test", "podMid", "22", "22G"),
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(25000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:25000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewQuantity(10000, resource.DecimalSI), // 50 - 17.5 - 3.5 - (14 + 5)
						util.GenNodeZoneName(1): *resource.NewQuantity(15000, resource.DecimalSI), // 50 - 17.5 - 3.5 - 14
					},
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(33, 9),
					Message:  "batchAllocatable[Mem(GB)]:33 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:12 - podHPUsed:33",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewScaledQuantity(15300, 6), // 62 - 21.7(62*0.35) - 6 - (14 + 5)
						util.GenNodeZoneName(1): *resource.NewScaledQuantity(17700, 6), // 58 - 20.3(58*0.35) - 6 - 14
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory not over-committed, LSE pods and NUMA-level resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					TopologyPolicies: []string{string(topov1alpha1.None)},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProdLSE",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSE),
								},
								Annotations: map[string]string{
									extension.AnnotationResourceStatus: `{
    "cpuset": "2-5,10-13",
    "numaNodeResources": [
        {
            "node": 0
        }
    ]
}`,
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Prod by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd1",
								Namespace: "test",
								// missing qos label
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								PriorityClassName: string(extension.PriorityProd),
								Priority:          pointer.Int32(extension.PriorityProdValueMax),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podBatch",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Batch by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podMid",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "20G"),
									},
									{
										Resources: makeResourceReq("10", "20G"),
									},
								},
								PriorityClassName: string(extension.PriorityMid),
								Priority:          pointer.Int32(extension.PriorityMidValueMin),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd2",
								Namespace: "test",
								Labels:    map[string]string{},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("7"),
										corev1.ResourceMemory: resource.MustParse("12G"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podProdLSE", "5", "5G"),
								genPodMetric("test", "podProd1", "6", "6G"),
								genPodMetric("test", "podBatch", "10", "10G"),
								genPodMetric("test", "podMid", "22", "22G"),
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(20000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:20000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:38000",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewQuantity(5000, resource.DecimalSI),  // 50 - 17.5 - 3.5 - (14 + 10)
						util.GenNodeZoneName(1): *resource.NewQuantity(15000, resource.DecimalSI), // 50 - 17.5 - 3.5 - 14
					},
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(18, 9),
					Message:  "batchAllocatable[Mem(GB)]:18 = nodeCapacity:120 - nodeSafetyMargin:42 - nodeReserved:0 - podHPRequest:60",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewScaledQuantity(5300, 6),  // 62 - 21.7(62*0.35) - (25 + 10)
						util.GenNodeZoneName(1): *resource.NewScaledQuantity(12700, 6), // 58 - 20.3(58*0.35) - 25
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate NUMA resources with memory not over-committed, missing metric LSE pod and dangling pod",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					TopologyPolicies: []string{string(topov1alpha1.None)},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProdLSE",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSE),
								},
								Annotations: map[string]string{
									extension.AnnotationResourceStatus: `{
    "cpuset": "2-5,10-13",
    "numaNodeResources": [
        {
            "node": 0
        }
    ]
}`,
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Prod by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd1",
								Namespace: "test",
								// missing qos label
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								PriorityClassName: string(extension.PriorityProd),
								Priority:          pointer.Int32(extension.PriorityProdValueMax),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podBatch",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Batch by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podMid",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "20G"),
									},
									{
										Resources: makeResourceReq("10", "20G"),
									},
								},
								PriorityClassName: string(extension.PriorityMid),
								Priority:          pointer.Int32(extension.PriorityMidValueMin),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd2",
								Namespace: "test",
								Labels:    map[string]string{},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("7"),
										corev1.ResourceMemory: resource.MustParse("12G"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetricWithSLO("test", "podProd1", "6", "6G", extension.PriorityProd, extension.QoSLS),
								genPodMetricWithSLO("test", "podProd2", "2", "4G", extension.PriorityProd, extension.QoSLSR), // dangling
								genPodMetricWithSLO("test", "podBatch", "10", "10G", extension.PriorityBatch, extension.QoSBE),
								genPodMetricWithSLO("test", "podMid", "22", "22G", extension.PriorityMid, extension.QoSBE),
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(18000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:18000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:40000",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewQuantity(4000, resource.DecimalSI),  // 50 - 17.5 - 3.5 - (14 + 10 + 1)
						util.GenNodeZoneName(1): *resource.NewQuantity(14000, resource.DecimalSI), // 50 - 17.5 - 3.5 - (14 + 1)
					},
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(18, 9),
					Message:  "batchAllocatable[Mem(GB)]:18 = nodeCapacity:120 - nodeSafetyMargin:42 - nodeReserved:0 - podHPRequest:60",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewScaledQuantity(5300, 6),  // 62 - 21.7(62*0.35) - (25 + 10)
						util.GenNodeZoneName(1): *resource.NewScaledQuantity(12700, 6), // 58 - 20.3(58*0.35) - 25
					},
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with cpu maxUsageRequest and memory request",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(70),
					CPUCalculatePolicy:            &cpuCalculateByMaxUsageReq,
					MemoryReclaimThresholdPercent: pointer.Int64(80),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"cpu-calculate-by-request":    "true",
							"memory-calculate-by-request": "true",
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(21000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:21000 = nodeCapacity:100000 - nodeSafetyMargin:30000 - systemUsageOrNodeReserved:7000 - podHPMaxUsedRequest:42000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(36, 9),
					Message:  "batchAllocatable[Mem(GB)]:36 = nodeCapacity:120 - nodeSafetyMargin:24 - nodeReserved:0 - podHPRequest:60",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with adjusted reclaim ratio",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(150),
					CPUCalculatePolicy:            &cpuCalculateByMaxUsageReq,
					MemoryReclaimThresholdPercent: pointer.Int64(120),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"cpu-calculate-by-request":    "true",
							"memory-calculate-by-request": "true",
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(101000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:101000 = nodeCapacity:100000 - nodeSafetyMargin:-50000 - systemUsageOrNodeReserved:7000 - podHPMaxUsedRequest:42000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(84, 9),
					Message:  "batchAllocatable[Mem(GB)]:84 = nodeCapacity:120 - nodeSafetyMargin:-24 - nodeReserved:0 - podHPRequest:60",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with batch cpu threshold percent only",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(150),
					CPUCalculatePolicy:            &cpuCalculateByMaxUsageReq,
					MemoryReclaimThresholdPercent: pointer.Int64(120),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
					BatchCPUThresholdPercent:      pointer.Int64(100),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"cpu-calculate-by-request":    "true",
							"memory-calculate-by-request": "true",
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(100000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:100000 = min(nodeCapacity:100000 * thresholdRatio:1, nodeCapacity:100000 - nodeSafetyMargin:-50000 - systemUsageOrNodeReserved:7000 - podHPMaxUsedRequest:42000)",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(84, 9),
					Message:  "batchAllocatable[Mem(GB)]:84 = nodeCapacity:120 - nodeSafetyMargin:-24 - nodeReserved:0 - podHPRequest:60",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with batch memory threshold percent only",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(150),
					CPUCalculatePolicy:            &cpuCalculateByMaxUsageReq,
					MemoryReclaimThresholdPercent: pointer.Int64(120),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
					BatchMemoryThresholdPercent:   pointer.Int64(100),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"cpu-calculate-by-request":    "true",
							"memory-calculate-by-request": "true",
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(101000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:101000 = nodeCapacity:100000 - nodeSafetyMargin:-50000 - systemUsageOrNodeReserved:7000 - podHPMaxUsedRequest:42000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(84, 9),
					Message:  "batchAllocatable[Mem(GB)]:84 = min(nodeCapacity:120 * thresholdRatio:1, nodeCapacity:120 - nodeSafetyMargin:-24 - nodeReserved:0 - podHPRequest:60)",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with both batch cpu/mem threshold percent",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(150),
					CPUCalculatePolicy:            &cpuCalculateByMaxUsageReq,
					MemoryReclaimThresholdPercent: pointer.Int64(120),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
					BatchCPUThresholdPercent:      pointer.Int64(100),
					BatchMemoryThresholdPercent:   pointer.Int64(100),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"cpu-calculate-by-request":    "true",
							"memory-calculate-by-request": "true",
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(100000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:100000 = min(nodeCapacity:100000 * thresholdRatio:1, nodeCapacity:100000 - nodeSafetyMargin:-50000 - systemUsageOrNodeReserved:7000 - podHPMaxUsedRequest:42000)",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(84, 9),
					Message:  "batchAllocatable[Mem(GB)]:84 = min(nodeCapacity:120 * thresholdRatio:1, nodeCapacity:120 - nodeSafetyMargin:-24 - nodeReserved:0 - podHPRequest:60)",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate without batch cpu/mem threshold percent-with different xxxCalculatePolicy",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(150),
					CPUCalculatePolicy:            &cpuCalculateByUsage,
					MemoryReclaimThresholdPercent: pointer.Int64(120),
					MemoryCalculatePolicy:         &memoryCalculateByMaxUsageReq,
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"cpu-calculate-by-request":    "true",
							"memory-calculate-by-request": "true",
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(110000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:110000 = nodeCapacity:100000 - nodeSafetyMargin:-50000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(72, 9),
					Message:  "batchAllocatable[Mem(GB)]:72 = nodeCapacity:120 - nodeSafetyMargin:-24 - systemUsage:12 - podHPMaxUsedRequest:60",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with both batch cpu/mem threshold percent-with different xxxCalculatePolicy",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
					CPUReclaimThresholdPercent:    pointer.Int64(150),
					CPUCalculatePolicy:            &cpuCalculateByUsage,
					MemoryReclaimThresholdPercent: pointer.Int64(120),
					MemoryCalculatePolicy:         &memoryCalculateByMaxUsageReq,
					BatchCPUThresholdPercent:      pointer.Int64(100),
					BatchMemoryThresholdPercent:   pointer.Int64(100),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
						Labels: map[string]string{
							"cpu-calculate-by-request":    "true",
							"memory-calculate-by-request": "true",
						},
					},
					Status: makeNodeStat("100", "120G"),
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(100000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:100000 = min(nodeCapacity:100000 * thresholdRatio:1, nodeCapacity:100000 - nodeSafetyMargin:-50000 - systemUsageOrNodeReserved:7000 - podHPUsed:33000)",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(72, 9),
					Message:  "batchAllocatable[Mem(GB)]:72 = min(nodeCapacity:120 * thresholdRatio:1, nodeCapacity:120 - nodeSafetyMargin:-24 - systemUsage:12 - podHPMaxUsedRequest:60)",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage, including product host application usage",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("4", "6G"),
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podA", "11", "11G"),
								genPodMetric("test", "podB", "10", "10G"),
								genPodMetric("test", "podC", "22", "22G"),
							},
							HostApplicationMetric: []*slov1alpha1.HostApplicationMetricInfo{
								{
									Name: "test-product-host-application",
									Usage: slov1alpha1.ResourceMap{
										ResourceList: makeResourceList("3", "6G"),
									},
									Priority: extension.PriorityProd,
								},
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage, including mid host application usage",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("4", "6G"),
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podA", "11", "11G"),
								genPodMetric("test", "podB", "10", "10G"),
								genPodMetric("test", "podC", "22", "22G"),
							},
							HostApplicationMetric: []*slov1alpha1.HostApplicationMetricInfo{
								{
									Name: "test-mid-host-application",
									Usage: slov1alpha1.ResourceMap{
										ResourceList: makeResourceList("3", "6G"),
									},
									Priority: extension.PriorityMid,
								},
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
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
			},
			wantErr: false,
		},
		{
			name: "calculate with memory usage, including batch host application usage",
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("4", "6G"),
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetric("test", "podA", "11", "11G"),
								genPodMetric("test", "podB", "10", "10G"),
								genPodMetric("test", "podC", "22", "22G"),
							},
							HostApplicationMetric: []*slov1alpha1.HostApplicationMetricInfo{
								{
									Name: "test-batch-host-application",
									Usage: slov1alpha1.ResourceMap{
										ResourceList: makeResourceList("3", "6G"),
									},
									Priority: extension.PriorityBatch,
								},
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(28000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:28000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:4000 - podHPUsed:33000",
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(39, 9),
					Message:  "batchAllocatable[Mem(GB)]:39 = nodeCapacity:120 - nodeSafetyMargin:42 - systemUsage:6 - podHPUsed:33",
				},
			},
			wantErr: false,
		},
		{
			name: "calculate NUMA resources with memory not over-committed, some pods not shown in list",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(&topov1alpha1.NodeResourceTopology{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					TopologyPolicies: []string{string(topov1alpha1.None)},
					Zones: topov1alpha1.ZoneList{
						{
							Name: util.GenNodeZoneName(0),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("62G"),
									Allocatable: resource.MustParse("62G"),
									Available:   resource.MustParse("62G"),
								},
							},
						},
						{
							Name: util.GenNodeZoneName(1),
							Type: util.NodeZoneType,
							Resources: topov1alpha1.ResourceInfoList{
								{
									Name:        string(corev1.ResourceCPU),
									Capacity:    resource.MustParse("50"),
									Allocatable: resource.MustParse("50"),
									Available:   resource.MustParse("50"),
								},
								{
									Name:        string(corev1.ResourceMemory),
									Capacity:    resource.MustParse("58G"),
									Allocatable: resource.MustParse("58G"),
									Available:   resource.MustParse("58G"),
								},
							},
						},
					},
				}).Build(),
			},
			args: args{
				strategy: &configuration.ColocationStrategy{
					Enable:                        pointer.Bool(true),
					CPUReclaimThresholdPercent:    pointer.Int64(65),
					MemoryReclaimThresholdPercent: pointer.Int64(65),
					MemoryCalculatePolicy:         &memoryCalculateByReq,
					DegradeTimeMinutes:            pointer.Int64(15),
					UpdateTimeThresholdSeconds:    pointer.Int64(300),
					ResourceDiffThreshold:         pointer.Float64(0.1),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node1",
					},
					Status: makeNodeStat("100", "120G"),
				},
				podList: &corev1.PodList{
					Items: []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProdLSE",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSLSE),
								},
								Annotations: map[string]string{
									extension.AnnotationResourceStatus: `{
    "cpuset": "2-5,10-13",
    "numaNodeResources": [
        {
            "node": 0
        }
    ]
}`,
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Prod by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd1",
								Namespace: "test",
								// missing qos label
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								PriorityClassName: string(extension.PriorityProd),
								Priority:          pointer.Int32(extension.PriorityProdValueMax),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podBatch",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
								// regarded as Batch by default
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodRunning,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podMid",
								Namespace: "test",
								Labels: map[string]string{
									extension.LabelPodQoS: string(extension.QoSBE),
								},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "20G"),
									},
									{
										Resources: makeResourceReq("10", "20G"),
									},
								},
								PriorityClassName: string(extension.PriorityMid),
								Priority:          pointer.Int32(extension.PriorityMidValueMin),
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodPending,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "podProd2",
								Namespace: "test",
								Labels:    map[string]string{},
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node1",
								Containers: []corev1.Container{
									{
										Resources: makeResourceReq("10", "10G"),
									},
								},
							},
							Status: corev1.PodStatus{
								Phase: corev1.PodSucceeded,
							},
						},
					},
				},
				resourceMetrics: &framework.ResourceMetrics{
					NodeMetric: &slov1alpha1.NodeMetric{
						Status: slov1alpha1.NodeMetricStatus{
							UpdateTime: &metav1.Time{Time: time.Now()},
							NodeMetric: &slov1alpha1.NodeMetricInfo{
								NodeUsage: slov1alpha1.ResourceMap{
									ResourceList: makeResourceList("50", "55G"),
								},
								SystemUsage: slov1alpha1.ResourceMap{
									ResourceList: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("7"),
										corev1.ResourceMemory: resource.MustParse("12G"),
									},
								},
							},
							PodsMetric: []*slov1alpha1.PodMetricInfo{
								genPodMetricWithSLO("test", "podProd1", "6", "6G", extension.PriorityProd, extension.QoSLS),
								genPodMetricWithSLO("test", "podProd2", "2", "4G", extension.PriorityProd, extension.QoSLSR), // dangling
								genPodMetricWithSLO("test", "podBatch", "10", "10G", extension.PriorityBatch, extension.QoSBE),
								genPodMetricWithSLO("test", "podMid", "22", "22G", extension.PriorityMid, extension.QoSBE),
								genPodMetricWithSLO("test", "podProd3", "4", "8G", extension.PriorityProd, extension.QoSLS),
								genPodMetricWithSLO("test", "podBatch2", "8", "8G", extension.PriorityBatch, extension.QoSBE),
							},
						},
					},
				},
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.BatchCPU,
					Quantity: resource.NewQuantity(14000, resource.DecimalSI),
					Message:  "batchAllocatable[CPU(Milli-Core)]:14000 = nodeCapacity:100000 - nodeSafetyMargin:35000 - systemUsageOrNodeReserved:7000 - podHPUsed:44000",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewQuantity(2000, resource.DecimalSI),  // 50 - 17.5 - 3.5 - (14 + 10 + 1 + 2)
						util.GenNodeZoneName(1): *resource.NewQuantity(12000, resource.DecimalSI), // 50 - 17.5 - 3.5 - (14 + 1 + 2)
					},
				},
				{
					Name:     extension.BatchMemory,
					Quantity: resource.NewScaledQuantity(18, 9),
					Message:  "batchAllocatable[Mem(GB)]:18 = nodeCapacity:120 - nodeSafetyMargin:42 - nodeReserved:0 - podHPRequest:60",
					ZoneQuantity: map[string]resource.Quantity{
						util.GenNodeZoneName(0): *resource.NewScaledQuantity(5300, 6),  // 62 - 21.7(62*0.35) - (25 + 10)
						util.GenNodeZoneName(1): *resource.NewScaledQuantity(12700, 6), // 58 - 20.3(58*0.35) - 25
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer testPluginCleanup()
			p := &Plugin{}
			assert.Equal(t, PluginName, p.Name())
			testOpt := &framework.Option{
				Scheme:   testScheme,
				Client:   fake.NewClientBuilder().WithScheme(testScheme).Build(),
				Builder:  builder.ControllerManagedBy(&testutil.FakeManager{}),
				Recorder: &record.FakeRecorder{},
			}
			if tt.fields.client != nil {
				testOpt.Client = tt.fields.client
			}
			err = p.Setup(testOpt)
			assert.NoError(t, err)

			podList := tt.args.podList
			if podList == nil {
				podList = getTestPodList()
			}
			resourceMetrics := tt.args.resourceMetrics
			if resourceMetrics == nil {
				resourceMetrics = getTestResourceMetrics()
			}
			got, gotErr := p.Calculate(tt.args.strategy, tt.args.node, podList, resourceMetrics)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			testingCorrectResourceItems(t, tt.want, got)
		})
	}
}

func TestPlugin_isDegradeNeeded(t *testing.T) {
	const degradeTimeoutMinutes = 10
	type fields struct {
		Clock *fakeclock.FakeClock
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
			fields: fields{
				Clock: &fakeclock.FakeClock{},
			},
			args: args{
				nodeMetric: nil,
			},
			want: true,
		},
		{
			name: "empty NodeMetric status should degrade",
			fields: fields{
				Clock: &fakeclock.FakeClock{},
			},
			args: args{
				nodeMetric: &slov1alpha1.NodeMetric{},
			},
			want: true,
		},
		{
			name: "outdated NodeMetric status should degrade",
			fields: fields{
				Clock: nil,
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
		{
			name: "outdated NodeMetric status should degrade 1",
			fields: fields{
				Clock: fakeclock.NewFakeClock(time.Now().Add(time.Minute * (degradeTimeoutMinutes + 1))),
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.fields.Clock != nil {
				oldClock := Clock
				Clock = tt.fields.Clock
				defer func() {
					Clock = oldClock
				}()
			}

			p := &Plugin{}
			assert.Equal(t, tt.want, p.isDegradeNeeded(tt.args.strategy, tt.args.nodeMetric, tt.args.node))
		})
	}
}

func testingCorrectResourceItems(t *testing.T, want, got []framework.ResourceItem) {
	assert.Equal(t, len(want), len(got))
	for i := range want {
		qWant, qGot := want[i].Quantity, got[i].Quantity
		want[i].Quantity, got[i].Quantity = nil, nil
		var zoneQWant, zoneQGot map[string]resource.Quantity
		if want[i].ZoneQuantity != nil {
			zoneQWant, zoneQGot = want[i].ZoneQuantity, got[i].ZoneQuantity
			want[i].ZoneQuantity, got[i].ZoneQuantity = nil, nil
			assert.Equal(t, len(zoneQWant), len(zoneQGot))
			for k := range zoneQWant {
				zqWant, zqGot := zoneQWant[k], zoneQGot[k]
				assert.Equal(t, (&zqWant).MilliValue(), (&zqGot).MilliValue(), fmt.Sprintf("item %v, zone %s", i, k))
			}
		}
		assert.Equal(t, want[i], got[i], "equal fields for resource "+want[i].Name)
		assert.Equal(t, qWant.MilliValue(), qGot.MilliValue(), "equal values for resource "+want[i].Name)
		want[i].Quantity, got[i].Quantity = qWant, qGot
		if zoneQWant != nil {
			want[i].ZoneQuantity, got[i].ZoneQuantity = zoneQWant, zoneQGot
		}
	}
}

func testingCorrectResourceList(t *testing.T, want, got *corev1.ResourceList) {
	assert.Equal(t, want.Cpu().MilliValue(), got.Cpu().MilliValue(), "should get correct cpu request")
	assert.Equal(t, want.Memory().Value(), got.Memory().Value(), "should get correct memory request")
	if _, ok := (*want)[extension.BatchCPU]; ok {
		qWant, qGot := (*want)[extension.BatchCPU], (*got)[extension.BatchCPU]
		assert.Equal(t, qWant.MilliValue(), qGot.MilliValue(), "should get correct batch-cpu")
	}
	if _, ok := (*want)[extension.BatchMemory]; ok {
		qWant, qGot := (*want)[extension.BatchMemory], (*got)[extension.BatchMemory]
		assert.Equal(t, qWant.Value(), qGot.Value(), "should get correct batch-memory")
	}
}

func testPluginCleanup() {
	client = nil
}
