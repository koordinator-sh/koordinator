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

package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/term"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/cmd/koord-runtime-proxy/app/options"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/cri"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/server/docker"
)

func NewRuntimeProxyCommand(ctx context.Context) *cobra.Command {
	opts := options.NewOptions()

	cmd := &cobra.Command{
		Use:  "koord-runtime-proxy",
		Long: "koord-runtime-proxy is a proxy between kubelet and the container runtime.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}

			if err := run(ctx, opts); err != nil {
				return err
			}
			return nil
		},
	}

	nfs := cliflag.NamedFlagSets{}

	genericFlagSet := nfs.FlagSet("generic")
	genericFlagSet.AddGoFlagSet(flag.CommandLine)
	opts.AddFlags(genericFlagSet)

	logsFlagSet := nfs.FlagSet("logs")
	options.AddKlogFlags(logsFlagSet)

	cmd.Flags().AddFlagSet(genericFlagSet)
	cmd.Flags().AddFlagSet(logsFlagSet)

	cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	cliflag.SetUsageAndHelpFunc(cmd, nfs, cols)
	return cmd
}

func run(ctx context.Context, opts *options.Options) error {
	if opts == nil {
		return fmt.Errorf("runtime proxy options is nil")
	}

	proxyConfig, err := setup(opts)
	if err != nil {
		return err
	}

	switch opts.RuntimeMode {
	case options.BackendRuntimeModeContainerd:
		server, err := cri.NewRuntimeManagerCRIServer(proxyConfig)
		if err != nil {
			return err
		}

		if err = server.Run(); err != nil {
			return err
		}
	case options.BackendRuntimeModeDockerd:
		server, err := docker.NewRuntimeManagerDockerdServer(proxyConfig)
		if err != nil {
			return err
		}

		if err = server.Run(); err != nil {
			return err
		}
	default:
		klog.Fatalf("unknown container runtime engine: %v", opts.RuntimeMode)
	}

	select {
	case <-ctx.Done():
		klog.Infof("koordiantor runtime-proxy shutting down")
	}
	return nil
}

func setup(opts *options.Options) (*config.Config, error) {
	err := os.Remove(opts.RuntimeProxyEndpoint)
	if err != nil && !os.IsNotExist(err) {
		klog.Errorf("Failed to cleanup runtime proxy endpoint: %v", err)
		return nil, err
	}

	err = os.MkdirAll(filepath.Dir(opts.RuntimeProxyEndpoint), 0755)
	if err != nil {
		klog.Errorf("Failed to mkdir %v for runtime proxy service.", err)
		return nil, err
	}

	proxyConfig := &config.Config{
		RuntimeProxyEndpoint:         opts.RuntimeProxyEndpoint,
		RemoteRuntimeServiceEndpoint: opts.RemoteRuntimeServiceEndpoint,
		RemoteImageServiceEndpoint:   opts.RemoyeRuntimeImageEndpoint,
	}

	return proxyConfig, nil
}
