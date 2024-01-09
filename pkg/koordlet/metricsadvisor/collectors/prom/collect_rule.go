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
	"fmt"
	"os"
	"time"

	"github.com/prometheus/common/model"
	"gopkg.in/yaml.v2"
	"k8s.io/klog/v2"
)

const (
	DefaultScrapeInterval   = 30 * time.Second
	DefaultScrapeTimeout    = 10 * time.Second
	MinValidMetricTimeRange = 2 * time.Second
)

// CollectRule is the collecting rule of the scrapers.
// It defines whether the global scraper is enabled and the jobs to scrape.
/**
# Example
config:
  enable: true
  jobs:
  - name: job1
    enable: true
    address: "127.0.0.1"
    port: 9317
    path: /metrics
    scrapeInterval: 10s
    scrapeTimeout: 5s
    metrics:
    - sourceName: metric1
      sourceLabels:
        label1: value1
      targetName: metric1
      targetType: node
    - sourceName: metric2
      targetName: metricA
      targetType: pod
      targetLabels:
        pod: pod_name
        namespace: pod_namespace
  - name: job2
    enable: true
    address: "127.0.0.1"
    port: 9316
    path: /metrics
    scrapeInterval: 30s
    scrapeTimeout: 10s
    metrics:
    - sourceName: metric3
      targetName: metric3
      targetType: node
  - name: job3
    enable: false
    address: "127.0.0.1"
    port: 9090
    path: /metrics
    scrapeInterval: 30s
    scrapeTimeout: 10s
    metrics:
    - sourceName: metric3
      targetName: metric3
      targetType: node
**/
type CollectRule struct {
	Enable bool      `json:"enable,omitempty"`
	Jobs   []JobRule `json:"jobs,omitempty"`
}

// JobRule is the rule of the job.
// It defines the job name, whether the job is enabled, the job address, port, path, scrape interval, scrape timeout,
// and the metrics to be scraped.
type JobRule struct {
	Name           string        `yaml:"name,omitempty" json:"name,omitempty"`
	Enable         bool          `yaml:"enable,omitempty" json:"enable,omitempty"`
	Address        string        `yaml:"address,omitempty" json:"address,omitempty"`
	Port           int           `yaml:"port,omitempty" json:"port,omitempty"`
	Path           string        `yaml:"path,omitempty" json:"path,omitempty"`
	ScrapeInterval time.Duration `yaml:"scrapeInterval,omitempty" json:"scrapeInterval,omitempty"`
	ScrapeTimeout  time.Duration `yaml:"scrapeTimeout,omitempty" json:"scrapeTimeout,omitempty"`
	Metrics        []MetricRule  `yaml:"metrics,omitempty" json:"metrics,omitempty"`
}

// MetricRule is the rule of the metric.
// It defines the name of the source metric to be scraped, the matched labels of the source metric, the name of the
// target metric to be stored into the metric cache and the target labels to identify the metric.
type MetricRule struct {
	SourceName   string         `yaml:"sourceName,omitempty" json:"sourceName,omitempty"`
	SourceLabels model.LabelSet `yaml:"sourceLabels,omitempty" json:"sourceLabels,omitempty"`
	TargetName   string         `yaml:"targetName,omitempty" json:"targetName,omitempty"`
	TargetType   MetricType     `yaml:"targetType,omitempty" json:"targetType,omitempty"`
	TargetLabels model.LabelSet `yaml:"targetLabels,omitempty" json:"targetLabels,omitempty"`
}

func LoadCollectRuleFromFile(path string) (*CollectRule, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat file, err: %w", err)
	}
	ruleBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file, err: %w", err)
	}

	rule := &CollectRule{}
	err = yaml.Unmarshal(ruleBytes, rule)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal file, content %s, err: %w", string(ruleBytes), err)
	}

	return rule, nil
}

func (c *CollectRule) IsEnabled() bool {
	return c.Enable
}

func (c *CollectRule) Validate() error {
	if len(c.Jobs) <= 0 {
		return fmt.Errorf("no job config")
	}

	jobMap := map[string]*JobRule{} // job name should be unique
	m := map[string]*MetricRule{}   // target metric name should be unique

	for i := range c.Jobs {
		job := &c.Jobs[i]
		if len(job.Name) <= 0 {
			return fmt.Errorf("job name is empty, job index %d", i)
		}
		_, ok := jobMap[job.Name]
		if ok {
			return fmt.Errorf("duplicate job name %s, job index %d", job.Name, i)
		}
		if len(job.Address) <= 0 || job.Port <= 0 {
			return fmt.Errorf("job address or port is empty, job %s", job.Name)
		}

		for j := range job.Metrics {
			metric := &job.Metrics[j]
			if len(metric.TargetName) <= 0 {
				return fmt.Errorf("metric target name is empty, job %s, metric index %d", job.Name, j)
			}
			_, ok = m[metric.TargetName]
			if ok {
				return fmt.Errorf("duplicate metric target name %s, job %s, metric index %d", metric.TargetName, job.Name, j)
			}
			if err := IsValidMetricRule(metric); err != nil {
				return fmt.Errorf("invalid metric reason %s, job %s, target metric %s", err, job.Name, metric.TargetName)
			}

			m[metric.TargetName] = metric
		}

		jobMap[job.Name] = job
	}

	return nil
}

func (c *CollectRule) ToConfigs() ([]*ScraperConfig, error) {
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("validate failed, err: %w", err)
	}

	if !c.IsEnabled() {
		klog.V(4).Infof("ScrapeConfig is disabled, skip init scrapers")
		return nil, nil
	}

	var scrapeConfigs []*ScraperConfig
	for _, job := range c.Jobs {
		scrapeConfig := &ScraperConfig{
			Enable:         job.Enable,
			JobName:        job.Name,
			ScrapeInterval: job.ScrapeInterval,
			Client: &ClientConfig{
				Timeout:     job.ScrapeTimeout,
				InsecureTLS: true,
				Address:     job.Address,
				Port:        job.Port,
				Path:        job.Path,
			},
		}
		if scrapeConfig.ScrapeInterval <= 0 {
			scrapeConfig.ScrapeInterval = DefaultScrapeInterval
		}
		if scrapeConfig.Client.Timeout <= 0 {
			scrapeConfig.Client.Timeout = DefaultScrapeTimeout
		}
		validTimeRange := scrapeConfig.ScrapeInterval / 2
		if validTimeRange < MinValidMetricTimeRange {
			validTimeRange = MinValidMetricTimeRange
		}

		var subDecoderConfigs []*DecodeConfig
		for _, metric := range job.Metrics {
			decoderConfig := &DecodeConfig{
				TargetMetric:   metric.TargetName,
				TargetType:     metric.TargetType,
				TargetLabels:   metric.TargetLabels,
				SourceMetric:   metric.SourceName,
				SourceLabels:   metric.SourceLabels,
				ValidTimeRange: validTimeRange,
			}
			subDecoderConfigs = append(subDecoderConfigs, decoderConfig)
		}
		scrapeConfig.Decoders = subDecoderConfigs

		scrapeConfigs = append(scrapeConfigs, scrapeConfig)
	}

	return scrapeConfigs, nil
}
