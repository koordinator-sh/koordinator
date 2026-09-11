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

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFailurePolicyType(t *testing.T) {
	tests := []struct {
		name       string
		typeString string
		want       FailurePolicyType
		wantErr    bool
	}{
		{
			name:       "Fail",
			typeString: "Fail",
			want:       PolicyFail,
			wantErr:    false,
		},
		{
			name:       "Ignore",
			typeString: "Ignore",
			want:       PolicyIgnore,
			wantErr:    false,
		},
		{
			name:       "unknown returns error",
			typeString: "Unknown",
			want:       "",
			wantErr:    true,
		},
		{
			name:       "empty string returns error",
			typeString: "",
			want:       "",
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetFailurePolicyType(tt.typeString)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOccursOn(t *testing.T) {
	tests := []struct {
		name     string
		hookType RuntimeHookType
		path     RuntimeRequestPath
		want     bool
	}{
		{
			name:     "PreRunPodSandbox occurs on RunPodSandbox",
			hookType: PreRunPodSandbox,
			path:     RunPodSandbox,
			want:     true,
		},
		{
			name:     "PreRunPodSandbox does not occur on StopPodSandbox",
			hookType: PreRunPodSandbox,
			path:     StopPodSandbox,
			want:     false,
		},
		{
			name:     "PostStopPodSandbox occurs on StopPodSandbox",
			hookType: PostStopPodSandbox,
			path:     StopPodSandbox,
			want:     true,
		},
		{
			name:     "PostStopPodSandbox does not occur on RunPodSandbox",
			hookType: PostStopPodSandbox,
			path:     RunPodSandbox,
			want:     false,
		},
		{
			name:     "PreCreateContainer occurs on CreateContainer",
			hookType: PreCreateContainer,
			path:     CreateContainer,
			want:     true,
		},
		{
			name:     "PreCreateContainer does not occur on StartContainer",
			hookType: PreCreateContainer,
			path:     StartContainer,
			want:     false,
		},
		{
			name:     "PreStartContainer occurs on StartContainer",
			hookType: PreStartContainer,
			path:     StartContainer,
			want:     true,
		},
		{
			name:     "PostStartContainer occurs on StartContainer",
			hookType: PostStartContainer,
			path:     StartContainer,
			want:     true,
		},
		{
			name:     "PostStartContainer does not occur on CreateContainer",
			hookType: PostStartContainer,
			path:     CreateContainer,
			want:     false,
		},
		{
			name:     "PreUpdateContainerResources occurs on UpdateContainerResources",
			hookType: PreUpdateContainerResources,
			path:     UpdateContainerResources,
			want:     true,
		},
		{
			name:     "PreUpdateContainerResources does not occur on StopContainer",
			hookType: PreUpdateContainerResources,
			path:     StopContainer,
			want:     false,
		},
		{
			name:     "PostStopContainer occurs on StopContainer",
			hookType: PostStopContainer,
			path:     StopContainer,
			want:     true,
		},
		{
			name:     "PostStopContainer does not occur on StartContainer",
			hookType: PostStopContainer,
			path:     StartContainer,
			want:     false,
		},
		{
			name:     "PreRemoveRunPodSandbox is not mapped and returns false",
			hookType: PreRemoveRunPodSandbox,
			path:     RunPodSandbox,
			want:     false,
		},
		{
			name:     "NoneRuntimeHookType returns false for any path",
			hookType: NoneRuntimeHookType,
			path:     RunPodSandbox,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.hookType.OccursOn(tt.path))
		})
	}
}

func TestPreHookType(t *testing.T) {
	tests := []struct {
		name string
		path RuntimeRequestPath
		want RuntimeHookType
	}{
		{
			name: "RunPodSandbox returns PreRunPodSandbox",
			path: RunPodSandbox,
			want: PreRunPodSandbox,
		},
		{
			name: "StopPodSandbox returns NoneRuntimeHookType",
			path: StopPodSandbox,
			want: NoneRuntimeHookType,
		},
		{
			name: "CreateContainer returns NoneRuntimeHookType",
			path: CreateContainer,
			want: NoneRuntimeHookType,
		},
		{
			name: "StartContainer returns NoneRuntimeHookType",
			path: StartContainer,
			want: NoneRuntimeHookType,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.path.PreHookType())
		})
	}
}

func TestPostHookType(t *testing.T) {
	tests := []struct {
		name string
		path RuntimeRequestPath
		want RuntimeHookType
	}{
		{
			name: "RunPodSandbox returns NoneRuntimeHookType",
			path: RunPodSandbox,
			want: NoneRuntimeHookType,
		},
		{
			name: "StopPodSandbox returns NoneRuntimeHookType",
			path: StopPodSandbox,
			want: NoneRuntimeHookType,
		},
		{
			name: "StopContainer returns NoneRuntimeHookType",
			path: StopContainer,
			want: NoneRuntimeHookType,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.path.PostHookType())
		})
	}
}

func TestHookStage(t *testing.T) {
	tests := []struct {
		name     string
		hookType RuntimeHookType
		want     RuntimeHookStage
	}{
		{
			name:     "Pre-prefixed hook type returns PreHook",
			hookType: PreRunPodSandbox,
			want:     PreHook,
		},
		{
			name:     "Post-prefixed hook type returns PostHook",
			hookType: PostStopPodSandbox,
			want:     PostHook,
		},
		{
			name:     "PreCreateContainer returns PreHook",
			hookType: PreCreateContainer,
			want:     PreHook,
		},
		{
			name:     "PostStopContainer returns PostHook",
			hookType: PostStopContainer,
			want:     PostHook,
		},
		{
			name:     "NoneRuntimeHookType returns UnknownHook",
			hookType: NoneRuntimeHookType,
			want:     UnknownHook,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.hookType.HookStage())
		})
	}
}
