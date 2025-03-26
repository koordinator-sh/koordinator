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

package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/klog/v2"
)

const (
	DefaultExpireTime = 5 * time.Minute
	DefaultGCInterval = 1 * time.Minute
)

var defaultMetricGC = NewMetricGC(DefaultExpireTime, DefaultGCInterval)

type GCGaugeVec struct {
	name         string
	vec          *prometheus.GaugeVec
	expireStatus MetricGC
}

func NewGCGaugeVec(name string, vec *prometheus.GaugeVec) *GCGaugeVec {
	return newGCGaugeVec(name, vec, defaultMetricGC)
}

func newGCGaugeVec(name string, vec *prometheus.GaugeVec, metricGC MetricGC) *GCGaugeVec {
	metricGC.AddMetric(name, vec.MetricVec)
	return &GCGaugeVec{name: name, vec: vec, expireStatus: metricGC}
}

func (g *GCGaugeVec) GetGaugeVec() *prometheus.GaugeVec {
	return g.vec
}

func (g *GCGaugeVec) WithSet(labels prometheus.Labels, value float64) {
	g.vec.With(labels).Set(value)
	g.expireStatus.UpdateStatus(g.name, labels)
}

func (g *GCGaugeVec) Delete(labels prometheus.Labels) {
	g.vec.Delete(labels)
	g.expireStatus.RemoveStatus(g.name, labels)
}

type GCCounterVec struct {
	name         string
	vec          *prometheus.CounterVec
	expireStatus MetricGC
}

func NewGCCounterVec(name string, vec *prometheus.CounterVec) *GCCounterVec {
	return newGCCounterVec(name, vec, defaultMetricGC)
}

func newGCCounterVec(name string, vec *prometheus.CounterVec, metricGC MetricGC) *GCCounterVec {
	metricGC.AddMetric(name, vec.MetricVec)
	return &GCCounterVec{name: name, vec: vec, expireStatus: metricGC}
}

func (g *GCCounterVec) GetCounterVec() *prometheus.CounterVec {
	return g.vec
}

func (g *GCCounterVec) WithInc(labels prometheus.Labels) {
	g.vec.With(labels).Inc()
	g.expireStatus.UpdateStatus(g.name, labels)
}

func (g *GCCounterVec) Delete(labels prometheus.Labels) {
	g.vec.Delete(labels)
	g.expireStatus.RemoveStatus(g.name, labels)
}

// GCHistogramVec wraps a prometheus.HistogramVec and integrates with MetricGC for expiration handling.
type GCHistogramVec struct {
	name         string
	vec          *prometheus.HistogramVec
	expireStatus MetricGC
}

func NewGCHistogramVec(name string, vec *prometheus.HistogramVec) *GCHistogramVec {
	return newGCHistogramVec(name, vec, defaultMetricGC)
}

func newGCHistogramVec(name string, vec *prometheus.HistogramVec, metricGC MetricGC) *GCHistogramVec {
	metricGC.AddMetric(name, vec.MetricVec)
	return &GCHistogramVec{name: name, vec: vec, expireStatus: metricGC}
}

func (g *GCHistogramVec) GetHistogramVec() *prometheus.HistogramVec {
	return g.vec
}

// WithObserve records a value in the histogram and updates the expiration status.
func (g *GCHistogramVec) WithObserve(labels prometheus.Labels, value float64) {
	g.vec.With(labels).Observe(value)
	g.expireStatus.UpdateStatus(g.name, labels)
}

// Delete removes the metric with the given labels and updates the expiration status.
func (g *GCHistogramVec) Delete(labels prometheus.Labels) {
	g.vec.Delete(labels)
	g.expireStatus.RemoveStatus(g.name, labels)
}

type MetricVecGC interface {
	// Len returns the length of the alive metric statuses.
	Len() int
	// UpdateStatus updates the metric status with the given label values and timestamp (Unix seconds).
	UpdateStatus(updateTime int64, labels prometheus.Labels)
	// RemoveStatus removes the metric status with the given label values.
	RemoveStatus(labels prometheus.Labels)
	// ExpireMetrics expires all metric statuses which are updated before the expired time (Unix seconds).
	ExpireMetrics(expireTime int64) int
}

// record metric last updateTime and then can expire by updateTime
type metricVecGC struct {
	lock      sync.Mutex
	name      string
	metricVec *prometheus.MetricVec
	statuses  map[string]metricStatus // label values -> timestamp
}

type metricStatus struct {
	Labels          prometheus.Labels
	lastUpdatedUnix int64
}

func NewMetricVecGC(name string, metricVec *prometheus.MetricVec) MetricVecGC {
	return &metricVecGC{
		name:      name,
		metricVec: metricVec,
		statuses:  map[string]metricStatus{},
	}
}

func (v *metricVecGC) Len() int {
	v.lock.Lock()
	defer v.lock.Unlock()
	return len(v.statuses)
}

func (v *metricVecGC) UpdateStatus(updateTime int64, labels prometheus.Labels) {
	statusKey := labelsToKey(labels)
	status := &metricStatus{
		Labels:          labels,
		lastUpdatedUnix: updateTime,
	}

	v.lock.Lock()
	defer v.lock.Unlock()
	v.updateStatus(statusKey, status)
}

func (v *metricVecGC) updateStatus(statusKey string, status *metricStatus) {
	v.statuses[statusKey] = *status
}

func (v *metricVecGC) RemoveStatus(labels prometheus.Labels) {
	statusKey := labelsToKey(labels)

	v.lock.Lock()
	defer v.lock.Unlock()
	v.removeStatus(statusKey)
}

func (v *metricVecGC) removeStatus(statusKey string) {
	delete(v.statuses, statusKey)
}

func (v *metricVecGC) ExpireMetrics(expireTime int64) int {
	v.lock.Lock()
	defer v.lock.Unlock()
	count := 0
	for key, status := range v.statuses {
		if status.lastUpdatedUnix < expireTime {
			v.removeStatus(key)
			v.metricVec.Delete(status.Labels)
			count++
			klog.V(6).Infof("metricVecGC %s delete metric, key %s, updateTime %v, expireTime %v",
				v.name, key, status.lastUpdatedUnix, expireTime)
		}
	}
	if count > 0 {
		klog.V(5).Infof("metricVecGC %s expires metrics successfully, expire num: %d", v.name, count)
	}
	return count
}

type MetricGC interface {
	Run()
	Stop()
	AddMetric(name string, metric *prometheus.MetricVec)
	UpdateStatus(name string, labels prometheus.Labels)
	RemoveStatus(name string, labels prometheus.Labels)
	CountStatus(name string) int
}

type metricGC struct {
	globalLock sync.RWMutex
	metrics    map[string]MetricVecGC // metric name -> metricVecGC

	// expire time for metrics
	expireTime time.Duration
	// gc interval
	interval time.Duration

	stopCh chan struct{}
}

func NewMetricGC(expireTime time.Duration, interval time.Duration) MetricGC {
	m := &metricGC{
		metrics:    map[string]MetricVecGC{},
		expireTime: expireTime,
		interval:   interval,
		stopCh:     make(chan struct{}, 1),
	}
	m.Run()

	return m
}

func (e *metricGC) AddMetric(metricName string, metric *prometheus.MetricVec) {
	e.globalLock.Lock()
	defer e.globalLock.Unlock()
	e.addMetric(metricName, metric)
}

func (e *metricGC) addMetric(metricName string, metric *prometheus.MetricVec) {
	vecGC := NewMetricVecGC(metricName, metric)
	e.metrics[metricName] = vecGC
}

func (e *metricGC) Run() {
	go e.run()
}

func (e *metricGC) run() {
	timer := time.NewTimer(e.interval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			err := e.expire()
			if err != nil {
				klog.Errorf("expire metrics error! err: %v", err)
			}
			timer.Reset(e.interval)
		case <-e.stopCh:
			klog.Infof("metrics gc task stopped!")
			return
		}
	}
}

func (e *metricGC) Stop() {
	close(e.stopCh)
}

func (e *metricGC) UpdateStatus(metricName string, labels prometheus.Labels) {
	e.globalLock.RLock()
	// different metric vectors can update simultaneously
	err := e.updateStatus(time.Now().Unix(), metricName, labels)
	e.globalLock.RUnlock()
	if err != nil {
		klog.Errorf("failed to update status for metric %s, err: %s", metricName, err.Error())
	}
}

func (e *metricGC) updateStatus(updateTime int64, metricName string, labels prometheus.Labels) error {
	metric, ok := e.metrics[metricName]
	if !ok {
		return fmt.Errorf("metric not correctly added")
	}
	metric.UpdateStatus(updateTime, labels)
	return nil
}

func (e *metricGC) RemoveStatus(metricName string, labels prometheus.Labels) {
	e.globalLock.RLock()
	err := e.removeStatus(metricName, labels)
	e.globalLock.RUnlock()
	if err != nil {
		klog.Errorf("failed to remove status for metric %s, err: %s", metricName, err.Error())
	}
}

func (e *metricGC) removeStatus(metricName string, labels prometheus.Labels) error {
	metric, ok := e.metrics[metricName]
	if !ok {
		return fmt.Errorf("metric not correctly added")
	}
	metric.RemoveStatus(labels)
	return nil
}

func (e *metricGC) CountStatus(metricName string) int {
	e.globalLock.RLock()
	defer e.globalLock.RUnlock()
	metric, ok := e.metrics[metricName]
	if !ok {
		return 0
	}
	return metric.Len()
}

func (e *metricGC) statusLen() int {
	e.globalLock.RLock()
	defer e.globalLock.RUnlock()
	statusLen := 0
	for _, metric := range e.metrics {
		statusLen += metric.Len()
	}
	return statusLen
}

func (e *metricGC) expire() error {
	e.globalLock.RLock()
	defer e.globalLock.RUnlock()
	expireTime := time.Now().Unix() - int64(e.expireTime/time.Second)
	count := 0
	for _, metric := range e.metrics {
		count += metric.ExpireMetrics(expireTime)
	}
	if count > 0 {
		klog.V(4).Infof("expire metrics successfully, expire num: %d", count)
	}
	return nil
}

// labelsToKey generate a key for a metric with the given metric label pairs.
// NOTE: It assumes that the label keys of a metric vector are fixed.
// pattern: ${name}:${key1},${key2},...
func labelsToKey(labels prometheus.Labels) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i := range keys {
		b.WriteString(labels[keys[i]])
		b.WriteByte(',')
	}
	return b.String()
}
