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
	"testing"

	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	resource_executor "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/resexecutor"
)

func TestNewRuntimeManagerCRIServer(t *testing.T) {
	type args struct {
		proxyConfig *config.Config
	}

	tests := []struct {
		name     string
		args     args
		expected bool
	}{
		{
			name: "Initialize the runtimeManager server success",
			args: args{
				proxyConfig: &config.Config{
					RuntimeProxyEndpoint:         " /var/run/koord-runtimeproxy/runtimeproxy.sock",
					RemoteRuntimeServiceEndpoint: "/var/run/containerd/containerd.sock",
					RemoteImageServiceEndpoint:   "/var/run/containerd/containerd.sock",
				},
			},
			expected: true,
		},
		{
			name: "Initialize the runtimeManager server fail",
			args: args{
				proxyConfig: &config.Config{
					RuntimeProxyEndpoint: " /var/run/koord-runtimeproxy/runtimeproxy.sock",
				},
			},
			expected: false,
		},
		{
			name:     "runtime proxy config is nil",
			args:     args{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRuntimeManagerCRIServer(tt.args.proxyConfig)
			if (err != nil) == tt.expected {
				t.Errorf("got err: %v, want: %v", err, tt.expected)
			}
		})
	}
}

func TestCreateRuntimeAndImageClient(t *testing.T) {
	type args struct {
		runtimeSock string
		imageSock   string
	}

	tests := []struct {
		name     string
		args     args
		expected bool
	}{
		{
			name: "create container runtime and image service success.",
			args: args{
				runtimeSock: "/var/run/containerd/containerd.sock",
				imageSock:   "/var/run/containerd/containerd.sock",
			},
			expected: true,
		},
		{
			name:     "create container runtime and image service fail.",
			args:     args{},
			expected: false,
		},
		{
			name: "only set runtime service sock",
			args: args{
				runtimeSock: "/var/run/containerd/containerd.sock",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := createRuntimeAndImageClient(tt.args.runtimeSock, tt.args.imageSock)
			if (err != nil) == tt.expected {
				t.Errorf("got err: %v, expected: %v", err, tt.expected)
			}
		})
	}
}

func TestGetRuntimeHookInfo(t *testing.T) {
	type args struct {
		serviceType RuntimeServiceType
	}

	type desired struct {
		runtimeRequestPath  config.RuntimeRequestPath
		runtimeResourceType resource_executor.RuntimeResourceType
	}

	tests := []struct {
		name     string
		args     args
		desired  desired
		expected bool
	}{
		{
			name: "RunPodSandbox",
			args: args{
				serviceType: RunPodSandbox,
			},
			desired: desired{
				runtimeRequestPath:  config.RunPodSandbox,
				runtimeResourceType: resource_executor.RuntimePodResource,
			},
			expected: true,
		},
		{
			name: "StopPodSandbox",
			args: args{
				serviceType: StopPodSandbox,
			},
			desired: desired{
				runtimeRequestPath:  config.StopPodSandbox,
				runtimeResourceType: resource_executor.RuntimePodResource,
			},
			expected: true,
		},
		{
			name: "CreateContainer",
			args: args{
				serviceType: CreateContainer,
			},
			desired: desired{
				runtimeRequestPath:  config.CreateContainer,
				runtimeResourceType: resource_executor.RuntimeContainerResource,
			},
			expected: true,
		},
		{
			name: "StartContainer",
			args: args{
				serviceType: StartContainer,
			},
			desired: desired{
				runtimeRequestPath:  config.StartContainer,
				runtimeResourceType: resource_executor.RuntimeContainerResource,
			},
			expected: true,
		},
		{
			name: "StopContainer",
			args: args{
				serviceType: StopContainer,
			},
			desired: desired{
				runtimeRequestPath:  config.StopContainer,
				runtimeResourceType: resource_executor.RuntimeContainerResource,
			},
			expected: true,
		},
		{
			name: "UpdateContainerResources",
			args: args{
				serviceType: UpdateContainerResources,
			},
			desired: desired{
				runtimeRequestPath:  config.UpdateContainerResources,
				runtimeResourceType: resource_executor.RuntimeContainerResource,
			},
			expected: true,
		},
		{
			name: "NoneRuntimeHookPath",
			args: args{
				serviceType: RuntimeServiceType(100),
			},
			desired: desired{
				runtimeRequestPath:  config.NoneRuntimeHookPath,
				runtimeResourceType: resource_executor.RuntimeNoopResource,
			},
			expected: true,
		},
	}

	proxyConfig := &config.Config{
		RuntimeProxyEndpoint:         " /var/run/koord-runtimeproxy/runtimeproxy.sock",
		RemoteRuntimeServiceEndpoint: "/var/run/containerd/containerd.sock",
		RemoteImageServiceEndpoint:   "/var/run/containerd/containerd.sock",
	}

	manger, err := NewRuntimeManagerCRIServer(proxyConfig)
	if err != nil {
		t.Errorf("got err: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeRequestPath, runtimeResourceType := manger.getRuntimeHookInfo(tt.args.serviceType)
			observed := runtimeRequestPath == tt.desired.runtimeRequestPath && runtimeResourceType == tt.desired.runtimeResourceType
			if observed != tt.expected {
				t.Errorf("observed: %v expected: %v", observed, tt.expected)
			}
		})

	}
}
