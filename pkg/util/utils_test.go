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

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/pointer"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
)

func Test_MergeCfg(t *testing.T) {
	type TestingStruct struct {
		A *int64 `json:"a,omitempty"`
		B *int64 `json:"b,omitempty"`
	}
	type args struct {
		old interface{}
		new interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    interface{}
		wantErr bool
	}{
		{
			name: "throw an error if the inputs' types are not the same",
			args: args{
				old: &TestingStruct{},
				new: pointer.Int64(1),
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "throw an error if any of the inputs is not a pointer",
			args: args{
				old: TestingStruct{},
				new: TestingStruct{},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "throw an error if any of inputs is nil",
			args:    args{},
			want:    nil,
			wantErr: true,
		},
		{
			name: "throw an error if any of inputs is nil 1",
			args: args{
				old: &TestingStruct{},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "new is empty",
			args: args{
				old: &TestingStruct{
					A: pointer.Int64(0),
					B: pointer.Int64(1),
				},
				new: &TestingStruct{},
			},
			want: &TestingStruct{
				A: pointer.Int64(0),
				B: pointer.Int64(1),
			},
		},
		{
			name: "old is empty",
			args: args{
				old: &TestingStruct{},
				new: &TestingStruct{
					B: pointer.Int64(1),
				},
			},
			want: &TestingStruct{
				B: pointer.Int64(1),
			},
		},
		{
			name: "both are empty",
			args: args{
				old: &TestingStruct{},
				new: &TestingStruct{},
			},
			want: &TestingStruct{},
		},
		{
			name: "new one overwrites the old one",
			args: args{
				old: &TestingStruct{
					A: pointer.Int64(0),
					B: pointer.Int64(1),
				},
				new: &TestingStruct{
					B: pointer.Int64(2),
				},
			},
			want: &TestingStruct{
				A: pointer.Int64(0),
				B: pointer.Int64(2),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := MergeCfg(tt.args.old, tt.args.new)
			assert.Equal(t, tt.wantErr, gotErr != nil)
			if !tt.wantErr {
				assert.Equal(t, tt.want, got.(*TestingStruct))
			}
		})
	}
}

func TestMinInt64(t *testing.T) {
	type args struct {
		i int64
		j int64
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		{
			name: "i < j",
			args: args{
				i: 0,
				j: 1,
			},
			want: 0,
		},
		{
			name: "i > j",
			args: args{
				i: 1,
				j: 0,
			},
			want: 0,
		},
		{
			name: "i = j",
			args: args{
				i: 0,
				j: 0,
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MinInt64(tt.args.i, tt.args.j); got != tt.want {
				t.Errorf("MinInt64() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaxInt64(t *testing.T) {
	type args struct {
		i int64
		j int64
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		{
			name: "i < j",
			args: args{
				i: 0,
				j: 1,
			},
			want: 1,
		},
		{
			name: "i > j",
			args: args{
				i: 1,
				j: 0,
			},
			want: 1,
		},
		{
			name: "i = j",
			args: args{
				i: 0,
				j: 0,
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaxInt64(tt.args.i, tt.args.j); got != tt.want {
				t.Errorf("MaxInt64() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_GeneratePodPatch(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container-1"},
				{Name: "test-container-2"},
			},
		},
	}
	patchAnnotation := map[string]string{"test_case": "Test_GeneratePodPatch"}
	pod2 := pod1.DeepCopy()
	pod2.SetAnnotations(patchAnnotation)
	patchBytes, err := GeneratePodPatch(pod1, pod2)
	if err != nil {
		t.Errorf("error creating patch bytes %v", err)
	}
	var patchMap map[string]interface{}
	err = json.Unmarshal(patchBytes, &patchMap)
	if err != nil {
		t.Errorf("error unmarshalling json patch : %v", err)
	}
	metadata, ok := patchMap["metadata"].(map[string]interface{})
	if !ok {
		t.Errorf("error converting metadata to version map")
	}
	annotation, _ := metadata["annotations"].(map[string]interface{})
	if fmt.Sprint(annotation) != fmt.Sprint(patchAnnotation) {
		t.Errorf("expect patchBytes: %q, got: %q", patchAnnotation, annotation)
	}
}

type fakeClientSetHandle struct {
	client      *kubefake.Clientset
	koordClient *koordfake.Clientset
}

func (f *fakeClientSetHandle) ClientSet() clientset.Interface {
	return f.client
}

func (f *fakeClientSetHandle) KoordinatorClientSet() koordinatorclientset.Interface {
	return f.koordClient
}

func TestPatch_PatchPodOrReservation(t *testing.T) {
	resources := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	addedResources := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
		apiext.ResourceGPU:    resource.MustParse("100"),
	}
	testNormalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: resources,
						Limits:   resources,
					},
				},
			},
		},
	}
	testR := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation-0",
			UID:  "123456",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod-1",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: resources,
								Limits:   resources,
							},
						},
					},
				},
			},
		},
	}
	tests := []struct {
		name           string
		pod            *corev1.Pod
		newPod         *corev1.Pod
		reservation    *schedulingv1alpha1.Reservation
		newReservation *schedulingv1alpha1.Reservation
		wantErr        bool
	}{
		{
			name:    "nothing to patch for normal pod",
			pod:     testNormalPod,
			newPod:  testNormalPod,
			wantErr: false,
		},
		{
			name: "patch successfully for normal pod",
			pod:  testNormalPod,
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod-1",
					Annotations: map[string]string{
						"test": "123",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Resources: corev1.ResourceRequirements{
								Requests: addedResources,
								Limits:   addedResources,
							},
							Env: []corev1.EnvVar{
								{
									Name:  "test-env",
									Value: "123",
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name:           "nothing to patch for reservation",
			reservation:    testR,
			newReservation: testR,
			wantErr:        false,
		},
		{
			name:        "patch successfully for reservation",
			reservation: testR,
			newReservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation-0",
					UID:  "123456",
					Annotations: map[string]string{
						"test": "123",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod-1",
							Annotations: map[string]string{
								"test": "456",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Resources: corev1.ResourceRequirements{
										Requests: addedResources,
										Limits:   addedResources,
									},
									Env: []corev1.EnvVar{
										{
											Name:  "test-env",
											Value: "123",
										},
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle := &fakeClientSetHandle{
				client:      kubefake.NewSimpleClientset(),
				koordClient: koordfake.NewSimpleClientset(),
			}
			if tt.pod != nil {
				_, err := handle.client.CoreV1().Pods(tt.pod.Namespace).Create(context.TODO(), tt.pod, metav1.CreateOptions{})
				assert.NoError(t, err)
				patched, gotErr := NewPatch().WithHandle(handle).Patch(context.TODO(), tt.pod, tt.newPod)
				assert.Equal(t, tt.wantErr, gotErr != nil)
				assert.Equal(t, tt.newPod, patched)
			}
			if tt.reservation != nil {
				_, err := handle.koordClient.SchedulingV1alpha1().Reservations().Create(context.TODO(), tt.reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
				patched, gotErr := NewPatch().WithHandle(handle).Patch(context.TODO(), tt.reservation, tt.newReservation)
				assert.Equal(t, tt.wantErr, gotErr != nil)
				assert.Equal(t, tt.newReservation, patched)
			}
		})
	}
}
