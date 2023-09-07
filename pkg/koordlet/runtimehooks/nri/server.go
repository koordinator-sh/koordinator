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

type nriConfig struct {
	Events []string `json:"events"`
}

type Options struct {
	NriSocketPath string
	// support stop running other hooks once someone failed
	PluginFailurePolicy rmconfig.FailurePolicyType
	// todo: add support for disable stages
	DisableStages map[string]struct{}
	Executor      resourceexecutor.ResourceUpdateExecutor
}

func (o Options) Validate() error {
	// a fast check for the NRI support status
	completeNriSocketPath := filepath.Join(system.Conf.VarRunRootDir, o.NriSocketPath)
	if !system.FileExists(completeNriSocketPath) {
		return fmt.Errorf("nri socket path %q does not exist", completeNriSocketPath)
	}

	return nil
}

type NriServer struct {
	cfg     nriConfig
	stub    stub.Stub
	mask    stub.EventMask
	options Options // server options
}

const (
	events     = "RunPodSandbox,CreateContainer,UpdateContainer"
	pluginName = "koordlet_nri"
	pluginIdx  = "00"
)

var (
	_ = stub.ConfigureInterface(&NriServer{})
	_ = stub.SynchronizeInterface(&NriServer{})
	_ = stub.RunPodInterface(&NriServer{})
	_ = stub.CreateContainerInterface(&NriServer{})
	_ = stub.UpdateContainerInterface(&NriServer{})
)

func NewNriServer(opt Options) (*NriServer, error) {
	err := opt.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate nri server, err: %w", err)
	}

	var opts []stub.Option
	opts = append(opts, stub.WithPluginName(pluginName))
	opts = append(opts, stub.WithPluginIdx(pluginIdx))
	opts = append(opts, stub.WithSocketPath(filepath.Join(system.Conf.VarRunRootDir, opt.NriSocketPath)))
	p := &NriServer{options: opt}
	if p.mask, err = api.ParseEventMask(events); err != nil {
		klog.Errorf("failed to parse events %v", err)
		return p, err
	}
	p.cfg.Events = strings.Split(events, ",")

	if p.stub, err = stub.New(p, append(opts, stub.WithOnClose(p.onClose))...); err != nil {
		klog.Errorf("failed to create plugin stub: %v", err)
		return nil, err
	}

	return p, nil
}

func (p *NriServer) Start() error {
	go func() {
		if p.stub != nil {
			err := p.stub.Run(context.Background())
			if err != nil {
				klog.Errorf("nri server exited with error: %v", err)
			} else {
				klog.V(4).Info("nri server started")
			}
		} else {
			klog.V(4).Info("nri stub is nil")
		}
	}()
	return nil
}

func (p *NriServer) Configure(config, runtime, version string) (stub.EventMask, error) {
	klog.V(4).Infof("got configuration data: %q from runtime %s %s", config, runtime, version)
	if config == "" {
		return p.mask, nil
	}

	err := yaml.Unmarshal([]byte(config), &p.cfg)
	if err != nil {
		return 0, fmt.Errorf("failed to parse provided configuration: %w", err)
	}

	p.mask, err = api.ParseEventMask(p.cfg.Events...)
	if err != nil {
		return 0, fmt.Errorf("failed to parse events in configuration: %w", err)
	}

	klog.V(6).Infof("handle NRI Configure successfully, config %s, runtime %s, version %s",
		config, runtime, version)
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

	klog.V(6).Infof("handle NRI RunPodSandbox successfully, pod %s/%s", pod.GetNamespace(), pod.GetName())
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

	adjust, _, err := containerCtx.NriDone(p.options.Executor)
	if err != nil {
		klog.Errorf("containerCtx nri done failed: %v", err)
		return nil, nil, nil
	}

	klog.V(6).Infof("handle NRI CreateContainer successfully, container %s/%s/%s",
		pod.GetNamespace(), pod.GetName(), container.GetName())
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

	_, update, err := containerCtx.NriDone(p.options.Executor)
	if err != nil {
		klog.Errorf("containerCtx nri done failed: %v", err)
		return nil, nil
	}

	klog.V(6).Infof("handle NRI UpdateContainer successfully, container %s/%s/%s",
		pod.GetNamespace(), pod.GetName(), container.GetName())
	return []*api.ContainerUpdate{update}, nil
}

func (p *NriServer) onClose() {
	p.stub.Stop()
	klog.V(6).Infof("NRI server closes")
}
