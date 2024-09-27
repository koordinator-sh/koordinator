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

package resourceamplification

import (
	"context"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestNodeResourceAmplificationPlugin_handleNormalization(t *testing.T) {
	testCases := []struct {
		name                        string
		annotations                 map[string]string
		resourceName                corev1.ResourceName
		alloatableValue             int64
		expectedErr                 bool
		expectedAllocatableValue    int64
		expectedOriginalAllocatable int64
	}{

		{
			name:                        "normal",
			annotations:                 map[string]string{extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 1.5}`},
			resourceName:                corev1.ResourceCPU,
			alloatableValue:             1000,
			expectedErr:                 false,
			expectedAllocatableValue:    1500,
			expectedOriginalAllocatable: 1000,
		},
		{
			name:                        "disable1",
			annotations:                 map[string]string{extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 0.5}`},
			resourceName:                corev1.ResourceCPU,
			alloatableValue:             1000,
			expectedErr:                 false,
			expectedAllocatableValue:    1000,
			expectedOriginalAllocatable: 1000,
		},
		{
			name:                        "disable2",
			annotations:                 map[string]string{extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 1}`},
			resourceName:                corev1.ResourceCPU,
			alloatableValue:             1000,
			expectedErr:                 false,
			expectedAllocatableValue:    1000,
			expectedOriginalAllocatable: 1000,
		},
		{
			name: "disable amplication will remove original allocatable",
			annotations: map[string]string{
				extension.AnnotationNodeRawAllocatable: "{}",
			},
			resourceName:                corev1.ResourceCPU,
			alloatableValue:             1000,
			expectedErr:                 false,
			expectedAllocatableValue:    1000,
			expectedOriginalAllocatable: -1,
		},
		{
			name:                        "unsupported ratio will be ignored",
			annotations:                 map[string]string{extension.AnnotationNodeResourceAmplificationRatio: `{"eni": 2}`},
			resourceName:                corev1.ResourceName("eni"),
			alloatableValue:             1000,
			expectedErr:                 false,
			expectedAllocatableValue:    1000,
			expectedOriginalAllocatable: 1000,
		},
		{
			name: "get original resource from annotation and amplify",
			annotations: map[string]string{extension.AnnotationNodeResourceAmplificationRatio: `{"cpu": 2}`,
				extension.AnnotationNodeRawAllocatable: `{"cpu":"1"}`},
			resourceName:                corev1.ResourceCPU,
			alloatableValue:             1000,
			expectedErr:                 false,
			expectedAllocatableValue:    2000,
			expectedOriginalAllocatable: 1000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := &NodeResourceAmplificationPlugin{}

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-node",
					Annotations: tc.annotations,
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: *resource.NewMilliQuantity(tc.alloatableValue, resource.DecimalSI),
					},
				},
			}
			err := plugin.handleUpdate(nil, node)

			// Check the result
			if tc.expectedErr {
				if err == nil {
					t.Errorf("Expected error, but got err: nil")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}

				cpu := node.Status.Allocatable[corev1.ResourceCPU]
				// Check the Allocatable value
				allocatableValue := cpu.MilliValue()
				if allocatableValue != tc.expectedAllocatableValue {
					t.Errorf("Expected Allocatable value: %d, got: %d", tc.expectedAllocatableValue, allocatableValue)
				}
				original, err := extension.GetNodeRawAllocatable(node.Annotations)
				if tc.expectedOriginalAllocatable > 0 {
					if err != nil {
						t.Errorf("Expected no error, got: %v", err)
					}
					if original.Cpu().MilliValue() != tc.expectedOriginalAllocatable {
						t.Errorf("Expected original Allocatable value: %d, got: %d", tc.expectedOriginalAllocatable, original.Cpu().MilliValue())
					}
				} else {
					_, ok := node.Annotations[extension.AnnotationNodeRawAllocatable]
					if ok {
						t.Errorf("unexpected raw allocatable")
					}
				}
			}
		})
	}
}

func TestNodeResourceAmplificationPlugin_Admit(t *testing.T) {
	testCases := []struct {
		name             string
		admissionRequest *admissionv1.AdmissionRequest
		node             *corev1.Node
		oldNode          *corev1.Node
		expectedErr      bool
	}{
		{
			name: "CreateOperation",
			admissionRequest: &admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
			},
			node:        &corev1.Node{},
			oldNode:     &corev1.Node{},
			expectedErr: false,
		},
		{
			name: "UpdateOperation",
			admissionRequest: &admissionv1.AdmissionRequest{
				Operation: admissionv1.Update,
			},
			node:        &corev1.Node{},
			oldNode:     &corev1.Node{},
			expectedErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := NewPlugin()

			request := admission.Request{
				AdmissionRequest: *tc.admissionRequest,
			}

			err := plugin.Admit(context.TODO(), request, tc.node, tc.oldNode)

			if tc.expectedErr && err == nil {
				t.Errorf("Expected an error, but got nil")
			} else if !tc.expectedErr && err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
		})
	}
}

func TestNodeResourceAmplificationPlugin_Validate(t *testing.T) {
	plugin := NewPlugin()
	request := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
		},
	}

	err := plugin.Admit(context.TODO(), request, &corev1.Node{}, &corev1.Node{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNodeResourceAmplificationPlugin_isSupportedResourceChanged(t *testing.T) {
	testCases := []struct {
		name     string
		oldNode  *corev1.Node
		newNode  *corev1.Node
		expected bool
	}{
		{
			name:     "oldNode is nil",
			oldNode:  nil,
			newNode:  &corev1.Node{},
			expected: false,
		},
		{
			name:     "newNode is nil",
			oldNode:  &corev1.Node{},
			newNode:  nil,
			expected: false,
		},
		{
			name:     "oldNode & newNode is nil",
			oldNode:  nil,
			newNode:  nil,
			expected: false,
		},
		{
			name: "resource is unchanged",
			oldNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			newNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expected: false,
		},
		{
			name: "add resource",
			oldNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
				},
			},
			newNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expected: true,
		},
		{
			name: "remove resource",
			oldNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			newNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
				},
			},
			expected: true,
		},
		{
			name: "remove updated",
			oldNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			newNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("5"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
			},
			expected: true,
		},
		{
			name: "unsupported resource remove",
			oldNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("4"),
						corev1.ResourceEphemeralStorage: resource.MustParse("80"),
					},
				},
			},
			newNode: &corev1.Node{
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("4"),
						corev1.ResourceEphemeralStorage: resource.MustParse("100"),
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := &NodeResourceAmplificationPlugin{}

			if actual := plugin.isSupportedResourceChanged(tc.oldNode, tc.newNode); actual != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, actual)
			}
		})
	}
}
