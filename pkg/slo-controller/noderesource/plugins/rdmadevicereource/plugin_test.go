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

package rdmadeviceresource

import (
	"testing"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util/testutil"
)

func TestPlugin(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		p := &Plugin{}
		assert.Equal(t, PluginName, p.Name())

		testScheme := runtime.NewScheme()
		testOpt := &framework.Option{
			Scheme:  testScheme,
			Client:  fake.NewClientBuilder().WithScheme(testScheme).Build(),
			Builder: builder.ControllerManagedBy(&testutil.FakeManager{}),
		}
		err := p.Setup(testOpt)
		assert.NoError(t, err)

		got := p.Reset(nil, "")
		assert.Nil(t, got)
	})
}

func TestPluginNeedSync(t *testing.T) {
	testStrategy := &configuration.ColocationStrategy{
		Enable:                        pointer.Bool(true),
		CPUReclaimThresholdPercent:    pointer.Int64(65),
		MemoryReclaimThresholdPercent: pointer.Int64(65),
		DegradeTimeMinutes:            pointer.Int64(15),
		UpdateTimeThresholdSeconds:    pointer.Int64(300),
		ResourceDiffThreshold:         pointer.Float64(0.1),
	}
	testNodeWithoutDevice := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
		},
	}
	testNodeWithDevice := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
	}
	testNodeWithDeviceDriverUpdate := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
	}
	testNodeWithDeviceResourceUpdate := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(300, resource.DecimalSI),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(300, resource.DecimalSI),
			},
		},
	}
	t.Run("test", func(t *testing.T) {
		p := &Plugin{}

		// nothing change, both have no gpu device
		got, got1 := p.NeedSync(testStrategy, testNodeWithoutDevice, testNodeWithoutDevice)
		assert.False(t, got)
		assert.Equal(t, "", got1)
		// nothing change, both has gpu devices
		got, got1 = p.NeedSync(testStrategy, testNodeWithDevice, testNodeWithDevice)
		assert.False(t, got)
		assert.Equal(t, "", got1)
		// ignore labels change
		got, got1 = p.NeedSync(testStrategy, testNodeWithDevice, testNodeWithDeviceDriverUpdate)
		assert.False(t, got)
		assert.Equal(t, "", got1)

		// add resources
		got, got1 = p.NeedSync(testStrategy, testNodeWithoutDevice, testNodeWithDevice)
		assert.True(t, got)
		assert.Equal(t, NeedSyncForResourceDiffMsg, got1)
		// resource update
		got, got1 = p.NeedSync(testStrategy, testNodeWithDevice, testNodeWithDeviceResourceUpdate)
		assert.True(t, got)
		assert.Equal(t, NeedSyncForResourceDiffMsg, got1)

		// delete resources
		got, got1 = p.NeedSync(testStrategy, testNodeWithDevice, testNodeWithoutDevice)
		assert.True(t, got)
		assert.Equal(t, NeedSyncForResourceDiffMsg, got1)
	})
}

func TestPluginPrepare(t *testing.T) {
	testNodeWithoutDevice := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
		},
	}
	testNodeWithDevice := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
	}
	testNodeWithoutDeviceResources := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
		},
	}
	type args struct {
		node *corev1.Node
		nr   *framework.NodeResource
	}
	tests := []struct {
		name      string
		args      args
		wantErr   bool
		wantField *corev1.Node
	}{
		{
			name: "nothing to prepare",
			args: args{
				node: testNodeWithoutDevice,
				nr:   framework.NewNodeResource(),
			},
			wantErr:   false,
			wantField: testNodeWithoutDevice,
		},
		{
			name: "update resources and labels correctly",
			args: args{
				node: testNodeWithoutDevice,
				nr: &framework.NodeResource{
					Resources: map[corev1.ResourceName]*resource.Quantity{
						extension.ResourceRDMA: resource.NewQuantity(200, resource.DecimalSI),
					},
					ZoneResources: map[string]corev1.ResourceList{},
					Messages:      map[corev1.ResourceName]string{},
					Resets:        map[corev1.ResourceName]bool{},
				},
			},
			wantErr:   false,
			wantField: testNodeWithDevice,
		},
		{
			name: "reset resources correctly",
			args: args{
				node: testNodeWithDevice,
				nr: &framework.NodeResource{
					Resets: map[corev1.ResourceName]bool{
						extension.ResourceRDMA: true,
					},
				},
			},
			wantErr:   false,
			wantField: testNodeWithoutDeviceResources,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			gotErr := p.Prepare(nil, tt.args.node, tt.args.nr)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.wantField, tt.args.node)
		})
	}
}

func TestPluginCalculate(t *testing.T) {
	testScheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = topov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = schedulingv1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
		},
	}
	testDevice := &schedulingv1alpha1.Device{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNode.Name,
			Labels: map[string]string{
				extension.LabelGPUModel:         "A100",
				extension.LabelGPUDriverVersion: "480",
			},
		},
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{
					UUID:   "1",
					Minor:  pointer.Int32(0),
					Health: true,
					Type:   schedulingv1alpha1.RDMA,
					Resources: map[corev1.ResourceName]resource.Quantity{
						extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
					},
				},
				{
					UUID:   "2",
					Minor:  pointer.Int32(1),
					Health: true,
					Type:   schedulingv1alpha1.RDMA,
					Resources: map[corev1.ResourceName]resource.Quantity{
						extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
					},
				},
			},
		},
	}
	deviceMissingRDMA := &schedulingv1alpha1.Device{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNode.Name,
		},
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{},
		},
	}
	type fields struct {
		client ctrlclient.Client
	}
	type args struct {
		node *corev1.Node
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []framework.ResourceItem
		wantErr bool
	}{
		{
			name: "args missing essential fields",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).Build(),
			},
			args: args{
				node: &corev1.Node{},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "get device object error",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
			},
			args: args{
				node: testNode,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "calculate device resources correctly",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNode, testDevice).Build(),
			},
			args: args{
				node: testNode,
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.ResourceRDMA,
					Quantity: resource.NewQuantity(200, resource.DecimalSI),
					Message:  UpdateResourcesMsg,
				},
			},
			wantErr: false,
		},
		{
			name: "calculate device resources correctly",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNode, testDevice).Build(),
			},
			args: args{
				node: testNode,
			},
			want: []framework.ResourceItem{
				{
					Name:     extension.ResourceRDMA,
					Quantity: resource.NewQuantity(200, resource.DecimalSI),
					Message:  UpdateResourcesMsg,
				},
			},
			wantErr: false,
		},
		{
			name: "calculate resetting device resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNode).Build(),
			},
			args: args{
				node: testNode,
			},
			want: []framework.ResourceItem{
				{
					Name:    extension.ResourceRDMA,
					Reset:   true,
					Message: ResetResourcesMsg,
				},
			},
			wantErr: false,
		},
		{
			name: "calculate resetting device resources",
			fields: fields{
				client: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(testNode, deviceMissingRDMA).Build(),
			},
			args: args{
				node: testNode,
			},
			want: []framework.ResourceItem{
				{
					Name:    extension.ResourceRDMA,
					Reset:   true,
					Message: ResetResourcesMsg,
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			client = tt.fields.client
			defer testPluginCleanup()
			got, gotErr := p.Calculate(nil, tt.args.node, nil, nil)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_cleanupGPUNodeResource(t *testing.T) {
	testScheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = topov1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	err = schedulingv1alpha1.AddToScheme(testScheme)
	assert.NoError(t, err)
	testNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				"test-label": "test-value",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:    resource.MustParse("100"),
				corev1.ResourceMemory: resource.MustParse("400Gi"),
			},
		},
	}
	testNodeWithoutLabels := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				"test-label": "test-value",
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceCPU:     resource.MustParse("100"),
				corev1.ResourceMemory:  resource.MustParse("400Gi"),
				extension.ResourceRDMA: *resource.NewQuantity(200, resource.DecimalSI),
			},
		},
	}
	t.Run("cleanup success", func(t *testing.T) {
		p := &Plugin{}
		client = fake.NewClientBuilder().WithScheme(testScheme).Build()
		defer testPluginCleanup()
		node := testNodeWithoutLabels.DeepCopy()
		resourceItems, err := p.Calculate(nil, node, nil, nil)
		assert.NoError(t, err, "expect calculate success")
		nr := framework.NewNodeResource(resourceItems...)
		err = p.Prepare(nil, node, nr)
		assert.NoError(t, err)
		assert.Equal(t, testNode, node)
	})
}

func testPluginCleanup() {
	client = nil
}
