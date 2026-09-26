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

package cri

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/cmd/koord-runtime-proxy/options"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/store"
)

func TestInterceptRuntimeRequestHandlesCheckpointAfterStop(t *testing.T) {
	const (
		skipHookKey = "test.koordinator.sh/skip-hook"
		skipHookVal = "true"
	)

	previousKey, previousVal := options.RuntimeHookServerKey, options.RuntimeHookServerVal
	options.RuntimeHookServerKey, options.RuntimeHookServerVal = skipHookKey, skipHookVal
	t.Cleanup(func() {
		options.RuntimeHookServerKey, options.RuntimeHookServerVal = previousKey, previousVal
	})

	tests := []struct {
		name                  string
		serviceType           RuntimeServiceType
		request               interface{}
		writeCheckpoint       func() error
		getCheckpoint         func() interface{}
		deleteCheckpoint      func()
		backendResponse       interface{}
		backendErr            error
		checkpointShouldExist bool
	}{
		{
			name:        "container checkpoint is deleted after successful stop",
			serviceType: StopContainer,
			request: &runtimeapi.StopContainerRequest{
				ContainerId: "container-stop-success",
			},
			writeCheckpoint: func() error {
				return store.WriteContainerInfo("container-stop-success", &store.ContainerInfo{
					ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
						PodLabels: map[string]string{skipHookKey: skipHookVal},
					},
				})
			},
			getCheckpoint: func() interface{} {
				return store.GetContainerInfo("container-stop-success")
			},
			deleteCheckpoint: func() {
				store.DeleteContainerInfo("container-stop-success")
			},
			backendResponse:       &runtimeapi.StopContainerResponse{},
			checkpointShouldExist: false,
		},
		{
			name:        "container checkpoint is retained after failed stop",
			serviceType: StopContainer,
			request: &runtimeapi.StopContainerRequest{
				ContainerId: "container-stop-failure",
			},
			writeCheckpoint: func() error {
				return store.WriteContainerInfo("container-stop-failure", &store.ContainerInfo{
					ContainerResourceHookRequest: &v1alpha1.ContainerResourceHookRequest{
						PodLabels: map[string]string{skipHookKey: skipHookVal},
					},
				})
			},
			getCheckpoint: func() interface{} {
				return store.GetContainerInfo("container-stop-failure")
			},
			deleteCheckpoint: func() {
				store.DeleteContainerInfo("container-stop-failure")
			},
			backendErr:            errors.New("backend stop failed"),
			checkpointShouldExist: true,
		},
		{
			name:        "pod sandbox checkpoint is deleted after successful stop",
			serviceType: StopPodSandbox,
			request: &runtimeapi.StopPodSandboxRequest{
				PodSandboxId: "pod-stop-success",
			},
			writeCheckpoint: func() error {
				return store.WritePodSandboxInfo("pod-stop-success", &store.PodSandboxInfo{
					PodSandboxHookRequest: &v1alpha1.PodSandboxHookRequest{
						Labels: map[string]string{skipHookKey: skipHookVal},
					},
				})
			},
			getCheckpoint: func() interface{} {
				return store.GetPodSandboxInfo("pod-stop-success")
			},
			deleteCheckpoint: func() {
				store.DeletePodSandboxInfo("pod-stop-success")
			},
			backendResponse:       &runtimeapi.StopPodSandboxResponse{},
			checkpointShouldExist: false,
		},
		{
			name:        "pod sandbox checkpoint is retained after failed stop",
			serviceType: StopPodSandbox,
			request: &runtimeapi.StopPodSandboxRequest{
				PodSandboxId: "pod-stop-failure",
			},
			writeCheckpoint: func() error {
				return store.WritePodSandboxInfo("pod-stop-failure", &store.PodSandboxInfo{
					PodSandboxHookRequest: &v1alpha1.PodSandboxHookRequest{
						Labels: map[string]string{skipHookKey: skipHookVal},
					},
				})
			},
			getCheckpoint: func() interface{} {
				return store.GetPodSandboxInfo("pod-stop-failure")
			},
			deleteCheckpoint: func() {
				store.DeletePodSandboxInfo("pod-stop-failure")
			},
			backendErr:            errors.New("backend stop failed"),
			checkpointShouldExist: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, tt.writeCheckpoint())
			t.Cleanup(tt.deleteCheckpoint)

			_, err := (&RuntimeManagerCriServer{}).InterceptRuntimeRequest(
				tt.serviceType,
				context.Background(),
				tt.request,
				func(context.Context, interface{}) (interface{}, error) {
					return tt.backendResponse, tt.backendErr
				},
				false,
			)

			if tt.backendErr != nil {
				require.ErrorIs(t, err, tt.backendErr)
			} else {
				require.NoError(t, err)
			}
			if tt.checkpointShouldExist {
				require.NotNil(t, tt.getCheckpoint())
			} else {
				require.Nil(t, tt.getCheckpoint())
			}
		})
	}
}
