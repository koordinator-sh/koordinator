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

package config

import "testing"

func TestGetFailurePolicyType(t *testing.T) {
	tests := []struct {
		name     string
		policy   string
		desired  FailurePolicyType
		expected bool
	}{
		{
			name:     "policy is Fail",
			policy:   "Fail",
			desired:  PolicyFail,
			expected: true,
		},
		{
			name:     "policy is Ignore",
			policy:   "Ignore",
			desired:  PolicyIgnore,
			expected: true,
		},
		{
			name:     "policy is None",
			policy:   "None",
			desired:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := GetFailurePolicyType(tt.policy)
			if (err != nil) == tt.expected {
				t.Errorf("got err: %v", err)
			}

			if policy != tt.desired {
				t.Errorf("got policy: %v, want: %v", policy, tt.desired)
			}
		})
	}
}

func TestOccursOn(t *testing.T) {
	tests := []struct {
		name               string
		runtimeHookType    RuntimeHookType
		runtimeRequestPath RuntimeRequestPath
		expected           bool
	}{
		{
			name:               "RunPodSandbox",
			runtimeHookType:    PreRunPodSandbox,
			runtimeRequestPath: RunPodSandbox,
			expected:           true,
		},
		{
			name:               "PostStopPodSandbox",
			runtimeHookType:    PostStopPodSandbox,
			runtimeRequestPath: StopPodSandbox,
			expected:           true,
		},
		{
			name:               "PreCreateContainer",
			runtimeHookType:    PreCreateContainer,
			runtimeRequestPath: CreateContainer,
			expected:           true,
		},
		{
			name:               "PreStartContainer",
			runtimeHookType:    PreStartContainer,
			runtimeRequestPath: StartContainer,
			expected:           true,
		},
		{
			name:               "PostStartContainer",
			runtimeHookType:    PostStartContainer,
			runtimeRequestPath: StartContainer,
			expected:           true,
		},
		{
			name:               "PreUpdateContainerResources",
			runtimeHookType:    PreUpdateContainerResources,
			runtimeRequestPath: UpdateContainerResources,
			expected:           true,
		},
		{
			name:               "PostStopContainer",
			runtimeHookType:    PostStopContainer,
			runtimeRequestPath: StopContainer,
			expected:           true,
		},
		{
			name:               "OtherCase",
			runtimeHookType:    RuntimeHookType("Other"),
			runtimeRequestPath: StopContainer,
			expected:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.runtimeHookType.OccursOn(tt.runtimeRequestPath)
			if got != tt.expected {
				t.Errorf("got %v, want: %v", got, tt.expected)
			}
		})
	}
}

func TestPreHookType(t *testing.T) {
	tests := []struct {
		name               string
		runtimeRequestPath RuntimeRequestPath
		runtimeHookType    RuntimeHookType
		expected           bool
	}{
		{
			name:               "RunPodSandbox",
			runtimeRequestPath: RunPodSandbox,
			runtimeHookType:    PreRunPodSandbox,
			expected:           true,
		},
		{
			name:               "Bad Case",
			runtimeRequestPath: CreateContainer,
			runtimeHookType:    NoneRuntimeHookType,
			expected:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeHookType := tt.runtimeRequestPath.PreHookType()
			if (runtimeHookType != tt.runtimeHookType) == tt.expected {
				t.Errorf("got hook type: %v, want: %v", runtimeHookType, tt.expected)
			}
		})
	}
}

func TestHookStage(t *testing.T) {
	tests := []struct {
		name             string
		runtimeHookType  RuntimeHookType
		runtimeHookStage RuntimeHookStage
		expected         bool
	}{
		{
			name:             "pre stage",
			runtimeHookType:  PreRunPodSandbox,
			runtimeHookStage: PreHook,
			expected:         true,
		},
		{
			name:             "post stage",
			runtimeHookType:  PostStartContainer,
			runtimeHookStage: PostHook,
			expected:         true,
		},
		{
			name:             "Bad case",
			runtimeHookType:  NoneRuntimeHookType,
			runtimeHookStage: UnknownHook,
			expected:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage := tt.runtimeHookType.HookStage()
			if (stage == tt.runtimeHookStage) != tt.expected {
				t.Errorf("got stage: %v, want: %v", stage, tt.runtimeHookStage)
			}
		})
	}
}
