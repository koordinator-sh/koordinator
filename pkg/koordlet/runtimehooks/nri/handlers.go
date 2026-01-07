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

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

func (p *NriServer) Configure(_ context.Context, config, runtime, version string) (stub.EventMask, error) {
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

	klog.V(4).Infof("handle NRI Configure successfully, config %s, runtime %s, version %s",
		config, runtime, version)
	return p.mask, nil
}

func (p *NriServer) Synchronize(_ context.Context, pods []*api.PodSandbox, containers []*api.Container) ([]*api.ContainerUpdate, error) {
	// todo: update existed containers configure
	return nil, nil
}

func (p *NriServer) RunPodSandbox(_ context.Context, pod *api.PodSandbox) error {
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

	klog.V(4).Infof("handle NRI RunPodSandbox successfully, pod %s/%s", pod.GetNamespace(), pod.GetName())
	return nil
}

func (p *NriServer) CreateContainer(_ context.Context, pod *api.PodSandbox, container *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
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

	klog.V(4).Infof("handle NRI CreateContainer successfully, container %s/%s/%s",
		pod.GetNamespace(), pod.GetName(), container.GetName())
	return adjust, nil, nil
}

func (p *NriServer) UpdateContainer(_ context.Context, pod *api.PodSandbox, container *api.Container, r *api.LinuxResources) ([]*api.ContainerUpdate, error) {
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

	klog.V(4).Infof("handle NRI UpdateContainer successfully, container %s/%s/%s",
		pod.GetNamespace(), pod.GetName(), container.GetName())
	return []*api.ContainerUpdate{update}, nil
}

func (p *NriServer) RemovePodSandbox(_ context.Context, pod *api.PodSandbox) error {
	podCtx := &protocol.PodContext{}
	podCtx.FromNri(pod)
	// todo: return error or bypass error based on PluginFailurePolicy
	err := hooks.RunHooks(p.options.PluginFailurePolicy, rmconfig.PreRemoveRunPodSandbox, podCtx)
	if err != nil {
		klog.Errorf("nri hooks run error: %v", err)
		if p.options.PluginFailurePolicy == rmconfig.PolicyFail {
			return err
		}
	}
	podCtx.NriRemoveDone(p.options.Executor)

	klog.V(4).Infof("handle NRI RemovePodSandbox successfully, pod %s/%s", pod.GetNamespace(), pod.GetName())
	return nil
}

func (p *NriServer) RemoveContainer(context.Context, *api.PodSandbox, *api.Container) error {
	// TODO
	return nil
}

func (p *NriServer) StopContainer(context.Context, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error) {
	// TODO
	return nil, nil
}

func (p *NriServer) StartContainer(context.Context, *api.PodSandbox, *api.Container) error {
	// TODO
	return nil
}

func (p *NriServer) StopPodSandbox(context.Context, *api.PodSandbox) error {
	// TODO
	return nil
}
