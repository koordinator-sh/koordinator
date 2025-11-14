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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestGetCPUNormalizationRatio(t *testing.T) {
	tests := []struct {
		name    string
		arg     *corev1.Node
		want    float64
		wantErr bool
	}{
		{
			name:    "node has no annotation",
			arg:     &corev1.Node{},
			want:    -1,
			wantErr: false,
		},
		{
			name: "node has no cpu-normalization annotation",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx": "yyy",
					},
				},
			},
			want:    -1,
			wantErr: false,
		},
		{
			name: "node has invalid cpu-normalization ratio format",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx":                           "yyy",
						AnnotationCPUNormalizationRatio: "invalidValue",
					},
				},
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "node has valid cpu-normalization ratio",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"xxx":                           "yyy",
						AnnotationCPUNormalizationRatio: "1.20",
					},
				},
			},
			want:    1.2,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetCPUNormalizationRatio(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestSetCPUNormalizationRatio(t *testing.T) {
	type args struct {
		node  *corev1.Node
		ratio float64
	}
	tests := []struct {
		name      string
		args      args
		want      bool
		wantField *corev1.Node
	}{
		{
			name: "set ratio for a node without annotation",
			args: args{
				node:  &corev1.Node{},
				ratio: 1.2,
			},
			want: true,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationCPUNormalizationRatio: "1.20",
					},
				},
			},
		},
		{
			name: "set ratio for a node has old ratio",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationCPUNormalizationRatio: "1.10",
						},
					},
				},
				ratio: 1.15,
			},
			want: true,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationCPUNormalizationRatio: "1.15",
					},
				},
			},
		},
		{
			name: "keep ratio precision as 2 for the label value",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationCPUNormalizationRatio: "1.00",
						},
					},
				},
				ratio: 1.125,
			},
			want: true,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationCPUNormalizationRatio: "1.12",
					},
				},
			},
		},
		{
			name: "keep the value unchanged",
			args: args{
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							AnnotationCPUNormalizationRatio: "1.20",
						},
					},
				},
				ratio: 1.2,
			},
			want: false,
			wantField: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationCPUNormalizationRatio: "1.20",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SetCPUNormalizationRatio(tt.args.node, tt.args.ratio)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantField, tt.args.node)
		})
	}
}

func TestGetCPUNormalizationEnabled(t *testing.T) {
	tests := []struct {
		name    string
		arg     *corev1.Node
		want    *bool
		wantErr bool
	}{
		{
			name:    "node has no label",
			arg:     &corev1.Node{},
			want:    nil,
			wantErr: false,
		},
		{
			name: "node has no label key",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"xxx": "yyy",
					},
				},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "parse label value failed",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"xxx":                        "yyy",
						LabelCPUNormalizationEnabled: "{invalidContent",
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "value is false",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelCPUNormalizationEnabled: "false",
					},
				},
			},
			want:    ptr.To[bool](false),
			wantErr: false,
		},
		{
			name: "value is true",
			arg: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelCPUNormalizationEnabled: "true",
					},
				},
			},
			want:    ptr.To[bool](true),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetCPUNormalizationEnabled(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestGetCPUBasicInfo(t *testing.T) {
	tests := []struct {
		name    string
		arg     map[string]string
		want    *CPUBasicInfo
		wantErr bool
	}{
		{
			name:    "nil annotation",
			arg:     nil,
			want:    nil,
			wantErr: false,
		},
		{
			name: "missing cpu basic info annotation",
			arg: map[string]string{
				"aaa": "bbb",
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "parse cpu basic info failed",
			arg: map[string]string{
				AnnotationCPUBasicInfo: "invalidValue",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "get cpu basic info successfully",
			arg: map[string]string{
				AnnotationCPUBasicInfo: `{
    "cpuModel": "CPU A 2.50GHz",
    "hyperThreadEnabled": true,
    "turboEnabled": false,
    "vendorID": "unknown"
}`,
			},
			want: &CPUBasicInfo{
				CPUModel:           "CPU A 2.50GHz",
				HyperThreadEnabled: true,
				TurboEnabled:       false,
				VendorID:           "unknown",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetCPUBasicInfo(tt.arg)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestSetCPUBasicInfo(t *testing.T) {
	testCPUBasicInfo := &CPUBasicInfo{
		CPUModel:           "CPU A 2.50GHz",
		HyperThreadEnabled: true,
		TurboEnabled:       false,
		VendorID:           "unknown",
	}
	testAnnotationBytes, err := json.Marshal(testCPUBasicInfo)
	assert.NoError(t, err)
	testCPUBasicInfo1 := &CPUBasicInfo{
		CPUModel:           "CPU A 2.50GHz",
		HyperThreadEnabled: true,
		TurboEnabled:       true,
		VendorID:           "unknown",
	}
	testAnnotationBytes1, err := json.Marshal(testCPUBasicInfo1)
	assert.NoError(t, err)
	type args struct {
		annotations map[string]string
		info        *CPUBasicInfo
	}
	tests := []struct {
		name      string
		args      args
		want      bool
		wantField map[string]string
	}{
		{
			name: "annotation not change",
			args: args{
				annotations: map[string]string{
					AnnotationCPUBasicInfo: string(testAnnotationBytes),
				},
				info: testCPUBasicInfo,
			},
			want: false,
			wantField: map[string]string{
				AnnotationCPUBasicInfo: string(testAnnotationBytes),
			},
		},
		{
			name: "annotation is new",
			args: args{
				annotations: map[string]string{},
				info:        testCPUBasicInfo,
			},
			want: true,
			wantField: map[string]string{
				AnnotationCPUBasicInfo: string(testAnnotationBytes),
			},
		},
		{
			name: "annotation changes",
			args: args{
				annotations: map[string]string{
					AnnotationCPUBasicInfo: string(testAnnotationBytes),
				},
				info: testCPUBasicInfo1,
			},
			want: true,
			wantField: map[string]string{
				AnnotationCPUBasicInfo: string(testAnnotationBytes1),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SetCPUBasicInfo(tt.args.annotations, tt.args.info)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantField, tt.args.annotations)
		})
	}
}

func TestCPUNormalizationRatioDifferent(t *testing.T) {
	testCases := []struct {
		old          float64
		new          float64
		expectedDiff bool
	}{
		{
			old:          1.2,
			new:          1.2,
			expectedDiff: false,
		},
		{
			old:          1.2,
			new:          1.3,
			expectedDiff: true,
		},
		{
			old:          1.2,
			new:          1.205,
			expectedDiff: false,
		},
		{
			old:          1.2,
			new:          1.195,
			expectedDiff: false,
		},
	}
	for _, tc := range testCases {
		assert.Equal(t, tc.expectedDiff, IsCPUNormalizationRatioDifferent(tc.old, tc.new))
	}
}
