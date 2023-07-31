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

package nri

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

type nriconfig struct {
	Events []string `json:"events"`
}

type Options struct {
	// support stop running other hooks once someone failed
	PluginFailurePolicy rmconfig.FailurePolicyType
	// todo: add support for disable stages
	DisableStages map[string]struct{}
	Executor      resourceexecutor.ResourceUpdateExecutor
}

type NriServer struct {
	stub            stub.Stub
	mask            stub.EventMask
	options         Options // server options
	runPodSandbox   func(*NriServer, *api.PodSandbox, *api.Container) error
	createContainer func(*NriServer, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
	updateContainer func(*NriServer, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
}

var (
	_ = stub.ConfigureInterface(&NriServer{})
	_ = stub.SynchronizeInterface(&NriServer{})
	_ = stub.RunPodInterface(&NriServer{})
	_ = stub.CreateContainerInterface(&NriServer{})
	_ = stub.UpdateContainerInterface(&NriServer{})

	events        = "RunPodSandbox,CreateContainer,UpdateContainer"
	nriSocketPath = "nri/nri.sock"
	pluginName    = "koordlet_nri"
	pluginIdx     = "00"
	cfg           nriconfig
	err           error
	opts          []stub.Option
)

func NewNriServer(opt Options) (*NriServer, error) {
	opts = append(opts, stub.WithPluginName(pluginName))
	opts = append(opts, stub.WithPluginIdx(pluginIdx))
	opts = append(opts, stub.WithSocketPath(filepath.Join(system.Conf.VarRunRootDir, nriSocketPath)))
	p := &NriServer{options: opt}
	if p.mask, err = api.ParseEventMask(events); err != nil {
		klog.Errorf("failed to parse events: %v", err)
	}
	cfg.Events = strings.Split(events, ",")

	if p.stub, err = stub.New(p, append(opts, stub.WithOnClose(p.onClose))...); err != nil {
		klog.Errorf("failed to create plugin stub: %v", err)
	}

	return p, err
}

func (s *NriServer) Start() error {
	go func() {
		if s.stub != nil {
			err := s.stub.Run(context.Background())
			if err != nil {
				klog.Errorf("nri server exited with error %v", err)
			} else {
				klog.Info("nri server start")
			}
		} else {
			klog.Error("nri stub is nil")
		}
	}()
	return nil
}

func (p *NriServer) Configure(config, runtime, version string) (stub.EventMask, error) {
	klog.Infof("got configuration data: %q from runtime %s %s", config, runtime, version)
	if config == "" {
		return p.mask, nil
	}

	err := yaml.Unmarshal([]byte(config), &cfg)
	if err != nil {
		return 0, fmt.Errorf("failed to parse provided configuration: %w", err)
	}

	p.mask, err = api.ParseEventMask(cfg.Events...)
	if err != nil {
		return 0, fmt.Errorf("failed to parse events in configuration: %w", err)
	}

	return p.mask, nil
}

func (p *NriServer) Synchronize(pods []*api.PodSandbox, containers []*api.Container) ([]*api.ContainerUpdate, error) {
	// todo: update existed containers configure
	return nil, nil
}

func (p *NriServer) RunPodSandbox(pod *api.PodSandbox) error {
	podCtx := &protocol.PodContext{}
	podCtx.FromNri(pod)
	// todo: return error or bypass error based on PluginFailurePolicy
	err := hooks.RunHooks(p.options.PluginFailurePolicy, rmconfig.PreRunPodSandbox, podCtx)
	if err != nil {
		klog.Errorf("nri hooks run error: %v", err)
		if p.options.PluginFailurePolicy == rmconfig.PolicyFail {
			return err
		}
	}
	podCtx.NriDone(p.options.Executor)
	return nil
}

func (p *NriServer) CreateContainer(pod *api.PodSandbox, container *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	containerCtx := &protocol.ContainerContext{}
	containerCtx.FromNri(pod, container)
	// todo: return error or bypass error based on PluginFailurePolicy
	err := hooks.RunHooks(p.options.PluginFailurePolicy, rmconfig.PreCreateContainer, containerCtx)
	if err != nil {
		klog.Errorf("nri run hooks error: %v", err)
		if p.options.PluginFailurePolicy == rmconfig.PolicyFail {
			return nil, nil, err
		}
	}

	adjust, _, err := Protocol2NRI(containerCtx)
	if err != nil {
		klog.Errorf("nri convert protocol to : %v", err)
		return nil, nil, nil
	}
	return adjust, nil, nil
}

func (p *NriServer) UpdateContainer(pod *api.PodSandbox, container *api.Container) ([]*api.ContainerUpdate, error) {
	containerCtx := &protocol.ContainerContext{}
	containerCtx.FromNri(pod, container)
	// todo: return error or bypass error based on PluginFailurePolicy
	err := hooks.RunHooks(p.options.PluginFailurePolicy, rmconfig.PreUpdateContainerResources, containerCtx)
	if err != nil {
		klog.Errorf("nri run hooks error: %v", err)
		if p.options.PluginFailurePolicy == rmconfig.PolicyFail {
			return nil, err
		}
	}

	_, update, err := Protocol2NRI(containerCtx)
	if err != nil {
		klog.Errorf("nri convert protocol to : %v", err)
		return nil, nil
	}

	return []*api.ContainerUpdate{update}, nil
}

func (p *NriServer) onClose() {
	p.stub.Stop()
}

func Protocol2NRI(proto protocol.HooksProtocol) (*api.ContainerAdjustment, *api.ContainerUpdate, error) {
	containerCtx := proto.(*protocol.ContainerContext)
	if containerCtx == nil {
		return nil, nil, fmt.Errorf("container protocol is nil for nri")
	}

	adjust := &api.ContainerAdjustment{}
	update := &api.ContainerUpdate{}
	// todo: add more fields conversions
	if containerCtx.Response.Resources.CPUSet != nil {
		adjust.SetLinuxCPUSetCPUs(*containerCtx.Response.Resources.CPUSet)
		update.SetLinuxCPUSetCPUs(*containerCtx.Response.Resources.CPUSet)
	}

	if containerCtx.Response.Resources.CFSQuota != nil {
		adjust.SetLinuxCPUQuota(*containerCtx.Response.Resources.CFSQuota)
		update.SetLinuxCPUQuota(*containerCtx.Response.Resources.CFSQuota)
	}

	if containerCtx.Response.Resources.CPUShares != nil {
		adjust.SetLinuxCPUShares(uint64(*containerCtx.Response.Resources.CPUShares))
		update.SetLinuxCPUShares(uint64(*containerCtx.Response.Resources.CPUShares))
	}

	if containerCtx.Response.Resources.MemoryLimit != nil {
		adjust.SetLinuxMemoryLimit(*containerCtx.Response.Resources.MemoryLimit)
		update.SetLinuxMemoryLimit(*containerCtx.Response.Resources.MemoryLimit)
	}

	if containerCtx.Response.AddContainerEnvs != nil {
		for k, v := range containerCtx.Response.AddContainerEnvs {
			adjust.AddEnv(k, v)
		}
	}

	return adjust, update, nil
}
