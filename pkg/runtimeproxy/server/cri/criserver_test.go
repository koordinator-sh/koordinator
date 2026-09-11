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
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/koordinator-sh/koordinator/cmd/koord-runtime-proxy/options"
)

type failingRecoveryRuntimeServer struct {
	runtimeapi.UnimplementedRuntimeServiceServer
	listPodSandboxCalled chan struct{}
}

func (s *failingRecoveryRuntimeServer) Version(context.Context, *runtimeapi.VersionRequest) (*runtimeapi.VersionResponse, error) {
	return &runtimeapi.VersionResponse{}, nil
}

func (s *failingRecoveryRuntimeServer) ListPodSandbox(context.Context, *runtimeapi.ListPodSandboxRequest) (*runtimeapi.ListPodSandboxResponse, error) {
	close(s.listPodSandboxCalled)
	return nil, errors.New("injected ListPodSandbox failure")
}

func TestRuntimeManagerCriServerRunFailsWhenCheckpointRecoveryFails(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "kp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})
	backendEndpoint := filepath.Join(tempDir, "backend.sock")
	proxyEndpoint := filepath.Join(tempDir, "proxy.sock")

	listener, err := net.Listen("unix", backendEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	backend := &failingRecoveryRuntimeServer{listPodSandboxCalled: make(chan struct{})}
	runtimeapi.RegisterRuntimeServiceServer(grpcServer, backend)
	go grpcServer.Serve(listener)
	t.Cleanup(func() {
		grpcServer.Stop()
		listener.Close()
	})

	oldRemoteEndpoint := options.RemoteRuntimeServiceEndpoint
	oldProxyEndpoint := options.RuntimeProxyEndpoint
	options.RemoteRuntimeServiceEndpoint = backendEndpoint
	options.RuntimeProxyEndpoint = proxyEndpoint
	t.Cleanup(func() {
		options.RemoteRuntimeServiceEndpoint = oldRemoteEndpoint
		options.RuntimeProxyEndpoint = oldProxyEndpoint
		os.Remove(proxyEndpoint)
	})

	runResult := make(chan error, 1)
	go func() {
		runResult <- (&RuntimeManagerCriServer{}).Run()
	}()

	select {
	case <-backend.listPodSandboxCalled:
	case <-time.After(time.Second):
		t.Fatal("runtime proxy did not call ListPodSandbox")
	}

	select {
	case err := <-runResult:
		if err == nil {
			t.Fatal("Run returned nil after ListPodSandbox failed")
		}
		if !strings.Contains(err.Error(), "recover runtime checkpoint") {
			t.Fatalf("Run returned an unexpected error: %v", err)
		}
		if _, err := os.Stat(proxyEndpoint); !os.IsNotExist(err) {
			t.Fatalf("Run created the proxy socket after ListPodSandbox failed: %v", err)
		}
	case <-time.After(time.Second):
		if _, err := os.Stat(proxyEndpoint); err != nil {
			t.Fatalf("Run did not return and did not create the proxy socket: %v", err)
		}
		t.Fatal("Run did not return after ListPodSandbox failed; proxy socket was created")
	}
}
