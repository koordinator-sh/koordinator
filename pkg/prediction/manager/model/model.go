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

package model

import (
	"fmt"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/apis"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/checkpoint"
	"github.com/koordinator-sh/koordinator/pkg/prediction/manager/metricscollector"
	"github.com/koordinator-sh/koordinator/pkg/util/histogram"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"math"
	"time"
)

type ModelKey interface {
	Key() string
}

type Model interface {
	ModelKey
	LoadHistoryIfNecessary(snapshot checkpoint.Snapshot) (bool, error)
}

type MetricDistributionModel interface {
	Model
	Update() error
	PrepareFeeder(f *Feeder)
	FeedSamples() error
	SaveSnapshot(snapshot checkpoint.Snapshot) error
	GetDistribution(detail *apis.MetricDistribution) error
}

type DistributionModelBuilder struct {
}

func (b *DistributionModelBuilder) Build() (MetricDistributionModel, error) {
	m := &metricDistributionModelImpl{}

	return m, nil
}

const (
	MinSampleWeight = 0.1
)

var (
	defaultQuantileMap = map[string]float64{
		"p60": 0.5,
		"p95": 0.9,
		"p99": 0.99,
	}
)

var _ MetricDistributionModel = &metricDistributionModelImpl{}

type metricDistributionModelImpl struct {
	id                apis.MetricIdentifier
	histogram         histogram.Histogram
	FirstSampleStart  time.Time
	LastSampleStart   time.Time
	TotalSamplesCount int64
	CreationTime      time.Time

	feedSamples func() ([]metricscollector.Sample, error)
}

func (m *metricDistributionModelImpl) Key() string {
	return string(m.id.Source) + "/" + m.id.Name
}

func (m *metricDistributionModelImpl) Update() (bool, error) {
	return false, nil
}

func (m *metricDistributionModelImpl) PrepareFeeder(f *Feeder) {
	m.feedSamples = f.LoadSamples
}

func (m *metricDistributionModelImpl) FeedSamples() error {
	samples, err := m.feedSamples()
	if err != nil {
		return err
	}
	for _, s := range samples {
		t, v := s.Value()
		// TODO: weight for different model args
		m.histogram.AddSample(v, math.Min(v, MinSampleWeight), t)
		if t.After(m.LastSampleStart) {
			m.LastSampleStart = t
		}
		if m.FirstSampleStart.IsZero() || t.Before(m.FirstSampleStart) {
			m.FirstSampleStart = t
		}
		m.TotalSamplesCount++
	}
	return nil
}

func (m *metricDistributionModelImpl) GetDistribution(detail *apis.MetricDistribution) error {
	if detail == nil {
		return nil
	}
	detail.Metric = m.id
	detail.Avg = resource.NewMilliQuantity(int64(m.histogram.Average()), resource.DecimalSI)
	for qName, q := range defaultQuantileMap {
		// TODO specified quantile
		value := resource.NewMilliQuantity(int64(m.histogram.Percentile(q)*1000), resource.DecimalSI)
		detail.Quantiles[qName] = *value
	}
	// TODO stddev
	detail.FirstSampleTime = &metav1.Time{Time: m.FirstSampleStart}
	detail.LastSampleTime = &metav1.Time{Time: m.LastSampleStart}
	detail.TotalSamplesCount = m.TotalSamplesCount
	detail.UpdateTime = &metav1.Time{Time: m.LastSampleStart}
	return nil
}

func (m *metricDistributionModelImpl) LoadHistoryIfNecessary(snapshot checkpoint.Snapshot) (bool, error) {
	// TODO
	return false, nil
}

func (m *metricDistributionModelImpl) SaveSnapshot(snapshot checkpoint.Snapshot) error {
	// TODO
	return nil
}

type Feeder struct {
	LoadSamples func() ([]metricscollector.Sample, error)
}

var FeedContainerMetricsFromMetricAPIFunc = func(repository metricscollector.MetricsServerRepository,
	containerName string, pods []types.NamespacedName, metricName string) func() ([]metricscollector.Sample, error) {
	return func() ([]metricscollector.Sample, error) {
		samples := make([]metricscollector.Sample, 0)
		var usageOfPods map[types.NamespacedName]metricscollector.Sample
		var err error

		switch metricName {
		case corev1.ResourceCPU.String():
			usageOfPods, err = repository.GetAllContainerCPUUsage(containerName, pods)
		case corev1.ResourceMemory.String():
			usageOfPods, err = repository.GetAllContainerMemoryUsage(containerName, pods)
		default:
			return nil, fmt.Errorf("invalid metric name %v", metricName)
		}

		if err != nil {
			return nil, err
		}
		for _, usage := range usageOfPods {
			samples = append(samples, usage)
		}
		return samples, nil
	}
}
