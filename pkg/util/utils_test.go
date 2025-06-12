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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
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
