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

package agent

import (
	"fmt"
	"os"
	"time"

	topologyclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientset "k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	clientsetbeta1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/typed/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/config"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/prediction"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resmanager"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	statesinformerimpl "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/impl"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var (
	scheme = apiruntime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

type Daemon interface {
	Run(stopCh <-chan struct{})
}

type daemon struct {
	metricAdvisor  metricsadvisor.MetricAdvisor
	statesInformer statesinformer.StatesInformer
	metricCache    metriccache.MetricCache
	resManager     resmanager.ResManager
	qosManager     qosmanager.QoSManager
	runtimeHook    runtimehooks.RuntimeHook
	predictServer  prediction.PredictServer
}

func NewDaemon(config *config.Configuration) (Daemon, error) {
	// get node name
	nodeName := os.Getenv("NODE_NAME")
	if len(nodeName) == 0 {
		return nil, fmt.Errorf("failed to new daemon: NODE_NAME env is empty")
	}
	klog.Infof("NODE_NAME is %v, start time %v", nodeName, float64(time.Now().Unix()))
	metrics.RecordKoordletStartTime(nodeName, float64(time.Now().Unix()))

	klog.Infof("sysconf: %+v, agentMode: %v", system.Conf, system.AgentMode)
	klog.Infof("kernel version INFO: %+v", system.HostSystemInfo)

	kubeClient := clientset.NewForConfigOrDie(config.KubeRestConf)
	crdClient := clientsetbeta1.NewForConfigOrDie(config.KubeRestConf)
	topologyClient := topologyclientset.NewForConfigOrDie(config.KubeRestConf)
	schedulingClient := v1alpha1.NewForConfigOrDie(config.KubeRestConf)

	metricCache, err := metriccache.NewMetricCache(config.MetricCacheConf)
	if err != nil {
		return nil, err
	}
	predictServer := prediction.NewPeakPredictServer(config.PredictionConf)
	predictorFactory := prediction.NewPredictorFactory(predictServer, config.PredictionConf.PredictionColdStartDuration)

	statesInformer := statesinformerimpl.NewStatesInformer(config.StatesInformerConf, kubeClient, crdClient, topologyClient, metricCache, nodeName, schedulingClient, predictorFactory)
	collectorService := metricsadvisor.NewMetricAdvisor(config.CollectorConf, statesInformer, metricCache)

	evictVersion, err := util.FindSupportedEvictVersion(kubeClient)
	if err != nil {
		return nil, err
	}

	resManagerService := resmanager.NewResManager(config.ResManagerConf, scheme, kubeClient, crdClient, nodeName, statesInformer, metricCache, int64(config.CollectorConf.CollectResUsedInterval.Seconds()), evictVersion)

	qosManager := qosmanager.NewQosManager(config.QosManagerConf, scheme, kubeClient, nodeName, statesInformer, metricCache)

	runtimeHook, err := runtimehooks.NewRuntimeHook(statesInformer, config.RuntimeHookConf)
	if err != nil {
		return nil, err
	}

	d := &daemon{
		metricAdvisor:  collectorService,
		statesInformer: statesInformer,
		metricCache:    metricCache,
		resManager:     resManagerService,
		qosManager:     qosManager,
		runtimeHook:    runtimeHook,
		predictServer:  predictServer,
	}

	return d, nil
}

func (d *daemon) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	klog.Infof("Starting daemon")

	go func() {
		if err := d.metricCache.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the metric cache: ", err)
		}
	}()

	// start states informer
	go func() {
		if err := d.statesInformer.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the states informer: ", err)
		}
	}()
	// wait for metric advisor sync
	if !cache.WaitForCacheSync(stopCh, d.statesInformer.HasSynced) {
		klog.Fatalf("time out waiting for states informer to sync")
	}

	// start metric advisor
	go func() {
		if err := d.metricAdvisor.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the metric advisor: ", err)
		}
	}()

	// start predict server
	go func() {
		if err := d.predictServer.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the predict server: ", err)
		}
	}()

	// wait for metric advisor sync
	if !cache.WaitForCacheSync(stopCh, d.metricAdvisor.HasSynced) {
		klog.Fatalf("time out waiting for metric advisor to sync")
	}

	// start resmanager
	go func() {
		if err := d.resManager.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the resManager: ", err)
		}
	}()

	// start QoS Manager
	go func() {
		if err := d.qosManager.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the QoSManager: ", err)
		}
	}()

	go func() {
		if err := d.runtimeHook.Run(stopCh); err != nil {
			klog.Fatalf("Unable to run the runtimeHook: ", err)
		}
	}()

	klog.Info("Start daemon successfully")
	<-stopCh
	klog.Info("Shutting down daemon")
}
