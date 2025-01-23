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

package core

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apiserver/pkg/quota/v1"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

// TestNewMockLimiter tests the NewMockLimiter function.
func TestNewMockLimiter(t *testing.T) {
	tests := []struct {
		name        string
		args        string
		expectedErr error
	}{
		{
			name:        "empty args",
			args:        "",
			expectedErr: fmt.Errorf("args is required but not found"),
		},
		{
			name:        "invalid JSON",
			args:        "{invalid json}",
			expectedErr: fmt.Errorf("failed to unmarshal args for custom limiter factory mock, err=invalid character 'i' looking for beginning of object key string"),
		},
		{
			name:        "missing labelSelector",
			args:        "{}",
			expectedErr: fmt.Errorf("labelSelector is required but not found"),
		},
		{
			name:        "valid args",
			args:        `{"labelSelector": "app=mock"}`,
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter, err := NewMockLimiter("mock", tt.args)
			if tt.expectedErr != nil {
				if err == nil || err.Error() != tt.expectedErr.Error() {
					t.Errorf("NewMockLimiter(%q) error = %v, want %v", tt.args, err, tt.expectedErr)
				}
			} else {
				if err != nil {
					t.Errorf("NewMockLimiter(%q) unexpected error: %v", tt.args, err)
				}
				if limiter == nil {
					t.Errorf("NewMockLimiter(%q) returned nil limiter", tt.args)
				}
			}
		})
	}
}

// TestMockCustomLimiter_GetLimitConf tests the GetLimitConf method of MockCustomLimiter.
func TestMockCustomLimiter_GetLimitConf(t *testing.T) {
	limiter, err := NewMockLimiter("mock", `{"labelSelector": "app=test"}`)
	if err != nil {
		t.Fatalf("Failed to create MockCustomLimiter: %v", err)
	}

	tests := []struct {
		name         string
		quota        *v1alpha1.ElasticQuota
		expectedConf *CustomLimitConf
		expectedErr  error
	}{
		{
			name: "no annotations",
			quota: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-quota",
				},
			},
			expectedConf: nil,
			expectedErr:  nil,
		},
		{
			name: "invalid limit annotation",
			quota: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-quota",
					Annotations: map[string]string{
						"custom-mock-limit-conf": "{invalid json}",
					},
				},
			},
			expectedConf: nil,
			expectedErr:  fmt.Errorf("failed to unmarshal custom limit for quota test-quota, key=mock, err=invalid character"),
		},
		{
			name: "valid limit annotation",
			quota: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-quota",
					Annotations: map[string]string{
						"custom-mock-limit-conf": `{"cpu": "100m", "memory": "200Mi"}`,
					},
				},
			},
			expectedConf: &CustomLimitConf{
				Limit: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedErr: nil,
		},
		{
			name: "valid limit and args annotations",
			quota: &v1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-quota",
					Annotations: map[string]string{
						"custom-mock-limit-conf": `{"cpu": "100m", "memory": "200Mi"}`,
						"custom-mock-args":       `{"debugEnabled": true}`,
					},
				},
			},
			expectedConf: &CustomLimitConf{
				Limit: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				Args: &MockCustomArgs{
					DebugEnabled: true,
				},
			},
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf, err := limiter.GetLimitConf(tt.quota)
			if tt.expectedErr != nil {
				if err == nil || !strings.Contains(err.Error(), tt.expectedErr.Error()) {
					t.Fatalf("GetLimitConf() unexpected error\nexpected: %v\n  actual: %v", tt.expectedErr, err)
				}
			}
			if tt.expectedErr == nil && err != nil {
				t.Fatalf("GetLimitConf() unexpected error: %v", err)
			}
			if conf == nil && tt.expectedConf == nil {
				return
			}
			if conf == nil || tt.expectedConf == nil {
				t.Fatalf("GetLimitConf() returned unexpected conf\nexpected: %v\n  actual: %v",
					tt.expectedConf, conf)
			}
			if !v1.Equals(conf.Limit, tt.expectedConf.Limit) {
				t.Errorf("GetLimitConf() returned unexpected limit\nexpected: %v\n  actual: %v",
					conf.Limit, tt.expectedConf.Limit)
			}
			if !customArgsDeepEqual(conf.Args, tt.expectedConf.Args) {
				t.Errorf("GetLimitConf() returned unexpected args\nexpected: %v  actual: %v",
					tt.expectedConf.Args, conf.Args)
			}
		})
	}
}

// TestMockCustomLimiter_CalculatePodUsedDelta tests the CalculatePodUsedDelta method of MockCustomLimiter.
func TestMockCustomLimiter_CalculatePodUsedDelta(t *testing.T) {
	tests := []struct {
		name          string
		quotaInfo     *QuotaInfo
		newPod        *corev1.Pod
		oldPod        *corev1.Pod
		expectedDelta corev1.ResourceList
	}{
		{
			name: "no custom limit",
			quotaInfo: &QuotaInfo{
				CalculateInfo: QuotaCalculateInfo{
					CustomLimits: map[string]*CustomLimitConf{},
				},
			},
			newPod:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "mock"}}},
			oldPod:        nil,
			expectedDelta: nil,
		},
		{
			name: "no matching pod",
			quotaInfo: &QuotaInfo{
				CalculateInfo: QuotaCalculateInfo{
					CustomLimits: map[string]*CustomLimitConf{
						"mock": {
							Limit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
					},
				},
			},
			newPod:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "other"}}},
			oldPod:        nil,
			expectedDelta: nil,
		},
		{
			name: "matching pod with resource request",
			quotaInfo: &QuotaInfo{
				CalculateInfo: QuotaCalculateInfo{
					CustomLimits: map[string]*CustomLimitConf{
						"mock": {
							Limit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
					},
				},
			},
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "mock"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("100Mi"),
								},
							},
						},
					},
				},
			},
			oldPod: nil,
			expectedDelta: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			},
		},
		{
			name: "pod update with resource request",
			quotaInfo: &QuotaInfo{
				CalculateInfo: QuotaCalculateInfo{
					CustomLimits: map[string]*CustomLimitConf{
						"mock": {
							Limit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
					},
				},
			},
			newPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "mock"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("70m"),
									corev1.ResourceMemory: resource.MustParse("150Mi"),
								},
							},
						},
					},
				},
			},
			oldPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "mock"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("100Mi"),
								},
							},
						},
					},
				},
			},
			expectedDelta: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("20m"),
				corev1.ResourceMemory: resource.MustParse("50Mi"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter, err := NewMockLimiter("mock", `{"labelSelector": "app=mock"}`)
			if err != nil {
				t.Fatalf("Failed to create MockCustomLimiter: %v", err)
			}

			delta := limiter.CalculatePodUsedDelta(tt.quotaInfo, tt.newPod, tt.oldPod, NewCustomLimiterState())
			if !v1.Equals(delta, tt.expectedDelta) {
				t.Errorf("CalculatePodUsedDelta() delta = %v, want %v", delta, tt.expectedDelta)
			}
		})
	}
}

// TestMockCustomLimiter_Check tests the Check method of MockCustomLimiter.
func TestMockCustomLimiter_Check(t *testing.T) {
	limiter, err := NewMockLimiter("mock", `{"labelSelector": "app=test"}`)
	if err != nil {
		t.Fatalf("Failed to create MockCustomLimiter: %v", err)
	}
	matchedLabels := map[string]string{"app": "test"}
	tests := []struct {
		name          string
		quotaInfo     *QuotaInfo
		requestDelta  corev1.ResourceList
		state         *CustomLimiterState
		expectedError error
	}{
		{
			name: "no custom limit",
			quotaInfo: &QuotaInfo{
				CalculateInfo: QuotaCalculateInfo{
					CustomLimits: map[string]*CustomLimitConf{},
				},
			},
			requestDelta: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			},
			state: &CustomLimiterState{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Labels: matchedLabels},
				},
			},
			expectedError: nil,
		},
		{
			name: "no matching pod exceeding limit",
			quotaInfo: &QuotaInfo{
				CalculateInfo: QuotaCalculateInfo{
					CustomLimits: map[string]*CustomLimitConf{
						"mock": {
							Limit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
					},
				},
			},
			requestDelta: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("400Mi"),
			},
			state: &CustomLimiterState{
				pod: &corev1.Pod{},
			},
			expectedError: nil,
		},
		{
			name: "matching pod within limit",
			quotaInfo: &QuotaInfo{
				CalculateInfo: QuotaCalculateInfo{
					CustomLimits: map[string]*CustomLimitConf{
						"mock": {
							Limit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
					},
					CustomUsed: map[string]corev1.ResourceList{
						"mock": {
							corev1.ResourceCPU:    resource.MustParse("30m"),
							corev1.ResourceMemory: resource.MustParse("80Mi"),
						},
					},
				},
			},
			requestDelta: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			},
			state: &CustomLimiterState{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Labels: matchedLabels},
				},
			},
			expectedError: nil,
		},
		{
			name: "matching pod exceeding limit",
			quotaInfo: &QuotaInfo{
				CalculateInfo: QuotaCalculateInfo{
					CustomLimits: map[string]*CustomLimitConf{
						"mock": {
							Limit: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("200Mi"),
							},
						},
					},
					CustomUsed: map[string]corev1.ResourceList{
						"mock": {
							corev1.ResourceCPU:    resource.MustParse("80m"),
							corev1.ResourceMemory: resource.MustParse("150Mi"),
						},
					},
				},
			},
			requestDelta: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			},
			state: &CustomLimiterState{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Labels: matchedLabels},
				},
			},
			expectedError: fmt.Errorf("insufficient resource"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err = limiter.Check(tt.quotaInfo, tt.requestDelta, tt.state)
			if tt.expectedError != nil {
				if err == nil || !strings.Contains(err.Error(), tt.expectedError.Error()) {
					t.Errorf("Check() error:\nexpected: %v\n  actual: %v", err, tt.expectedError)
				}
			} else {
				if err != nil {
					t.Errorf("Check() unexpected error: %v", err)
				}
			}
		})
	}
}
