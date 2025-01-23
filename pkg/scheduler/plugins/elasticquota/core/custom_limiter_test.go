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
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func TestGetCustomLimiters(t *testing.T) {
	tests := []struct {
		name         string
		pluginArgs   *config.ElasticQuotaArgs
		factories    map[string]CustomLimiterFactory
		expectedErr  error
		expectedKeys []string
	}{
		{
			name: "valid configuration",
			pluginArgs: &config.ElasticQuotaArgs{
				CustomLimiters: map[string]config.CustomLimiterConf{
					"limiter1": {
						FactoryKey:  "mock-factory",
						FactoryArgs: "args1",
					},
					"test-limiter2": {
						FactoryKey:  "mock-factory",
						FactoryArgs: "args2",
					},
				},
			},
			factories: map[string]CustomLimiterFactory{
				"mock-factory": func(key, args string) (CustomLimiter, error) {
					return &MockCustomLimiter{key: key}, nil
				},
			},
			expectedKeys: []string{"limiter1", "test-limiter2"},
		},
		{
			name: "empty key",
			pluginArgs: &config.ElasticQuotaArgs{
				CustomLimiters: map[string]config.CustomLimiterConf{
					"": {
						FactoryKey:  "mock-factory",
						FactoryArgs: "args1",
					},
				},
			},
			factories: map[string]CustomLimiterFactory{
				"mock-factory": func(key, args string) (CustomLimiter, error) {
					return &MockCustomLimiter{key: key}, nil
				},
			},
			expectedErr: errors.New("failed to initialize custom limiter with empty key"),
		},
		{
			name: "key exceeds maximum length",
			pluginArgs: &config.ElasticQuotaArgs{
				CustomLimiters: map[string]config.CustomLimiterConf{
					"thisisaverylongcustom": {
						FactoryKey:  "mock-factory",
						FactoryArgs: "args1",
					},
				},
			},
			factories: map[string]CustomLimiterFactory{
				"mock-factory": func(key, args string) (CustomLimiter, error) {
					return &MockCustomLimiter{key: key}, nil
				},
			},
			expectedErr: errors.New(validation.MaxLenError(CustomLimiterKeyMaxLen)),
		},
		{
			name: "invalid DNS1123 label",
			pluginArgs: &config.ElasticQuotaArgs{
				CustomLimiters: map[string]config.CustomLimiterConf{
					"invalid-label!": {
						FactoryKey:  "mock-factory",
						FactoryArgs: "args1",
					},
				},
			},
			factories: map[string]CustomLimiterFactory{
				"mock-factory": func(key, args string) (CustomLimiter, error) {
					return &MockCustomLimiter{key: key}, nil
				},
			},
			expectedErr: errors.New("failed to initialize custom limiter invalid-label!: [a lowercase RFC 1123 label must consist"),
		},
		{
			name: "empty factory key",
			pluginArgs: &config.ElasticQuotaArgs{
				CustomLimiters: map[string]config.CustomLimiterConf{
					"limiter1": {
						FactoryKey:  "",
						FactoryArgs: "args1",
					},
				},
			},
			factories: map[string]CustomLimiterFactory{
				"mock-factory": func(key, args string) (CustomLimiter, error) {
					return &MockCustomLimiter{key: key}, nil
				},
			},
			expectedErr: errors.New("failed to initialize custom limiter limiter1: factory key is empty"),
		},
		{
			name: "factory not found",
			pluginArgs: &config.ElasticQuotaArgs{
				CustomLimiters: map[string]config.CustomLimiterConf{
					"limiter1": {
						FactoryKey:  "nonexistent-factory",
						FactoryArgs: "args1",
					},
				},
			},
			factories: map[string]CustomLimiterFactory{
				"mock-factory": func(key, args string) (CustomLimiter, error) {
					return &MockCustomLimiter{key: key}, nil
				},
			},
			expectedErr: errors.New("failed to initialize custom limiter limiter1: factory nonexistent-factory not found"),
		},
		{
			name: "factory returns error",
			pluginArgs: &config.ElasticQuotaArgs{
				CustomLimiters: map[string]config.CustomLimiterConf{
					"limiter1": {
						FactoryKey:  "mock-factory",
						FactoryArgs: "args1",
					},
				},
			},
			factories: map[string]CustomLimiterFactory{
				"mock-factory": func(key, args string) (CustomLimiter, error) {
					return nil, errors.New("factory error")
				},
			},
			expectedErr: errors.New("failed to initialize custom limiter limiter1 by factory mock-factory, err=factory error"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			customLimiterFactories = tt.factories
			rst, err := GetCustomLimiters(tt.pluginArgs)

			// Check for expected error
			if tt.expectedErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.expectedErr)
				} else if !strings.Contains(err.Error(), tt.expectedErr.Error()) {
					t.Errorf("expected error %v, got %v", tt.expectedErr, err)
				}
				return
			}

			// Check for expected keys
			if len(rst) != len(tt.expectedKeys) {
				t.Errorf("expected %d custom limiters, got %d", len(tt.expectedKeys), len(rst))
			}
			for _, key := range tt.expectedKeys {
				if _, ok := rst[key]; !ok {
					t.Errorf("expected custom limiter %s not found", key)
				}
			}
		})
	}
}

// TestCustomLimitConfMapEquals tests the customLimitConfMapEquals function.
func TestCustomLimitConfMapEquals(t *testing.T) {
	tests := []struct {
		name     string
		a        CustomLimitConfMap
		b        CustomLimitConfMap
		expected bool
	}{
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "first is nil",
			a:        nil,
			b:        CustomLimitConfMap{},
			expected: false,
		},
		{
			name:     "second is nil",
			a:        CustomLimitConfMap{},
			b:        nil,
			expected: false,
		},
		{
			name:     "both empty",
			a:        CustomLimitConfMap{},
			b:        CustomLimitConfMap{},
			expected: true,
		},
		{
			name: "same content",
			a: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
				"key2": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("400Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: false,
					},
				},
			},
			b: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
				"key2": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("400Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: false,
					},
				},
			},
			expected: true,
		},
		{
			name: "different keys",
			a: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
			},
			b: CustomLimitConfMap{
				"key2": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
			},
			expected: false,
		},
		{
			name: "different limits",
			a: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
			},
			b: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("400Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
			},
			expected: false,
		},
		{
			name: "different args",
			a: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
			},
			b: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: false,
					},
				},
			},
			expected: false,
		},
		{
			name: "different lengths",
			a: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
			},
			b: CustomLimitConfMap{
				"key1": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: true,
					},
				},
				"key2": &CustomLimitConf{
					Limit: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("400Mi"),
					},
					Args: &MockCustomArgs{
						DebugEnabled: false,
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := customLimitConfMapEquals(tt.a, tt.b)
			if actual != tt.expected {
				t.Errorf("customLimitConfMapEquals(%v, %v) = %v, want %v", tt.a, tt.b, actual, tt.expected)
			}
		})
	}
}

// GetTestQuotasForCustomLimiters returns a list of test quotas.
func GetTestQuotasForCustomLimiters() (quotas []*v1alpha1.ElasticQuota, quotaMap map[string]*v1alpha1.ElasticQuota) {
	// quota 1 Max[100, 100]  Min[80,80]
	// |-- quota 11 Max[50, 50]  Min[30,30]
	// 		|-- quota 111 Max[50, 50]  Min[20,20]
	// |-- quota 12 Max[50, 50]  Min[30,30]
	//   	|-- quota 121 Max[50, 50]  Min[20,20]
	//   	|-- quota 122 Max[50, 50]  Min[10,10]
	// quota 2 Max[100, 100]  Min[60,60]
	// |-- quota 21 Max[50, 50]  Min[40,40]
	//   	|-- quota 211 Max[50, 50]  Min[15,15]
	q1 := CreateQuota("1", extension.RootQuotaName, 100, 100, 80, 80, true, true)
	q2 := CreateQuota("2", extension.RootQuotaName, 100, 100, 60, 60, true, true)
	q11 := CreateQuota("11", "1", 50, 50, 30, 30, true, true)
	q12 := CreateQuota("12", "1", 50, 50, 30, 30, true, true)
	q21 := CreateQuota("21", "2", 50, 50, 40, 40, true, true)
	q111 := CreateQuota("111", "11", 50, 50, 20, 20, true, false)
	q121 := CreateQuota("121", "12", 50, 50, 20, 20, true, false)
	q122 := CreateQuota("122", "12", 50, 50, 10, 10, true, false)
	q211 := CreateQuota("211", "21", 50, 50, 15, 15, true, false)
	quotaMap = make(map[string]*v1alpha1.ElasticQuota)
	for _, q := range []*v1alpha1.ElasticQuota{q1, q2, q11, q12, q21, q111, q121, q122, q211} {
		quotas = append(quotas, q)
		quotaMap[q.Name] = q
	}
	return
}

func UpdateQuotas(gqm *GroupQuotaManager, preHookFn func(quotasMap map[string]*v1alpha1.ElasticQuota),
	quotas ...*v1alpha1.ElasticQuota) error {
	if preHookFn != nil {
		quotasMap := make(map[string]*v1alpha1.ElasticQuota)
		for _, q := range quotas {
			quotasMap[q.Name] = q
		}
		preHookFn(quotasMap)
	}
	for _, q := range quotas {
		err := gqm.UpdateQuota(q, false)
		if err != nil {
			return err
		}
	}
	return nil
}
