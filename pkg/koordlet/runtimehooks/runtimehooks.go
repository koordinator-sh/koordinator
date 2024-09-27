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

package runtimehooks

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/nri"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/proxyserver"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

type HookPlugin interface {
	Register(op hooks.Options)
}

type RuntimeHook interface {
	Run(stopCh <-chan struct{}) error
}

type runtimeHook struct {
	statesInformer    statesinformer.StatesInformer
	server            proxyserver.Server
	nriServer         *nri.NriServer
	reconciler        reconciler.Reconciler
	hostAppReconciler reconciler.Reconciler
	reader            resourceexecutor.CgroupReader
	executor          resourceexecutor.ResourceUpdateExecutor
}

func (r *runtimeHook) Run(stopCh <-chan struct{}) error {
	klog.V(5).Infof("runtime hook server start running")
	go r.executor.Run(stopCh)
	if err := r.server.Start(); err != nil {
		return err
	}
	if r.nriServer != nil {
		go func() {
			if err := r.nriServer.Start(); err != nil {
				// if NRI is not enabled or container runtime not support NRI, we just skip NRI server start
				klog.Warningf("nri mode runtime hook server start failed: %v", err)
			} else {
				klog.V(4).Infof("nri mode runtime hook server has started")
			}
		}()
	}
	if err := r.reconciler.Run(stopCh); err != nil {
		return err
	}
	if err := r.hostAppReconciler.Run(stopCh); err != nil {
		return err
	}
	if err := r.server.Register(); err != nil {
		return err
	}
	klog.V(5).Infof("runtime hook server has started")
	<-stopCh
	klog.Infof("runtime hook is stopped")
	return nil
}

func NewRuntimeHook(si statesinformer.StatesInformer, cfg *Config, schema *apiruntime.Scheme, kubeClient clientset.Interface, nodeName string) (RuntimeHook, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&clientcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(schema, corev1.EventSource{Component: "koordlet-runtimehook", Host: nodeName})
	failurePolicy, err := config.GetFailurePolicyType(cfg.RuntimeHooksFailurePolicy)
	if err != nil {
		return nil, err
	}
	pluginFailurePolicy, err := config.GetFailurePolicyType(cfg.RuntimeHooksPluginFailurePolicy)
	if err != nil {
		return nil, err
	}
	cr := resourceexecutor.NewCgroupReader()
	e := resourceexecutor.NewResourceUpdateExecutor()
	newServerOptions := proxyserver.Options{
		Network:             cfg.RuntimeHooksNetwork,
		Address:             cfg.RuntimeHooksAddr,
		HostEndpoint:        cfg.RuntimeHookHostEndpoint,
		FailurePolicy:       failurePolicy,
		PluginFailurePolicy: pluginFailurePolicy,
		ConfigFilePath:      cfg.RuntimeHookConfigFilePath,
		DisableStages:       getDisableStagesMap(cfg.RuntimeHookDisableStages),
		Executor:            e,
		EventRecorder:       recorder,
	}

	backOff := wait.Backoff{
		Duration: cfg.RuntimeHooksNRIBackOffDuration,
		Factor:   cfg.RuntimeHooksNRIBackOffFactor,
		Jitter:   0.1,
		Steps:    cfg.RuntimeHooksNRIBackOffSteps,
		Cap:      cfg.RuntimeHooksNRIBackOffCap,
	}
	var nriServer *nri.NriServer
	if cfg.RuntimeHooksNRI {
		nriServerOptions := nri.Options{
			NriPluginName:       cfg.RuntimeHooksNRIPluginName,
			NriPluginIdx:        cfg.RuntimeHooksNRIPluginIndex,
			NriSocketPath:       cfg.RuntimeHooksNRISocketPath,
			NriConnectTimeout:   cfg.RuntimeHooksNRIConnectTimeout,
			PluginFailurePolicy: pluginFailurePolicy,
			DisableStages:       getDisableStagesMap(cfg.RuntimeHookDisableStages),
			Executor:            e,
			BackOff:             backOff,
			EventRecorder:       recorder,
		}
		nriServer, err = nri.NewNriServer(nriServerOptions)
		if err != nil {
			klog.Warningf("new nri mode runtimehooks server error: %v", err)
		}
	} else {
		klog.V(4).Info("nri mode runtimehooks is disabled")
	}

	s, err := proxyserver.NewServer(newServerOptions)
	newReconcilerCtx := reconciler.Context{
		StatesInformer:    si,
		Executor:          e,
		ReconcileInterval: cfg.RuntimeHookReconcileInterval,
		EventRecorder:     recorder,
	}

	newPluginOptions := hooks.Options{
		Reader:         cr,
		Executor:       e,
		StatesInformer: si,
		EventRecorder:  recorder,
	}

	if err != nil {
		return nil, err
	}
	r := &runtimeHook{
		statesInformer:    si,
		server:            s,
		nriServer:         nriServer,
		reconciler:        reconciler.NewReconciler(newReconcilerCtx),
		hostAppReconciler: reconciler.NewHostAppReconciler(newReconcilerCtx),
		reader:            cr,
		executor:          e,
	}
	registerPlugins(newPluginOptions)
	si.RegisterCallbacks(statesinformer.RegisterTypeNodeSLOSpec, "runtime-hooks-rule-node-slo",
		"Update hooks rule can run callbacks if NodeSLO spec update",
		rule.UpdateRules)
	si.RegisterCallbacks(statesinformer.RegisterTypeNodeTopology, "runtime-hooks-rule-node-topo",
		"Update hooks rule if NodeTopology info update",
		rule.UpdateRules)
	si.RegisterCallbacks(statesinformer.RegisterTypeNodeMetadata, "runtime-hooks-rule-node-metadata",
		"Update hooks rule if Node metadata update",
		rule.UpdateRules)
	si.RegisterCallbacks(statesinformer.RegisterTypeAllPods, "runtime-hooks-rule-all-pods",
		"Update hooks rule of all Pods refresh", rule.UpdateRules)
	if err := s.Setup(); err != nil {
		return nil, fmt.Errorf("failed to setup runtime hook server, error %v", err)
	}
	return r, nil
}

func registerPlugins(op hooks.Options) {
	klog.V(5).Infof("start register plugins for runtime hook")
	for hookFeature, hookPlugin := range runtimeHookPlugins {
		enabled := features.DefaultKoordletFeatureGate.Enabled(hookFeature)
		if enabled {
			hookPlugin.Register(op)
		}
		klog.Infof("runtime hook plugin %s enable %v", hookFeature, enabled)
	}
}

func getDisableStagesMap(stagesSlice []string) map[string]struct{} {
	stagesMap := map[string]struct{}{}
	for _, item := range stagesSlice {
		if _, ok := stagesMap[item]; !ok {
			stagesMap[item] = struct{}{}
		}
	}
	return stagesMap
}
