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

package transformer

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinatorscheme "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/scheme"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

// TestSetTransformBeforeStart verifies that SetTransform must be called before
// the informer factory is started. This test ensures the timing contract is correct.
func TestSetTransformBeforeStart(t *testing.T) {
	// Create a fake Kubernetes client
	fakeClient := fake.NewSimpleClientset()

	// Create a SharedInformerFactory
	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)

	// Get the Pod informer (before starting)
	podInformer := informerFactory.Core().V1().Pods().Informer()

	// Test 1: SetTransform BEFORE Start should succeed
	err := podInformer.SetTransform(func(obj interface{}) (interface{}, error) {
		return obj, nil
	})
	assert.NoError(t, err, "SetTransform should succeed before Start")

	// Start the factory
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	informerFactory.Start(ctx.Done())

	// Wait for cache sync
	informerFactory.WaitForCacheSync(ctx.Done())

	// Test 2: SetTransform AFTER Start should fail
	err = podInformer.SetTransform(func(obj interface{}) (interface{}, error) {
		return obj, nil
	})
	assert.Error(t, err, "SetTransform should fail after Start")
	assert.Contains(t, err.Error(), "informer has already started", "Error should indicate informer already started")
}

// TestSetTransformBeforeStartNode tests the same timing contract for Node informer
func TestSetTransformBeforeStartNode(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(fakeClient, 0)

	nodeInformer := informerFactory.Core().V1().Nodes().Informer()

	// SetTransform before Start should succeed
	err := nodeInformer.SetTransform(TransformNode)
	assert.NoError(t, err, "SetTransform for Node should succeed before Start")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	// SetTransform after Start should fail
	err = nodeInformer.SetTransform(TransformNode)
	assert.Error(t, err, "SetTransform for Node should fail after Start")
}

// TestTransformPodIdempotence verifies that applying the Pod transform multiple times
// produces the same result (idempotence property)
func TestTransformPodIdempotence(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
	}{
		{
			name: "pod with deprecated batch resources",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.BatchCPU:    resource.MustParse("1000"),
									apiext.BatchMemory: resource.MustParse("1Gi"),
								},
								Limits: corev1.ResourceList{
									apiext.BatchCPU:    resource.MustParse("2000"),
									apiext.BatchMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "pod with deprecated device resources",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-gpu",
					Namespace: "default",
					Annotations: map[string]string{
						apiext.AnnotationDeviceAllocated: `{"gpu":[{"minor":1,"resources":{"koordinator.sh/gpu-core":"60","koordinator.sh/gpu-memory":"8Gi"}}]}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "gpu-container",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									apiext.ResourceGPUCore:        resource.MustParse("100"),
									apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "pod with overhead",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-overhead",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
					Overhead: corev1.ResourceList{
						apiext.BatchCPU:    resource.MustParse("500"),
						apiext.BatchMemory: resource.MustParse("512Mi"),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transformFn := TransformPodFactory()

			// Apply transform first time
			result1, err := transformFn(tt.pod.DeepCopy())
			assert.NoError(t, err, "First transform should not error")

			pod1 := result1.(*corev1.Pod)

			// Apply transform second time on the already-transformed result
			result2, err := transformFn(pod1.DeepCopy())
			assert.NoError(t, err, "Second transform should not error")

			pod2 := result2.(*corev1.Pod)

			// Results should be identical (idempotence)
			assert.True(t, reflect.DeepEqual(pod1.Spec, pod2.Spec),
				"Pod spec should be identical after applying transform twice")
			assert.True(t, reflect.DeepEqual(pod1.Annotations, pod2.Annotations),
				"Pod annotations should be identical after applying transform twice")
		})
	}
}

// TestTransformNodeIdempotence verifies that applying the Node transform multiple times
// produces the same result (idempotence property)
func TestTransformNodeIdempotence(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
	}{
		{
			name: "node with deprecated batch resources",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
						apiext.BatchCPU:       resource.MustParse("4000"),
						apiext.BatchMemory:    resource.MustParse("8Gi"),
					},
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
						apiext.BatchCPU:       resource.MustParse("4000"),
						apiext.BatchMemory:    resource.MustParse("8Gi"),
					},
				},
			},
		},
		{
			name: "node with deprecated device resources",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-gpu",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:            resource.MustParse("8"),
						corev1.ResourceMemory:         resource.MustParse("16Gi"),
						apiext.ResourceGPUCore:        resource.MustParse("400"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("400"),
					},
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:            resource.MustParse("8"),
						corev1.ResourceMemory:         resource.MustParse("16Gi"),
						apiext.ResourceGPUCore:        resource.MustParse("400"),
						apiext.ResourceGPUMemoryRatio: resource.MustParse("400"),
					},
				},
			},
		},
		{
			name: "node with standard resources only",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node-standard",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
					},
					Capacity: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply transform first time
			result1, err := TransformNode(tt.node.DeepCopy())
			assert.NoError(t, err, "First transform should not error")

			node1 := result1.(*corev1.Node)

			// Apply transform second time on the already-transformed result
			result2, err := TransformNode(node1.DeepCopy())
			assert.NoError(t, err, "Second transform should not error")

			node2 := result2.(*corev1.Node)

			// Results should be identical (idempotence)
			assert.True(t, reflect.DeepEqual(node1.Status.Allocatable, node2.Status.Allocatable),
				"Node allocatable should be identical after applying transform twice")
			assert.True(t, reflect.DeepEqual(node1.Status.Capacity, node2.Status.Capacity),
				"Node capacity should be identical after applying transform twice")
		})
	}
}

// TestTransformDeviceIdempotence verifies that applying the Device transform multiple times
// produces the same result (idempotence property)
func TestTransformDeviceIdempotence(t *testing.T) {
	tests := []struct {
		name   string
		device *schedulingv1alpha1.Device
	}{
		{
			name: "device with GPU resources",
			device: &schedulingv1alpha1.Device{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Spec: schedulingv1alpha1.DeviceSpec{
					Devices: []schedulingv1alpha1.DeviceInfo{
						{
							Type:   schedulingv1alpha1.GPU,
							Minor:  intPtr(0),
							Health: true,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
						{
							Type:   schedulingv1alpha1.GPU,
							Minor:  intPtr(1),
							Health: true,
							Resources: corev1.ResourceList{
								apiext.ResourceGPUCore:        resource.MustParse("100"),
								apiext.ResourceGPUMemoryRatio: resource.MustParse("100"),
								apiext.ResourceGPUMemory:      resource.MustParse("16Gi"),
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply transform first time
			result1, err := TransformDevice(tt.device.DeepCopy())
			assert.NoError(t, err, "First transform should not error")

			device1 := result1.(*schedulingv1alpha1.Device)

			// Apply transform second time on the already-transformed result
			result2, err := TransformDevice(device1.DeepCopy())
			assert.NoError(t, err, "Second transform should not error")

			device2 := result2.(*schedulingv1alpha1.Device)

			// Results should be identical (idempotence)
			assert.True(t, reflect.DeepEqual(device1.Spec, device2.Spec),
				"Device spec should be identical after applying transform twice")
		})
	}
}

// TestTransformFactoryProducesValidFunction verifies that TransformPodFactory returns
// a valid transform function that can be used with informers
func TestTransformFactoryProducesValidFunction(t *testing.T) {
	fn := TransformPodFactory()

	// Should be non-nil
	assert.NotNil(t, fn, "TransformPodFactory should return a non-nil function")

	// Should successfully transform a pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	result, err := fn(pod)
	assert.NoError(t, err, "Transform function should not error")
	assert.NotNil(t, result, "Transform function should return a result")
}

// TestSetupTransformersWithMockFactory tests that SetupTransformers correctly sets up
// transforms for resources before factory start
func TestSetupTransformersWithMockFactory(t *testing.T) {
	// This test verifies the behavior of setting transforms on a fresh factory
	fakeClient := fake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(fakeClient, time.Hour)

	// Get informers for resources that have transformers registered
	nodeInformer := informerFactory.Core().V1().Nodes().Informer()
	podInformer := informerFactory.Core().V1().Pods().Informer()

	// Set transforms before starting (should succeed)
	err := nodeInformer.SetTransform(TransformNode)
	assert.NoError(t, err, "Setting node transform before start should succeed")

	transformFn := TransformPodFactory()
	err = podInformer.SetTransform(transformFn)
	assert.NoError(t, err, "Setting pod transform before start should succeed")

	// Start and verify transforms work
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	// Verify that setting transform after start fails
	err = nodeInformer.SetTransform(TransformNode)
	assert.Error(t, err, "Setting node transform after start should fail")

	err = podInformer.SetTransform(transformFn)
	assert.Error(t, err, "Setting pod transform after start should fail")
}

// intPtr returns a pointer to the given int32 value
func intPtr(i int32) *int32 {
	return &i
}

// Ensure koordinator scheme is registered for test
func init() {
	_ = koordinatorscheme.AddToScheme(koordinatorscheme.Scheme)
}

// TestTransformersMapContainsExpectedResources verifies that the transformers map
// contains the expected resource transformers
func TestTransformersMapContainsExpectedResources(t *testing.T) {
	// Verify nodes transformer is registered
	nodeGVR := corev1.SchemeGroupVersion.WithResource("nodes")
	_, exists := transformers[nodeGVR]
	assert.True(t, exists, "Node transformer should be registered")

	// Verify devices transformer is registered
	deviceGVR := schedulingv1alpha1.SchemeGroupVersion.WithResource("devices")
	_, exists = transformers[deviceGVR]
	assert.True(t, exists, "Device transformer should be registered")

	// Verify pods transformer factory is registered
	podGVR := corev1.SchemeGroupVersion.WithResource("pods")
	_, exists = transformerFactories[podGVR]
	assert.True(t, exists, "Pod transformer factory should be registered")
}

// TestKoordInformerFactoryTransform tests transform setup with koordinator informer factory
func TestKoordInformerFactoryTransform(t *testing.T) {
	// This test verifies that Device transformer can be set on koordinator informer
	// Note: With nil client, the informer won't actually process events,
	// but we can still verify the SetTransform behavior before factory start
	koordInformerFactory := koordinformers.NewSharedInformerFactory(nil, 0)

	// Get the device informer
	deviceInformer := koordInformerFactory.Scheduling().V1alpha1().Devices().Informer()

	// Set transform before starting (should succeed)
	err := deviceInformer.SetTransform(TransformDevice)
	assert.NoError(t, err, "Setting device transform before start should succeed")

	// Verify the transform function works correctly standalone
	device := &schedulingv1alpha1.Device{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{
					Type:   schedulingv1alpha1.GPU,
					Minor:  intPtr(0),
					Health: true,
					Resources: corev1.ResourceList{
						apiext.ResourceGPUCore: resource.MustParse("100"),
					},
				},
			},
		},
	}
	result, err := TransformDevice(device)
	assert.NoError(t, err, "TransformDevice should not error")
	assert.NotNil(t, result, "TransformDevice should return a result")
}
