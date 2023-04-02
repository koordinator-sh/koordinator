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

package options

import (
	"fmt"

	"github.com/spf13/pflag"
)

const (
	DefaultRuntimeProxyEndpoint = "/var/run/koord-runtimeproxy/runtimeproxy.sock"

	DefaultContainerdRuntimeServiceEndpoint = "/var/run/containerd/containerd.sock"
	DefaultContainerdImageServiceEndpoint   = "/var/run/containerd/containerd.sock"

	BackendRuntimeModeContainerd = "Containerd"
	BackendRuntimeModeDockerd    = "Docker"
	DefaultBackendRuntimeMode    = BackendRuntimeModeContainerd
)

type Options struct {
	RuntimeProxyEndpoint         string
	RuntimeMode                  string
	RemoteRuntimeServiceEndpoint string
	RemoyeRuntimeImageEndpoint   string
}

func NewOptions() *Options {
	return &Options{}
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	if o == nil {
		return
	}

	fs.StringVar(&o.RuntimeProxyEndpoint, "koord-runtimeproxy-endpoint", DefaultRuntimeProxyEndpoint, "koord-runtimeproxy service endpoint.")
	fs.StringVar(&o.RuntimeMode, "backend-runtime-mode", DefaultBackendRuntimeMode, "backend container runtime engine(Containerd or Dockerd).")
	fs.StringVar(&o.RemoteRuntimeServiceEndpoint, "remote-runtime-service-endpoint", DefaultContainerdRuntimeServiceEndpoint, "backend runtime service endpoint.")
	fs.StringVar(&o.RemoyeRuntimeImageEndpoint, "remote-image-service-endpoint", DefaultContainerdImageServiceEndpoint, "backend image service endpoint.")
}

func (o *Options) Validate() error {
	if o.RuntimeProxyEndpoint == "" {
		return fmt.Errorf("must be set koord-runtimeproxy-endpoint")
	}

	if o.RuntimeMode == "" {
		return fmt.Errorf("must be set backend-runtime-mode")
	}

	if o.RemoteRuntimeServiceEndpoint == "" {
		return fmt.Errorf("must be set remote-runtime-service-endpoint")
	}

	if o.RemoyeRuntimeImageEndpoint == "" {
		return fmt.Errorf("must be set remote-image-service-endpoint")
	}

	return nil
}
