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

package frameworkext

import (
	"sync"
	"time"

	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

var (
	schedulerMonitorPeriod = 10 * time.Second
	schedulingTimeout      = 30 * time.Second

	logWarningF = klog.Warningf
)

func init() {
	pflag.DurationVar(&schedulerMonitorPeriod, "scheduler-monitor-period", schedulerMonitorPeriod, "Execution period of scheduler monitor")
	pflag.DurationVar(&schedulingTimeout, "scheduling-timeout", schedulingTimeout, "The maximum acceptable scheduling time interval. After timeout, the metric will be updated and the log will be printed.")
}

type SchedulerMonitor struct {
	timeout        time.Duration
	lock           sync.Mutex
	schedulingPods map[types.UID]podScheduleState
}

type podScheduleState struct {
	start         time.Time
	namespace     string
	name          string
	schedulerName string
}

func NewSchedulerMonitor(period time.Duration, timeout time.Duration) *SchedulerMonitor {
	m := &SchedulerMonitor{
		timeout:        timeout,
		schedulingPods: map[types.UID]podScheduleState{},
	}
	go wait.Forever(m.monitor, period)
	return m
}

func (m *SchedulerMonitor) monitor() {
	m.lock.Lock()
	defer m.lock.Unlock()

	now := time.Now()
	for uid, v := range m.schedulingPods {
		recordIfSchedulingTimeout(uid, &v, now, m.timeout)
	}
}

func (m *SchedulerMonitor) StartMonitoring(pod *corev1.Pod) {
	klog.Infof("start monitoring pod %v(%s)", klog.KObj(pod), pod.UID)

	m.lock.Lock()
	defer m.lock.Unlock()
	m.schedulingPods[pod.UID] = podScheduleState{
		start:         time.Now(),
		namespace:     pod.Namespace,
		name:          pod.Name,
		schedulerName: pod.Spec.SchedulerName,
	}
}

func (m *SchedulerMonitor) Complete(pod *corev1.Pod) {
	klog.Infof("pod %v(%s) scheduled complete", klog.KObj(pod), pod.UID)

	m.lock.Lock()
	defer m.lock.Unlock()

	state, ok := m.schedulingPods[pod.UID]
	if ok {
		now := time.Now()
		recordIfSchedulingTimeout(pod.UID, &state, now, m.timeout)
		delete(m.schedulingPods, pod.UID)
	}
}

func recordIfSchedulingTimeout(uid types.UID, state *podScheduleState, now time.Time, timeout time.Duration) {
	if interval := now.Sub(state.start); interval > timeout {
		logWarningF("!!!CRITICAL TIMEOUT!!! scheduling pod %s/%s(%s) took longer (%s) than the timeout %v", state.namespace, state.name, uid, interval, timeout)
		metrics.SchedulingTimeout.WithLabelValues(state.schedulerName).Inc()
	}
}
