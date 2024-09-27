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
	"time"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"go.uber.org/atomic"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
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
	NriPluginName     string
	NriPluginIdx      string
	NriSocketPath     string
	NriConnectTimeout time.Duration
	// support stop running other hooks once someone failed
	PluginFailurePolicy rmconfig.FailurePolicyType
	// todo: add support for disable stages
	DisableStages map[string]struct{}
	Executor      resourceexecutor.ResourceUpdateExecutor
	BackOff       wait.Backoff
	EventRecorder record.EventRecorder
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
	cfg      nriConfig
	stub     stub.Stub
	mask     stub.EventMask
	options  Options       // server options
	stubOpts []stub.Option // nri stub options
	stopped  *atomic.Bool  // if false, the stub will try to reconnect when stub.OnClose is invoked
}

const (
	events = "RunPodSandbox,RemovePodSandbox,CreateContainer,UpdateContainer"
)

var (
	_ = stub.ConfigureInterface(&NriServer{})
	_ = stub.SynchronizeInterface(&NriServer{})
	_ = stub.RunPodInterface(&NriServer{})
	_ = stub.RemovePodInterface(&NriServer{})
	_ = stub.CreateContainerInterface(&NriServer{})
	_ = stub.UpdateContainerInterface(&NriServer{})
)

func NewNriServer(opt Options) (*NriServer, error) {
	err := opt.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate nri server, err: %w", err)
	}

	stubOpts := []stub.Option{
		stub.WithPluginName(opt.NriPluginName),
		stub.WithPluginIdx(opt.NriPluginIdx),
		stub.WithSocketPath(filepath.Join(system.Conf.VarRunRootDir, opt.NriSocketPath)),
	}
	p := &NriServer{
		options:  opt,
		stubOpts: stubOpts,
		stopped:  atomic.NewBool(false),
	}
	if p.mask, err = api.ParseEventMask(events); err != nil {
		klog.Errorf("failed to parse events %v", err)
		return p, err
	}
	p.cfg.Events = strings.Split(events, ",")

	if p.stub, err = stub.New(p, append(p.stubOpts, stub.WithOnClose(p.onClose))...); err != nil {
		klog.Errorf("failed to create plugin stub: %v", err)
		return nil, err
	}

	return p, nil
}

func (p *NriServer) Start() error {
	err := p.options.Validate()
	if err != nil {
		return err
	}
	success := time.After(p.options.NriConnectTimeout)
	errorChan := make(chan error)

	go func(chan error) {
		if p.stub != nil {
			err := p.stub.Run(context.Background())
			if err != nil {
				klog.Errorf("nri server exited with error: %v", err)
				errorChan <- err
			} else {
				klog.V(4).Info("nri server started")
			}
		} else {
			err := fmt.Errorf("nri stub is nil")
			errorChan <- err
		}
	}(errorChan)

	select {
	case <-success:
		return nil
	case <-errorChan:
		return fmt.Errorf("nri start fail, err: %w", err)
	}
}

func (p *NriServer) Stop() {
	p.stopped.Store(true)
	p.stub.Stop()
}

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

	klog.V(6).Infof("handle NRI Configure successfully, config %s, runtime %s, version %s",
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

	klog.V(6).Infof("handle NRI RunPodSandbox successfully, pod %s/%s", pod.GetNamespace(), pod.GetName())
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

	klog.V(6).Infof("handle NRI CreateContainer successfully, container %s/%s/%s",
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

	klog.V(6).Infof("handle NRI UpdateContainer successfully, container %s/%s/%s",
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

	klog.V(6).Infof("handle NRI RemovePodSandbox successfully, pod %s/%s", pod.GetNamespace(), pod.GetName())
	return nil
}

func (p *NriServer) onClose() {
	//TODO: consider the pod status during restart
	retryFunc := func() (bool, error) {
		if p.stopped.Load() { // if set to stopped, no longer reconnect
			return true, nil
		}

		newStub, err := stub.New(p, append(p.stubOpts, stub.WithOnClose(p.onClose))...)
		if err != nil {
			klog.Errorf("failed to create plugin stub: %v", err)
			return false, nil
		}

		p.stub = newStub
		err = p.Start()
		if err != nil {
			completeNriSocketPath := filepath.Join(system.Conf.VarRunRootDir, p.options.NriSocketPath)
			targetErr := fmt.Errorf("nri socket path %q does not exist", completeNriSocketPath)
			if err.Error() == targetErr.Error() {
				return false, err
			}
			//TODO: check the error type, if nri server disable nri, we should also break backoff
			klog.Warningf("nri reconnect failed, err: %s", err)
			return false, nil
		} else {
			klog.V(4).Info("nri server restart success")
			return true, nil
		}
	}

	// TODO: high version wait not support BackoffUntil with BackOffManger as parameters, when updated to v0.27.0 version wait, we can refine ExponentialBackoff.
	err := wait.ExponentialBackoff(p.options.BackOff, retryFunc)
	if err != nil {
		klog.Errorf("nri server restart failed after several times retry")
	}
}
