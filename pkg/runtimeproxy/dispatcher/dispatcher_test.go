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

package dispatcher

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/client"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

func TestRuntimeHookDispatcher_Dispatch(t *testing.T) {
	tests := []struct {
		name               string
		requestPath        config.RuntimeRequestPath
		allHooks           []*config.RuntimeHookConfig
		request            interface{}
		hookSeverReturnErr error
		expectedOperation  config.FailurePolicyType
		expectReturnErr    bool
	}{
		{
			name:              "no hook registered",
			requestPath:       config.RunPodSandbox,
			request:           &v1alpha1.PodSandboxHookRequest{},
			allHooks:          []*config.RuntimeHookConfig{},
			expectedOperation: config.PolicyNone,
			expectReturnErr:   false,
		},
		{
			name:        "normal hook hit, and hook server access ok",
			requestPath: config.RunPodSandbox,
			request:     &v1alpha1.PodSandboxHookRequest{},
			allHooks: []*config.RuntimeHookConfig{
				{
					RemoteEndpoint: "endpoint0",
					FailurePolicy:  config.PolicyFail,
					RuntimeHooks: []config.RuntimeHookType{
						config.PreRunPodSandbox,
					},
				},
			},
			expectedOperation: config.PolicyFail,
			expectReturnErr:   false,
		},
		{
			name:               "normal hook hit, and hook server access fail, should return err and PolicyFail",
			requestPath:        config.RunPodSandbox,
			request:            &v1alpha1.PodSandboxHookRequest{},
			hookSeverReturnErr: fmt.Errorf("should return err to kubelet instead of skipping"),
			allHooks: []*config.RuntimeHookConfig{
				{
					RemoteEndpoint: "endpoint0",
					FailurePolicy:  config.PolicyFail,
					RuntimeHooks: []config.RuntimeHookType{
						config.PreRunPodSandbox,
					},
				},
			},
			expectedOperation: config.PolicyFail,
			expectReturnErr:   true,
		},
		{
			name:        "has hook but not the requested one",
			requestPath: config.RunPodSandbox,
			request:     &v1alpha1.PodSandboxHookRequest{},
			allHooks: []*config.RuntimeHookConfig{
				{
					RemoteEndpoint: "endpoint0",
					FailurePolicy:  config.PolicyFail,
					RuntimeHooks: []config.RuntimeHookType{
						config.PreUpdateContainerResources,
					},
				},
			},
			expectedOperation: config.PolicyNone,
			expectReturnErr:   false,
		},
	}
	for _, tt := range tests {
		configManager := NewMockManager(tt.allHooks)
		clientManager := NewMockHookServerClientManager(tt.hookSeverReturnErr)

		// construct the real runtimeHookDispatcher
		runtimeHookDispatcher := &RuntimeHookDispatcher{
			hookManager: configManager,
			cm:          clientManager,
		}
		rsp, err, operation := runtimeHookDispatcher.Dispatch(context.TODO(), tt.requestPath, config.PreHook, tt.request)
		assert.Equal(t, operation, tt.expectedOperation, tt.name)
		assert.Equal(t, err != nil, tt.expectReturnErr)
		//if err != nil, the rsp need to be nil
		if err != nil {
			assert.Equal(t, rsp == nil, true)
		}
	}
}

type mockManager struct {
	allHooks []*config.RuntimeHookConfig
}

func NewMockManager(allHooks []*config.RuntimeHookConfig) *mockManager {
	return &mockManager{
		allHooks: allHooks,
	}
}

func (m *mockManager) GetAllHook() []*config.RuntimeHookConfig {
	return m.allHooks
}

func (m *mockManager) Run() error {
	return nil
}

type mockHookServerClientManager struct {
	hookServerError error
}

func NewMockHookServerClientManager(hookServerError error) *mockHookServerClientManager {
	return &mockHookServerClientManager{
		hookServerError: hookServerError,
	}
}

func (m *mockHookServerClientManager) RuntimeHookServerClient(serverPath client.HookServerPath) (*client.RuntimeHookClient, error) {
	return &client.RuntimeHookClient{
		RuntimeHookServiceClient: &mockHookServerClient{
			hookServerError: m.hookServerError,
		},
	}, nil
}

type mockHookServerClient struct {
	hookServerError error
}

func (m *mockHookServerClient) PreRunPodSandboxHook(ctx context.Context, in *v1alpha1.PodSandboxHookRequest, opts ...grpc.CallOption) (*v1alpha1.PodSandboxHookResponse, error) {
	return &v1alpha1.PodSandboxHookResponse{}, m.hookServerError
}
func (m *mockHookServerClient) PostStopPodSandboxHook(ctx context.Context, in *v1alpha1.PodSandboxHookRequest, opts ...grpc.CallOption) (*v1alpha1.PodSandboxHookResponse, error) {
	return nil, nil
}
func (m *mockHookServerClient) PreCreateContainerHook(ctx context.Context, in *v1alpha1.ContainerResourceHookRequest, opts ...grpc.CallOption) (*v1alpha1.ContainerResourceHookResponse, error) {
	return nil, nil
}
func (m *mockHookServerClient) PreStartContainerHook(ctx context.Context, in *v1alpha1.ContainerResourceHookRequest, opts ...grpc.CallOption) (*v1alpha1.ContainerResourceHookResponse, error) {
	return nil, nil
}
func (m *mockHookServerClient) PostStartContainerHook(ctx context.Context, in *v1alpha1.ContainerResourceHookRequest, opts ...grpc.CallOption) (*v1alpha1.ContainerResourceHookResponse, error) {
	return nil, nil
}
func (m *mockHookServerClient) PostStopContainerHook(ctx context.Context, in *v1alpha1.ContainerResourceHookRequest, opts ...grpc.CallOption) (*v1alpha1.ContainerResourceHookResponse, error) {
	return nil, nil
}
func (m *mockHookServerClient) PreUpdateContainerResourcesHook(ctx context.Context, in *v1alpha1.ContainerResourceHookRequest, opts ...grpc.CallOption) (*v1alpha1.ContainerResourceHookResponse, error) {
	return nil, nil
}
