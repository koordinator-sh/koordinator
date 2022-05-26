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

package main

import (
	"flag"

	"github.com/spf13/pflag"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/cmd/runtime-manager/options"
	"github.com/koordinator-sh/koordinator/pkg/runtime-manager/server/cri"
)

func main() {
	flag.StringVar(&options.RuntimeManagerEndpoint, "runtime-manager-endpoint", options.DefaultRuntimeManagerEndpoint,
		"runtime-manager service endpoint.")
	flag.StringVar(&options.RemoteRuntimeServiceEndpoint, "remote-runtime-service-endpoint", options.DefaultContainerdRuntimeServiceEndpoint,
		"backend runtime service endpoint.")
	flag.StringVar(&options.RemoteImageServiceEndpoint, "remote-image-service-endpoint", options.DefaultContainerdImageServiceEndpoint,
		"backend image service endpoint.")
	flag.StringVar(&options.BackendRuntimeMode, "backend-runtime-mode", options.DefaultBackendRuntimeMode,
		"backend container engine(Containerd|Docker).")

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	switch options.BackendRuntimeMode {
	case options.BackendRuntimeModeContainerd:
		server := cri.NewRuntimeManagerCriServer()
		go server.Run()
	case options.BackendRuntimeModeDocker:
	default:
		klog.Fatalf("unknown runtime engine backend %v", options.BackendRuntimeMode)
	}

	stopCh := genericapiserver.SetupSignalHandler()
	<-stopCh
	klog.Info("RuntimeManager shutting down")
}
