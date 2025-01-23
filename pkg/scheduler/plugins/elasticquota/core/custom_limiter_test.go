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

	"k8s.io/apimachinery/pkg/util/validation"

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
