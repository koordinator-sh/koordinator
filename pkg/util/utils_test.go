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
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
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
				new: ptr.To[int64](1),
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
					A: ptr.To[int64](0),
					B: ptr.To[int64](1),
				},
				new: &TestingStruct{},
			},
			want: &TestingStruct{
				A: ptr.To[int64](0),
				B: ptr.To[int64](1),
			},
		},
		{
			name: "old is empty",
			args: args{
				old: &TestingStruct{},
				new: &TestingStruct{
					B: ptr.To[int64](1),
				},
			},
			want: &TestingStruct{
				B: ptr.To[int64](1),
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
					A: ptr.To[int64](0),
					B: ptr.To[int64](1),
				},
				new: &TestingStruct{
					B: ptr.To[int64](2),
				},
			},
			want: &TestingStruct{
				A: ptr.To[int64](0),
				B: ptr.To[int64](2),
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
			UID:       "xxx",
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

func Test_GeneratePodPatchWithUID(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-pod-1",
			UID:       "xxx",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container-1"},
				{Name: "test-container-2"},
			},
		},
	}
	patchAnnotation := map[string]string{"test_case": "Test_GeneratePodPatchWithUID"}
	pod2 := pod1.DeepCopy()
	pod2.SetAnnotations(patchAnnotation)
	patchBytes, err := GeneratePodPatchWithUID(pod1, pod2)
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
	uid, ok := metadata["uid"]
	if !ok {
		t.Errorf("expect metadata.uid to be not nil")
	}
	if fmt.Sprint(uid) != string(pod1.UID) {
		t.Errorf("metadata.uid got %s, expect %s", uid, pod1.UID)
	}
	annotation, _ := metadata["annotations"].(map[string]interface{})
	if fmt.Sprint(annotation) != fmt.Sprint(patchAnnotation) {
		t.Errorf("expect patchBytes: %q, got: %q", patchAnnotation, annotation)
	}
}

func Test_GenerateReservationPatch(t *testing.T) {
	r1 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation-1",
			UID:  "xxx",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test-container-1"},
						{Name: "test-container-2"},
					},
				},
			},
		},
	}
	patchAnnotation := map[string]string{"test_case": "Test_GenerateReservationPatch"}
	r2 := r1.DeepCopy()
	r2.SetAnnotations(patchAnnotation)
	patchBytes, err := GenerateReservationPatch(r1, r2)
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

func Test_GenerateReservationPatchWithUID(t *testing.T) {
	r1 := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-reservation-1",
			UID:  "xxx",
		},
		Spec: schedulingv1alpha1.ReservationSpec{
			Template: &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test-container-1"},
						{Name: "test-container-2"},
					},
				},
			},
		},
	}
	patchAnnotation := map[string]string{"test_case": "Test_GenerateReservationPatch"}
	r2 := r1.DeepCopy()
	r2.SetAnnotations(patchAnnotation)
	patchBytes, err := GenerateReservationPatchWithUID(r1, r2)
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
	uid, ok := metadata["uid"]
	if !ok {
		t.Errorf("expect metadata.uid to be not nil")
	}
	if fmt.Sprint(uid) != string(r1.UID) {
		t.Errorf("metadata.uid got %s, expect %s", uid, r1.UID)
	}
	annotation, _ := metadata["annotations"].(map[string]interface{})
	if fmt.Sprint(annotation) != fmt.Sprint(patchAnnotation) {
		t.Errorf("expect patchBytes: %q, got: %q", patchAnnotation, annotation)
	}
}

func TestPatchPod(t *testing.T) {
	tests := []struct {
		name         string
		originalObj  metav1.Object
		modifiedObj  metav1.Object
		subResources []string
		wantErr      bool
	}{
		{
			name: "patch pod",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			modifiedObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
								{
									Name:  "appendEnv",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "skipped to patch pod",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "xxxxxx",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			modifiedObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "xxxxxx",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "patch pod status",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{},
			},
			modifiedObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
								{
									Name:  "appendEnv",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					NominatedNodeName: "test-node",
				},
			},
			subResources: []string{"status"},
		},
		{
			name: "patch reservation",
			originalObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
									},
								},
							},
						},
					},
				},
			},
			modifiedObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
										{
											Name:  "appendEnv",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
											apiext.ResourceGPU: resource.MustParse("100"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
											apiext.ResourceGPU: resource.MustParse("100"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "skipped to patch reservation",
			originalObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
					UID:  "yyyyyy",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
									},
								},
							},
						},
					},
				},
			},
			modifiedObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
					UID:  "yyyyyy",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientSet := kubefake.NewSimpleClientset()
			koordClientSet := koordfake.NewSimpleClientset()

			if pod, ok := tt.originalObj.(*corev1.Pod); ok {
				_, err := clientSet.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			} else if reservation, ok := tt.originalObj.(*schedulingv1alpha1.Reservation); ok {
				_, err := koordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			if pod, ok := tt.originalObj.(*corev1.Pod); ok {
				original, modified := tt.originalObj.(*corev1.Pod), tt.modifiedObj.(*corev1.Pod)
				_, err := PatchPod(context.TODO(), clientSet, original, modified, tt.subResources...)
				assert.Equal(t, tt.wantErr, err != nil, err)

				got, err := clientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
				assert.NoError(t, err)
				assert.Equal(t, modified, got)
			} else if reservation, ok := tt.originalObj.(*schedulingv1alpha1.Reservation); ok {
				original, modified := tt.originalObj.(*schedulingv1alpha1.Reservation), tt.modifiedObj.(*schedulingv1alpha1.Reservation)
				_, err := PatchReservation(context.TODO(), koordClientSet, original, modified)
				assert.Equal(t, tt.wantErr, err != nil, err)

				got, err := koordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				assert.NoError(t, err)
				assert.Equal(t, tt.modifiedObj.(*schedulingv1alpha1.Reservation), got)
			}
		})
	}
}

func TestPatchPodSafe(t *testing.T) {
	tests := []struct {
		name         string
		originalObj  metav1.Object
		modifiedObj  metav1.Object
		subResources []string
		wantErr      bool
	}{
		{
			name: "patch pod",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			modifiedObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
					UID: "yyyyyy",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
								{
									Name:  "appendEnv",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "skipped to patch pod",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "xxxxxx",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			modifiedObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       "xxxxxx",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
		},
		{
			name: "patch pod status",
			originalObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
								},
							},
						},
					},
				},
			},
			modifiedObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
					UID: "yyyyyy",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "main",
							Env: []corev1.EnvVar{
								{
									Name:  "test",
									Value: "true",
								},
								{
									Name:  "appendEnv",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("4"),
									apiext.ResourceGPU: resource.MustParse("100"),
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					NominatedNodeName: "test-node",
				},
			},
		},
		{
			name: "patch reservation",
			originalObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
									},
								},
							},
						},
					},
				},
			},
			modifiedObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
					UID: "yyyyyy",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
										{
											Name:  "appendEnv",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
											apiext.ResourceGPU: resource.MustParse("100"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
											apiext.ResourceGPU: resource.MustParse("100"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "skipped to patch reservation",
			originalObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
					UID:  "yyyyyy",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
									},
								},
							},
						},
					},
				},
			},
			modifiedObj: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-reservation",
					UID:  "yyyyyy",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "main",
									Env: []corev1.EnvVar{
										{
											Name:  "test",
											Value: "true",
										},
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("4"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientSet := kubefake.NewSimpleClientset()
			koordClientSet := koordfake.NewSimpleClientset()

			if pod, ok := tt.originalObj.(*corev1.Pod); ok {
				_, err := clientSet.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			} else if reservation, ok := tt.originalObj.(*schedulingv1alpha1.Reservation); ok {
				_, err := koordClientSet.SchedulingV1alpha1().Reservations().Create(context.TODO(), reservation, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			if pod, ok := tt.originalObj.(*corev1.Pod); ok {
				original, modified := tt.originalObj.(*corev1.Pod), tt.modifiedObj.(*corev1.Pod)
				_, err := PatchPodSafe(context.TODO(), clientSet, original, modified, tt.subResources...)
				assert.Equal(t, tt.wantErr, err != nil, err)

				got, err := clientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
				assert.NoError(t, err)
				assert.Equal(t, modified, got)
			} else if reservation, ok := tt.originalObj.(*schedulingv1alpha1.Reservation); ok {
				original, modified := tt.originalObj.(*schedulingv1alpha1.Reservation), tt.modifiedObj.(*schedulingv1alpha1.Reservation)
				_, err := PatchReservationSafe(context.TODO(), koordClientSet, original, modified)
				assert.Equal(t, tt.wantErr, err != nil, err)

				got, err := koordClientSet.SchedulingV1alpha1().Reservations().Get(context.TODO(), reservation.Name, metav1.GetOptions{})
				assert.NoError(t, err)
				assert.Equal(t, tt.modifiedObj.(*schedulingv1alpha1.Reservation), got)
			}
		})
	}
}

func Test_GenerateNodePatch(t *testing.T) {
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
			UID:  "xxx",
		},
	}
	patchAnnotation := map[string]string{"test_case": "Test_GenerateNodePatch"}
	node2 := node1.DeepCopy()
	node2.SetAnnotations(patchAnnotation)
	patchBytes, err := GenerateNodePatch(node1, node2)
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

func TestPatchNode(t *testing.T) {
	tests := []struct {
		name         string
		originalNode *corev1.Node
		modifiedNode *corev1.Node
		subResources []string
		wantErr      bool
	}{
		{
			name: "patch node",
			originalNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
			},
			modifiedNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
			},
		},
		{
			name: "skipped to patch node",
			originalNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					UID:  "xxxxxx",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
			},
			modifiedNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					UID:  "xxxxxx",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
			},
		},
		{
			name: "patch node status",
			originalNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("4"),
					},
				},
			},
			modifiedNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
					Annotations: map[string]string{
						"testAnnotation": "1",
					},
					Labels: map[string]string{
						"testLabel": "2",
					},
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("8")},
				},
			},
			subResources: []string{"status"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientSet := kubefake.NewSimpleClientset()

			_, err := clientSet.CoreV1().Nodes().Create(context.TODO(), tt.originalNode, metav1.CreateOptions{})
			assert.NoError(t, err)

			original, modified := tt.originalNode, tt.modifiedNode
			_, err = PatchNode(context.TODO(), clientSet, original, modified, tt.subResources...)
			assert.Equal(t, tt.wantErr, err != nil, err)

			got, err := clientSet.CoreV1().Nodes().Get(context.TODO(), tt.originalNode.Name, metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Equal(t, modified, got)
		})
	}
}

func TestMinFloat64(t *testing.T) {
	big := 2.0
	small := 1.0
	gotMin := MinFloat64(big, small)
	assert.Equal(t, small, gotMin)
	gotMax := MaxFloat64(big, small)
	assert.Equal(t, big, gotMax)
}

func TestOnceValues(t *testing.T) {
	calls := []int{0}
	f := OnceValues(func() ([]int, error) {
		calls[0]++
		return calls, nil
	})
	allocs := testing.AllocsPerRun(10, func() { f() })
	v1, v2 := f()
	if calls[0] != 1 {
		t.Errorf("want calls==1, got %d", calls)
	}
	if v1[0] != 1 || v2 != nil {
		t.Errorf("want v1[0]==1 and v2==nil, got %d and %d", v1, v2)
	}
	if allocs != 0 {
		t.Errorf("want 0 allocations per call, got %v", allocs)
	}
}

func Test_isErrorConnectionClose(t *testing.T) {
	type args struct {
		err error
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "http2: client connection force closed via ClientConn.Close",
			args: args{
				err: fmt.Errorf("http2: client connection force closed via ClientConn.Close"),
			},
			want: true,
		},
		{
			name: "http2: server sent GOAWAY and closed the connection; LastStreamID=1113, ErrCode=NO_ERROR, debug=\"\"",
			args: args{
				err: fmt.Errorf("http2: server sent GOAWAY and closed the connection; LastStreamID=1113, ErrCode=NO_ERROR, debug=\"\""),
			},
			want: true,
		},
		{
			name: "other error",
			args: args{
				err: fmt.Errorf("other error"),
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, isErrorConnectionClosed(tt.args.err), "isErrorConnectionClosed(%v)", tt.args.err)
		})
	}
}
