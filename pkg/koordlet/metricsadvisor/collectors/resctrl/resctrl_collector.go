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

package resctrl

import (
	"time"

	"go.uber.org/atomic"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/resctrl"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	CollectorName = "resctrlCollector"
)

type resctrlCollector struct {
	collectInterval      time.Duration
	started              *atomic.Bool
	metricCache          metriccache.MetricCache
	statesInformer       statesinformer.StatesInformer
	resctrlReader        resourceexecutor.ResctrlReader
	resctrlCollectorGate bool
}

func New(opt *framework.Options) framework.Collector {
	return &resctrlCollector{
		collectInterval: opt.Config.ResctrlCollectorInterval,
		statesInformer:  opt.StatesInformer,
		metricCache:     opt.MetricCache,
		resctrlReader:   opt.ResctrlReader,
		started:         atomic.NewBool(false),
	}
}

func (r *resctrlCollector) Enabled() bool {
	return r.resctrlCollectorGate
}

func (r *resctrlCollector) Setup(c *framework.Context) {}

func (r *resctrlCollector) Started() bool {
	return r.started.Load()
}

func (r *resctrlCollector) Run(stopCh <-chan struct{}) {
	go wait.Until(r.collectQoSResctrlStat, r.collectInterval, stopCh)
}

func (r *resctrlCollector) collectQoSResctrlStat() {
	err := system.CheckResctrlSchemataValid()
	if err != nil {
		// use resctrl/schemata to check mount state TODO: use another method to check
		klog.V(4).Infof("cannot find resctrl file system schemata, error: %v", err)
		return
	}
	klog.V(6).Info("start collect QoS resctrl Stat")
	resctrlMetrics := make([]metriccache.MetricSample, 0)
	collectTime := time.Now()
	for _, qos := range []string{
		resctrl.LSRResctrlGroup,
		resctrl.LSResctrlGroup,
		resctrl.BEResctrlGroup} {
		l3Map, err := r.resctrlReader.ReadResctrlL3Stat(qos)
		if err != nil {
			klog.V(4).Infof("collect QoS %s resctrl llc data error: %v", qos, err)
			continue
		}
		for cacheId, value := range l3Map {
			metrics.RecordQosResctrl(metrics.ResourceTypeLLC, int(cacheId), qos, "", value)
			llcSample, err := metriccache.QosResctrl.GenerateSample(metriccache.MetricPropertiesFunc.QosResctrl(qos, int(cacheId), metrics.ResourceTypeLLC, ""), collectTime, float64(value))
			if err != nil {
				klog.Warningf("generate QoS %s resctrl llc sample error: %v", qos, err)
			}
			resctrlMetrics = append(resctrlMetrics, llcSample)
		}
		mbMap, err := r.resctrlReader.ReadResctrlMBStat(qos)
		if err != nil {
			klog.V(4).Infof("collect QoS %s resctrl mb data error: %v", qos, err)
			continue
		}
		for cacheId, value := range mbMap {
			for mbType, mbValue := range value {
				metrics.RecordQosResctrl(metrics.ResourceTypeMB, int(cacheId), qos, mbType, mbValue)
				mbSample, err := metriccache.QosResctrl.GenerateSample(metriccache.MetricPropertiesFunc.QosResctrl(qos, int(cacheId), metrics.ResourceTypeMB, mbType), collectTime, float64(mbValue))
				if err != nil {
					klog.V(4).Infof("generate QoS %s resctrl mb sample error: %v", qos, err)
				}
				resctrlMetrics = append(resctrlMetrics, mbSample)
			}
		}
	}

	// save QoS resctrl data to tsdb
	r.saveMetric(resctrlMetrics)

	r.started.Store(true)
	klog.V(6).Infof("collect resctrl data at %s", time.Now())
}

func (r *resctrlCollector) saveMetric(samples []metriccache.MetricSample) error {
	if len(samples) == 0 {
		return nil
	}

	appender := r.metricCache.Appender()
	if err := appender.Append(samples); err != nil {
		klog.ErrorS(err, "Append container metrics error")
		return err
	}

	if err := appender.Commit(); err != nil {
		klog.ErrorS(err, "Commit container metrics failed")
		return err
	}

	return nil
}
