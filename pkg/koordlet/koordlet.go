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

	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	clientsetbeta1 "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/config"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/pleg"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/reporter"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resmanager"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
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
	collector      metricsadvisor.Collector
	statesInformer statesinformer.StatesInformer
	metricCache    metriccache.MetricCache
	reporter       reporter.Reporter
	resManager     resmanager.ResManager
	runtimeHook    runtimehooks.RuntimeHook
}

func NewDaemon(config *config.Configuration) (Daemon, error) {
	// get node name
	nodeName := os.Getenv("NODE_NAME")
	if len(nodeName) == 0 {
		return nil, fmt.Errorf("failed to new daemon: NODE_NAME env is empty")
	}
	klog.Infof("NODE_NAME is %v,start time %v", nodeName, float64(time.Now().Unix()))
	metrics.RecordKoordletStartTime(nodeName, float64(time.Now().Unix()))

	klog.Infof("sysconf: %+v,agentMode:%v", system.Conf, system.AgentMode)
	klog.Infof("kernel version INFO : %+v", system.HostSystemInfo)

	// setup cgroup path formatter from cgroup driver type
	var detectCgroupDriver system.CgroupDriverType
	if pollErr := wait.PollImmediate(time.Second*10, time.Minute, func() (bool, error) {
		driver := system.GuessCgroupDriverFromCgroupName()
		if driver.Validate() {
			detectCgroupDriver = driver
			return true, nil
		}
		klog.Infof("can not detect cgroup driver from 'kubepods' cgroup name")

		if driver, err := system.GuessCgroupDriverFromKubelet(); err == nil && driver.Validate() {
			detectCgroupDriver = driver
			return true, nil
		} else {
			klog.Errorf("guess kubelet cgroup driver failed, retry...: %v", err)
			return false, nil
		}
	}); pollErr != nil {
		return nil, fmt.Errorf("can not detect kubelet cgroup driver: %v", pollErr)
	}
	system.SetupCgroupPathFormatter(detectCgroupDriver)
	klog.Infof("Node %s use '%s' as cgroup driver", nodeName, string(detectCgroupDriver))

	kubeClient := clientset.NewForConfigOrDie(config.KubeRestConf)
	crdClient := clientsetbeta1.NewForConfigOrDie(config.KubeRestConf)

	pleg, err := pleg.NewPLEG(system.Conf.CgroupRootDir)
	if err != nil {
		return nil, err
	}

	statesInformer := statesinformer.NewStatesInformer(config.StatesInformerConf, kubeClient, crdClient, pleg, nodeName)
	metricCache, err := metriccache.NewMetricCache(config.MetricCacheConf)
	if err != nil {
		return nil, err
	}

	collectorService := metricsadvisor.NewCollector(config.CollectorConf, statesInformer, metricCache)
	reporterService := reporter.NewReporter(config.ReporterConf, kubeClient, crdClient, nodeName, metricCache, statesInformer)

	resManagerService := resmanager.NewResManager(config.ResManagerConf, scheme, kubeClient, crdClient, nodeName, statesInformer, metricCache, int64(config.CollectorConf.CollectResUsedIntervalSeconds))

	runtimeHook, err := runtimehooks.NewRuntimeHook(statesInformer, config.RuntimeHookConf)
	if err != nil {
		return nil, err
	}

	d := &daemon{
		collector:      collectorService,
		statesInformer: statesInformer,
		metricCache:    metricCache,
		reporter:       reporterService,
		resManager:     resManagerService,
		runtimeHook:    runtimeHook,
	}

	return d, nil
}

func (d *daemon) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	klog.Infof("Starting daemon")

	go func() {
		if err := d.metricCache.Run(stopCh); err != nil {
			klog.Error("Unable to run the metric cache: ", err)
			os.Exit(1)
		}
	}()

	// start meta service
	go func() {
		if err := d.statesInformer.Run(stopCh); err != nil {
			klog.Error("Unable to run the meta service: ", err)
			os.Exit(1)
		}
	}()

	// start collector
	go func() {
		if err := d.collector.Run(stopCh); err != nil {
			klog.Error("Unable to run the collector: ", err)
			os.Exit(1)
		}
	}()

	// wait for collector sync
	if !cache.WaitForCacheSync(stopCh, d.collector.HasSynced) {
		klog.Error("time out waiting for collector to sync")
		os.Exit(1)
	}

	// start reporter
	go func() {
		if err := d.reporter.Run(stopCh); err != nil {
			klog.Error("Unable to run the reporter: ", err)
			os.Exit(1)
		}
	}()

	// start resmanager
	go func() {
		if err := d.resManager.Run(stopCh); err != nil {
			klog.Error("Unable to run the resManager: ", err)
			os.Exit(1)
		}
	}()

	go func() {
		if err := d.runtimeHook.Run(stopCh); err != nil {
			klog.Errorf("Unable to run the runtimeHook: ", err)
			os.Exit(1)
		}
	}()

	klog.Info("Start daemon successfully")
	<-stopCh
	klog.Info("Shutting down daemon")
}
