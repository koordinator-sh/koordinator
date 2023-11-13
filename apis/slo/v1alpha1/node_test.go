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

package v1alpha1

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func TestGetOriginExtendAllocatable(t *testing.T) {
	type args struct {
		annotations map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    *OriginAllocatable
		wantErr bool
	}{
		{
			name: "annotation not exist",
			args: args{
				annotations: map[string]string{},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "bad annotation format",
			args: args{
				annotations: map[string]string{
					NodeOriginExtendedAllocatableAnnotationKey: "bad-format",
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "get from annotation succ",
			args: args{
				annotations: map[string]string{
					NodeOriginExtendedAllocatableAnnotationKey: "{\"resources\": {\"kubernetes.io/batch-cpu\": 1000,\"kubernetes.io/batch-memory\": 1024}}",
				},
			},
			want: &OriginAllocatable{
				Resources: map[corev1.ResourceName]resource.Quantity{
					apiext.BatchCPU:    resource.MustParse("1000"),
					apiext.BatchMemory: resource.MustParse("1024"),
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetOriginExtendedAllocatable(tt.args.annotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetOriginExtendAllocatableR() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetOriginExtendAllocatableR() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetOriginExtendedAllocatableRes(t *testing.T) {
	type args struct {
		annotations         map[string]string
		extendedAllocatable corev1.ResourceList
	}
	tests := []struct {
		name           string
		args           args
		wantAnnotation map[string]string
		wantErr        bool
	}{
		{
			name: "create new extended allocatable",
			args: args{
				annotations: map[string]string{
					"other-key": "other-val",
				},
				extendedAllocatable: corev1.ResourceList{
					apiext.BatchCPU:    resource.MustParse("1"),
					apiext.BatchMemory: resource.MustParse("1Gi"),
				},
			},
			wantAnnotation: map[string]string{
				"other-key": "other-val",
				NodeOriginExtendedAllocatableAnnotationKey: "{\"resources\":{\"kubernetes.io/batch-cpu\":\"1\",\"kubernetes.io/batch-memory\":\"1Gi\"}}",
			},
			wantErr: false,
		},
		{
			name: "update current extended allocatable",
			args: args{
				annotations: map[string]string{
					"other-key": "other-val",
					NodeOriginExtendedAllocatableAnnotationKey: "{\"resources\":{\"kubernetes.io/batch-cpu\":\"1\",\"kubernetes.io/batch-memory\":\"1Gi\"}}",
				},
				extendedAllocatable: corev1.ResourceList{
					apiext.BatchCPU:    resource.MustParse("2"),
					apiext.BatchMemory: resource.MustParse("2Gi"),
				},
			},
			wantAnnotation: map[string]string{
				"other-key": "other-val",
				NodeOriginExtendedAllocatableAnnotationKey: "{\"resources\":{\"kubernetes.io/batch-cpu\":\"2\",\"kubernetes.io/batch-memory\":\"2Gi\"}}",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SetOriginExtendedAllocatableRes(tt.args.annotations, tt.args.extendedAllocatable)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.wantAnnotation, tt.args.annotations)
		})
	}
}

func TestGetThirdPartyAllocations(t *testing.T) {
	type args struct {
		annotations map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    *ThirdPartyAllocations
		wantErr bool
	}{
		{
			name: "annotation is nil",
			args: args{
				annotations: nil,
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "annotation not exist",
			args: args{
				annotations: map[string]string{},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "bad annotation format",
			args: args{
				annotations: map[string]string{
					NodeThirdPartyAllocationsAnnotationKey: "bad-format",
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "get from annotation succ",
			args: args{
				annotations: map[string]string{
					NodeThirdPartyAllocationsAnnotationKey: "{\"allocations\":[{\"name\":\"hadoop-yarn\",\"priority\":\"koord-batch\",\"resources\":{\"kubernetes.io/batch-cpu\":\"1\",\"kubernetes.io/batch-memory\":\"1Gi\"}}]}",
				},
			},
			want: &ThirdPartyAllocations{
				Allocations: []ThirdPartyAllocation{
					{
						Name:     "hadoop-yarn",
						Priority: apiext.PriorityBatch,
						Resources: corev1.ResourceList{
							apiext.BatchCPU:    resource.MustParse("1"),
							apiext.BatchMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetThirdPartyAllocations(tt.args.annotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetThirdPartyAllocations() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetThirdPartyAllocations() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetThirdPartyAllocation(t *testing.T) {
	type args struct {
		annotations map[string]string
		name        string
		priority    apiext.PriorityClass
		resource    corev1.ResourceList
	}
	tests := []struct {
		name           string
		args           args
		wantAnnotation map[string]string
		wantErr        bool
	}{
		{
			name: "create new extended allocatable",
			args: args{
				annotations: map[string]string{
					"other-key": "other-val",
				},
				name:     "hadoop-yarn",
				priority: apiext.PriorityBatch,
				resource: corev1.ResourceList{
					apiext.BatchCPU:    resource.MustParse("1"),
					apiext.BatchMemory: resource.MustParse("1Gi"),
				},
			},
			wantAnnotation: map[string]string{
				"other-key":                            "other-val",
				NodeThirdPartyAllocationsAnnotationKey: "{\"allocations\":[{\"name\":\"hadoop-yarn\",\"priority\":\"koord-batch\",\"resources\":{\"kubernetes.io/batch-cpu\":\"1\",\"kubernetes.io/batch-memory\":\"1Gi\"}}]}",
			},
			wantErr: false,
		},
		{
			name: "update current extended allocatable",
			args: args{
				annotations: map[string]string{
					"other-key":                            "other-val",
					NodeThirdPartyAllocationsAnnotationKey: "{\"allocations\":[{\"name\":\"hadoop-yarn\",\"priority\":\"koord-batch\",\"resources\":{\"kubernetes.io/batch-cpu\":\"1\",\"kubernetes.io/batch-memory\":\"1Gi\"}}]}",
				},
				name:     "hadoop-yarn",
				priority: apiext.PriorityBatch,
				resource: corev1.ResourceList{
					apiext.BatchCPU:    resource.MustParse("2"),
					apiext.BatchMemory: resource.MustParse("2Gi"),
				},
			},
			wantAnnotation: map[string]string{
				"other-key":                            "other-val",
				NodeThirdPartyAllocationsAnnotationKey: "{\"allocations\":[{\"name\":\"hadoop-yarn\",\"priority\":\"koord-batch\",\"resources\":{\"kubernetes.io/batch-cpu\":\"2\",\"kubernetes.io/batch-memory\":\"2Gi\"}}]}",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SetThirdPartyAllocation(tt.args.annotations, tt.args.name, tt.args.priority, tt.args.resource)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.wantAnnotation, tt.args.annotations)
		})
	}
}
