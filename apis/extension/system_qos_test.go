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

package extension

import (
	"reflect"
	"testing"

	"k8s.io/utils/pointer"
)

func TestGetSystemQOSResource(t *testing.T) {
	type args struct {
		anno map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    *SystemQOSResource
		wantErr bool
	}{
		{
			name: "nil annotation",
			args: args{
				anno: nil,
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "annotation key not exist",
			args: args{
				anno: map[string]string{
					"not-exist-key": "not-exist_val",
				},
			},
			want:    &SystemQOSResource{},
			wantErr: false,
		},
		{
			name: "bad json format",
			args: args{
				anno: map[string]string{
					AnnotationNodeSystemQOSResource: "bad-format-str",
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "parse format succeed",
			args: args{
				anno: map[string]string{
					AnnotationNodeSystemQOSResource: `{"cpuset":"0-1", "cpusetExclusive": true}`,
				},
			},
			want: &SystemQOSResource{
				CPUSet:          "0-1",
				CPUSetExclusive: pointer.Bool(true),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetSystemQOSResource(tt.args.anno)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetSystemQOSResource() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetSystemQOSResource() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSystemQOSResource_IsCPUSetExclusive(t *testing.T) {
	type fields struct {
		CPUSet          string
		CPUSetExclusive *bool
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "exclusive is nil",
			fields: fields{
				CPUSet:          "0-1",
				CPUSetExclusive: nil,
			},
			want: true,
		},
		{
			name: "exclusive is true",
			fields: fields{
				CPUSet:          "0-1",
				CPUSetExclusive: pointer.Bool(true),
			},
			want: true,
		},
		{
			name: "exclusive is false",
			fields: fields{
				CPUSet:          "0-1",
				CPUSetExclusive: pointer.Bool(false),
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &SystemQOSResource{
				CPUSet:          tt.fields.CPUSet,
				CPUSetExclusive: tt.fields.CPUSetExclusive,
			}
			if got := r.IsCPUSetExclusive(); got != tt.want {
				t.Errorf("IsCPUSetExclusive() = %v, want %v", got, tt.want)
			}
		})
	}
}
