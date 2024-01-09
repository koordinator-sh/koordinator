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

package prom

import (
	"time"

	"go.uber.org/atomic"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

const CollectorName = "PromCollector"

// The collector is for collecting the metrics (prometheus format encoded) from external prometheus exporters.
// To simplify the implementation of the prometheus metrics collection, the collector allows multiple scrapers to
// register, and a scraper is responsible for scrape and decode the metrics from a prometheus exporter.
// A scraper collects from only one exporter and decode the raw metrics into the metrics classified by the metric
// level (node, pod, container, qos...).
type collector struct {
	appendableDB   metriccache.Appendable
	statesInformer statesinformer.StatesInformer
	rulePath       string
	started        *atomic.Bool
}

func New(opt *framework.Options) framework.Collector {
	return &collector{
		appendableDB:   opt.MetricCache,
		statesInformer: opt.StatesInformer,
		rulePath:       opt.Config.CollectPromMetricRulePath,
		started:        atomic.NewBool(false),
	}
}

func (c *collector) Enabled() bool {
	return len(c.rulePath) > 0
}

func (c *collector) Setup(s *framework.Context) {}

func (c *collector) Run(stopCh <-chan struct{}) {
	if !cache.WaitForCacheSync(stopCh, c.statesInformer.HasSynced) {
		// exit when statesInformer sync failed
		metrics.RecordModuleHealthyStatus(metrics.ModuleMetricsAdvisor, CollectorName, false)
		klog.Fatalf("timed out waiting for states informer caches to sync")
	}
	c.run(stopCh)
}

func (c *collector) Started() bool {
	return c.started.Load()
}

func (c *collector) run(stopCh <-chan struct{}) {
	// load rules from the rule and generate scraper configs
	rule, err := LoadCollectRuleFromFile(c.rulePath)
	if err != nil {
		klog.Warningf("load collect rule failed, file %s, err: %w", c.rulePath, err)
		metrics.RecordModuleHealthyStatus(metrics.ModuleMetricsAdvisor, CollectorName, false)
		return
	}
	configs, err := rule.ToConfigs()
	if err != nil {
		klog.Warningf("convert collect rule to config failed, rule %+v, err: %w", rule, err)
		metrics.RecordModuleHealthyStatus(metrics.ModuleMetricsAdvisor, CollectorName, false)
		return
	}

	// init scrapers
	scrapersWithInterval := map[time.Duration][]Scraper{}
	for _, cfg := range configs {
		s, err := NewGenericScraper(cfg)
		if err != nil {
			klog.Warningf("failed to init prom scraper %+v, err: %s", s, err)
			continue
		}

		klog.V(4).Infof("init prom scraper %s successfully", s.Name())
		_, ok := scrapersWithInterval[cfg.ScrapeInterval]
		if !ok {
			scrapersWithInterval[cfg.ScrapeInterval] = []Scraper{s}
		} else {
			scrapersWithInterval[cfg.ScrapeInterval] = append(scrapersWithInterval[cfg.ScrapeInterval], s)
		}
	}

	for interval := range scrapersWithInterval {
		scrapers := scrapersWithInterval[interval]
		go wait.Until(func() {
			c.runScrapers(scrapers...)
		}, interval, stopCh)
		klog.V(5).Infof("run scrapers at interval %v, num %d", interval, len(scrapers))
	}

	klog.V(4).Infof("start prom collector, scrapers num %d", len(scrapersWithInterval))
}

func (c *collector) runScrapers(scrapers ...Scraper) {
	node := c.statesInformer.GetNode()
	if node == nil {
		metrics.RecordModuleHealthyStatus(metrics.ModuleMetricsAdvisor, CollectorName, false)
		klog.V(4).Infof("failed to run prom collector, err: no node found")
		return
	}
	podMetas := c.statesInformer.GetAllPods()

	count := 0
	for _, s := range scrapers {
		scraperName := s.Name()
		metricSamples, err := s.GetMetricSamples(node, podMetas)
		if err != nil {
			metrics.RecordCollectPromMetricsStatus(scraperName, false)
			klog.V(4).Infof("failed to scrape prom samples by scraper %s, err: %s", scraperName, err)
			continue
		}
		metrics.RecordCollectPromMetricsStatus(scraperName, true)
		klog.V(6).Infof("scrape prom metrics finished by scraper %s, metrics num %d", scraperName, len(metricSamples))

		appender := c.appendableDB.Appender()
		if err = appender.Append(metricSamples); err != nil {
			klog.V(4).Infof("failed to append metrics for scraper %s, samples %+v, err: %s", scraperName, metricSamples, err)
			continue
		}
		klog.V(6).Infof("append metric samples finished for scraper %s, metrics num %d", scraperName, len(metricSamples))

		if err = appender.Commit(); err != nil {
			klog.V(4).Infof("failed to commit metrics for scraper %s, err: %s", scraperName, err)
			continue
		}

		count += len(metricSamples)
	}

	klog.V(4).Infof("prom collector scrapes metrics finished, scraper num %d, metrics num %d", len(scrapers), count)
	c.started.Store(true)
	metrics.RecordModuleHealthyStatus(metrics.ModuleMetricsAdvisor, CollectorName, true)
}
