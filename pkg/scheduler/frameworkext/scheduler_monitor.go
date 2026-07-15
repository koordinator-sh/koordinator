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
	"context"
	"sync"
	"time"

	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/metrics"
)

// tracingInstrumentationScope is the OpenTelemetry instrumentation scope reported
// for spans emitted by the koord-scheduler framework extender.
const tracingInstrumentationScope = "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"

var (
	schedulerMonitorPeriod         = 10 * time.Second
	schedulingTimeout              = 30 * time.Second
	schedulingDropUnhandledTimeout = 5 * time.Second

	logWarningF = klog.Warningf
)

func init() {
	pflag.DurationVar(&schedulerMonitorPeriod, "scheduler-monitor-period", schedulerMonitorPeriod, "Execution period of scheduler monitor")
	pflag.DurationVar(&schedulingTimeout, "scheduling-timeout", schedulingTimeout, "The maximum acceptable scheduling time interval. After timeout, the metric will be updated and the log will be printed.")
	pflag.DurationVar(&schedulingDropUnhandledTimeout, "scheduling-drop-unhandled-timeout", schedulingDropUnhandledTimeout, "The maximum acceptable scheduling time interval to drop the invalid pod context when the pod is dequeued but has not handled to schedule.")
}

var (
	StartMonitor       = defaultStartMonitor       // start a schedule attempt
	CompleteMonitor    = defaultCompleteMonitor    // complete a schedule attempt
	RecordQueuePodInfo = defaultRecordQueuePodInfo // dequeue a schedule attempt
	GCMonitor          = defaultGCMonitor          // garbage collect an unhandled schedule attempt
)

type SchedulerMonitor struct {
	timeout          time.Duration
	unhandledTimeout time.Duration
	lock             sync.Mutex
	schedulingPods   map[types.UID]podScheduleState
}

type podScheduleState struct {
	namespace     string
	name          string
	schedulerName string
	// scheduling info
	start time.Time
	// queue info
	dequeued        time.Time
	lastEnqueued    time.Time
	attempts        int
	initialEnqueued *time.Time
	// for extensions
	extensionInfo interface{}
	// span is the OpenTelemetry root span for the current scheduling attempt. It is
	// started in StartMonitoring and ended in Complete, and is nil when no attempt is
	// in flight or no tracer provider is configured.
	span oteltrace.Span
}

func NewSchedulerMonitor(period time.Duration, timeout time.Duration) *SchedulerMonitor {
	m := &SchedulerMonitor{
		timeout:          timeout,
		unhandledTimeout: schedulingDropUnhandledTimeout,
		schedulingPods:   map[types.UID]podScheduleState{},
	}
	go wait.Forever(m.monitor, period)
	return m
}

func (m *SchedulerMonitor) monitor() {
	now := time.Now()
	m.lock.Lock()
	defer m.lock.Unlock()
	for uid := range m.schedulingPods {
		state := m.schedulingPods[uid]
		if shouldSkip, needDelete := isPodUnhandledExceedingTimeout(&state, now, m.unhandledTimeout); shouldSkip {
			if needDelete {
				delete(m.schedulingPods, uid)
				GCMonitor(uid, &state, now)
			}
			continue
		}
		recordIfSchedulingTimeout(uid, &state, now, m.timeout)
	}
}

func (m *SchedulerMonitor) RecordNextPod(podInfo *framework.QueuedPodInfo) {
	if podInfo == nil || podInfo.Pod == nil {
		return
	}
	pod := podInfo.Pod

	// clean up from the cache when the pod is terminating
	if pod.DeletionTimestamp != nil {
		m.lock.Lock()
		delete(m.schedulingPods, pod.UID)
		m.lock.Unlock()
		return
	}

	now := time.Now()
	scheduleState := podScheduleState{
		namespace:       pod.Namespace,
		name:            pod.Name,
		schedulerName:   pod.Spec.SchedulerName,
		dequeued:        now,
		lastEnqueued:    podInfo.Timestamp,
		attempts:        podInfo.Attempts,
		initialEnqueued: podInfo.InitialAttemptTimestamp,
	}
	RecordQueuePodInfo(podInfo, &scheduleState)

	m.lock.Lock()
	m.schedulingPods[pod.UID] = scheduleState
	m.lock.Unlock()
}

func (m *SchedulerMonitor) StartMonitoring(ctx context.Context, pod *corev1.Pod) context.Context {
	now := time.Now()

	// Start the root span for the whole scheduling attempt. When no tracer provider is
	// configured, the global provider is a no-op, so this adds negligible overhead. The
	// span is ended in Complete. Returning the derived ctx lets downstream extension
	// points nest their spans under this attempt span.
	ctx, span := otel.GetTracerProvider().Tracer(tracingInstrumentationScope).Start(ctx, "SchedulingCycle",
		oteltrace.WithAttributes(
			attribute.String("pod.namespace", pod.Namespace),
			attribute.String("pod.name", pod.Name),
		))

	m.lock.Lock()
	scheduleState, exists := m.schedulingPods[pod.UID]
	if !exists {
		scheduleState = podScheduleState{
			start:         now,
			namespace:     pod.Namespace,
			name:          pod.Name,
			schedulerName: pod.Spec.SchedulerName,
		}
	} else {
		scheduleState.start = now
	}
	scheduleState.span = span
	StartMonitor(pod, &scheduleState)
	m.schedulingPods[pod.UID] = scheduleState
	m.lock.Unlock()
	return ctx
}

func (m *SchedulerMonitor) Complete(pod *corev1.Pod, status *fwktype.Status) {
	m.lock.Lock()
	state, ok := m.schedulingPods[pod.UID]
	delete(m.schedulingPods, pod.UID)
	m.lock.Unlock()

	if ok {
		now := time.Now()
		if state.span != nil {
			if status != nil && !status.IsSuccess() {
				state.span.SetStatus(codes.Error, status.Message())
			}
			state.span.End()
		}
		CompleteMonitor(pod, &state, now, m.timeout, status)
	}
}

func isPodUnhandledExceedingTimeout(state *podScheduleState, now time.Time, timeout time.Duration) (skipped bool, toDelete bool) {
	if !state.start.IsZero() { // pod is handled
		return false, false
	}
	if interval := now.Sub(state.dequeued); interval > timeout { // unhandled exceeding timeout
		klog.V(4).Infof("pod %s/%s is dropped due to handled interval %v exceeding timeout %v", state.namespace, state.name, interval, timeout)
		return true, true
	} else { // unhandled in timeout
		klog.V(6).Infof("pod %s/%s is dropped due to handled interval %v during the timeout %v", state.namespace, state.name, interval, timeout)
	}
	return true, false
}

func defaultStartMonitor(pod *corev1.Pod, state *podScheduleState) {
	klog.Infof("start monitoring pod %v(%s)", klog.KObj(pod), pod.UID)
}

func defaultCompleteMonitor(pod *corev1.Pod, state *podScheduleState, end time.Time, timeout time.Duration, status *fwktype.Status) {
	klog.Infof("pod %v(%s) scheduled complete", klog.KObj(pod), pod.UID)
	recordIfSchedulingTimeout(pod.UID, state, end, timeout)
}

func defaultRecordQueuePodInfo(podInfo *framework.QueuedPodInfo, state *podScheduleState) {
}

func defaultGCMonitor(uid types.UID, state *podScheduleState, end time.Time) {
}

func recordIfSchedulingTimeout(uid types.UID, state *podScheduleState, now time.Time, timeout time.Duration) {
	if state.start.IsZero() {
		klog.V(5).Infof("scheduling pod %s/%s(%s) missing a start %v", state.namespace, state.name, uid, state.start)
		return
	}
	if interval := now.Sub(state.start); interval > timeout {
		logWarningF("!!!CRITICAL TIMEOUT!!! scheduling pod %s/%s(%s) took longer (%s) than the timeout %v", state.namespace, state.name, uid, interval, timeout)
		metrics.SchedulingTimeout.WithLabelValues(state.schedulerName).Inc()
	}
}
