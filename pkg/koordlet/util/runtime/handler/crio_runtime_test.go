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

package handler

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	mockclient "github.com/koordinator-sh/koordinator/pkg/koordlet/util/runtime/handler/mockclient"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_NewCrioRuntimeHandler(t *testing.T) {
	stubs := gostub.Stub(&GrpcDial, func(context context.Context, target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return &grpc.ClientConn{}, nil
	})
	defer stubs.Reset()

	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()

	helper.WriteFileContents("/var/run/crio/crio.sock", "test")
	system.Conf.VarRunRootDir = filepath.Join(helper.TempDir, "/var/run")
	CrioEndpoint1 := GetCrioEndpoint()
	unixEndPoint := fmt.Sprintf("unix://%s", CrioEndpoint1)
	crioRuntime, err := NewCrioRuntimeHandler(unixEndPoint)
	assert.NoError(t, err)
	assert.NotNil(t, crioRuntime)

	// custom VarRunRootDir
	helper.WriteFileContents("/host-var-run/crio/crio.sock", "test1")
	system.Conf.VarRunRootDir = filepath.Join(helper.TempDir, "/host-var-run")
	CrioEndpoint1 = GetCrioEndpoint()
	unixEndPoint = fmt.Sprintf("unix://%s", CrioEndpoint1)
	crioRuntime, err = NewCrioRuntimeHandler(unixEndPoint)
	assert.NoError(t, err)
	assert.NotNil(t, crioRuntime)
}

func Test_Crio_StopContainer(t *testing.T) {
	type args struct {
		name         string
		containerId  string
		runtimeError error
		expectError  bool
	}
	tests := []args{
		{
			name:         "test_stopContainer_success",
			containerId:  "test_container_id",
			runtimeError: nil,
			expectError:  false,
		},
		{
			name:         "test_stopContainer_fail",
			containerId:  "test_container_id",
			runtimeError: fmt.Errorf("stopContainer error"),
			expectError:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			mockRuntimeClient := mockclient.NewMockRuntimeServiceClient(ctl)
			mockRuntimeClient.EXPECT().StopContainer(gomock.Any(), gomock.Any()).Return(nil, tt.runtimeError)

			runtimeHandler := ContainerdRuntimeHandler{runtimeServiceClient: mockRuntimeClient, timeout: 1, endpoint: GetCrioEndpoint()}
			gotErr := runtimeHandler.StopContainer(tt.containerId, 1)
			assert.Equal(t, gotErr != nil, tt.expectError)

		})
	}
}

func Test_Crio_UpdateContainerResources(t *testing.T) {
	type args struct {
		name         string
		containerId  string
		runtimeError error
		expectError  bool
	}
	tests := []args{
		{
			name:         "test_UpdateContainerResources_success",
			containerId:  "test_container_id",
			runtimeError: nil,
			expectError:  false,
		},
		{
			name:         "test_UpdateContainerResources_fail",
			containerId:  "test_container_id",
			runtimeError: fmt.Errorf("UpdateContainerResources error"),
			expectError:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			mockRuntimeClient := mockclient.NewMockRuntimeServiceClient(ctl)
			mockRuntimeClient.EXPECT().UpdateContainerResources(gomock.Any(), gomock.Any()).Return(nil, tt.runtimeError)

			runtimeHandler := ContainerdRuntimeHandler{runtimeServiceClient: mockRuntimeClient, timeout: 1, endpoint: GetCrioEndpoint()}
			gotErr := runtimeHandler.UpdateContainerResources(tt.containerId, UpdateOptions{})
			assert.Equal(t, tt.expectError, gotErr != nil)
		})
	}
}
