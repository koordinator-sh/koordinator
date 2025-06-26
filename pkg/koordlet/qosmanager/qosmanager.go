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

package qosmanager

import (
	"fmt"
	"time"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers/copilot"

	corev1 "k8s.io/api/core/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientset "k8s.io/client-go/kubernetes"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	koordclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	_ "github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	ma "github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type QOSManager interface {
	Run(stopCh <-chan struct{}) error
}

type qosManager struct {
	options *framework.Options
	context *framework.Context
}

func NewQOSManager(cfg *framework.Config, schema *apiruntime.Scheme, kubeClient clientset.Interface, crdClient *koordclientset.Clientset, nodeName string,
	statesInformer statesinformer.StatesInformer, metricCache metriccache.MetricCache, metricAdvisorConfig *ma.Config, evictVersion string) QOSManager {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&clientcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(schema, corev1.EventSource{Component: "koordlet-qosManager", Host: nodeName})
	cgroupReader := resourceexecutor.NewCgroupReader()
	evictor := framework.NewEvictor(kubeClient, recorder, evictVersion)
	var copilotAgent *copilot.CopilotAgent
	if cfg.EvictByCopilotAgent {
		copilotAgent = copilot.NewCopilotAgent(cfg.EvictByCopilotEndPoint)
	}

	opt := &framework.Options{
		CgroupReader:        cgroupReader,
		StatesInformer:      statesInformer,
		MetricCache:         metricCache,
		EventRecorder:       recorder,
		KubeClient:          kubeClient,
		EvictVersion:        evictVersion,
		Config:              cfg,
		MetricAdvisorConfig: metricAdvisorConfig,
		CopilotAgent:        copilotAgent,
	}

	ctx := &framework.Context{
		Evictor:    evictor,
		Strategies: make(map[string]framework.QOSStrategy, len(plugins.StrategyPlugins)),
	}

	for name, strategyFn := range plugins.StrategyPlugins {
		ctx.Strategies[name] = strategyFn(opt)
	}

	r := &qosManager{
		options: opt,
		context: ctx,
	}
	return r
}

func (r *qosManager) setup() {
	for _, s := range r.context.Strategies {
		s.Setup(r.context)
	}
}

func (r *qosManager) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	// minimum interval is one second.
	if r.options.MetricAdvisorConfig.CollectResUsedInterval < time.Second {
		klog.Infof("collectResUsedIntervalSeconds is %v, qos manager is disabled",
			r.options.MetricAdvisorConfig.CollectResUsedInterval)
		return nil
	}

	klog.Info("Starting qos manager")
	r.setup()

	err := r.context.Evictor.Start(stopCh)
	if err != nil {
		klog.Fatal("start evictor failed %v", err)
	}

	go framework.RunQOSGreyCtrlPlugins(r.options.KubeClient, stopCh)

	if !cache.WaitForCacheSync(stopCh, r.options.StatesInformer.HasSynced) {
		return fmt.Errorf("time out waiting for states informer caches to sync")
	}

	for name, strategy := range r.context.Strategies {
		klog.V(4).Infof("ready to start qos strategy %v", name)
		if !strategy.Enabled() {
			klog.V(4).Infof("qos strategy %v is not enabled, skip running", name)
			continue
		}
		strategy.Run(stopCh)
		klog.V(4).Infof("qos strategy %v start", name)
	}

	klog.Infof("start qos manager extensions")
	framework.SetupPlugins(r.options.KubeClient, r.options.MetricCache, r.options.StatesInformer)
	utilruntime.Must(framework.StartPlugins(r.options.Config.QOSExtensionCfg, stopCh))

	klog.Info("Starting qosManager successfully")
	<-stopCh
	klog.Info("shutting down qosManager")
	return nil
}
