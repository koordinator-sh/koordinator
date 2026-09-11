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
	// started in StartMonitoring and ended in Complete, or earlier on the timeout,
	// superseded re-entry, and pod-deletion paths, and is nil when no attempt is in
	// flight or no tracer provider is configured.
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
	// Spans for attempts that exceeded the timeout are collected under the lock and ended
	// after it is released: span.End hands the span off to the exporter's batch processor,
	// which must not run under the scheduling hot-path lock.
	var timedOutSpans []oteltrace.Span
	m.lock.Lock()
	for uid := range m.schedulingPods {
		state := m.schedulingPods[uid]
		if shouldSkip, needDelete := isPodUnhandledExceedingTimeout(&state, now, m.unhandledTimeout); shouldSkip {
			if needDelete {
				// Only never-started attempts reach this branch (isPodUnhandledExceedingTimeout
				// requires state.start to be zero), so state.span is always nil here and there
				// is nothing to end.
				delete(m.schedulingPods, uid)
				GCMonitor(uid, &state, now)
			}
			continue
		}
		// When a started attempt exceeds the timeout, end its span with an error status so
		// it is not leaked if the attempt never reaches Complete, and clear the stored span
		// so Complete does not end it a second time.
		if recordIfSchedulingTimeout(uid, &state, now, m.timeout) && state.span != nil {
			timedOutSpans = append(timedOutSpans, state.span)
			state.span = nil
			m.schedulingPods[uid] = state
		}
	}
	m.lock.Unlock()

	for _, span := range timedOutSpans {
		endSpan(span, codes.Error, "scheduling timed out")
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
		state, ok := m.schedulingPods[pod.UID]
		delete(m.schedulingPods, pod.UID)
		m.lock.Unlock()
		if ok {
			// The pod is terminating; end its attempt span (if one was started) so it is
			// not leaked when the attempt is abandoned without ever reaching Complete.
			endSpan(state.span, codes.Error, "pod deleted during scheduling")
		}
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
	var supersededSpan oteltrace.Span
	if !exists {
		scheduleState = podScheduleState{
			start:         now,
			namespace:     pod.Namespace,
			name:          pod.Name,
			schedulerName: pod.Spec.SchedulerName,
		}
	} else {
		// A previous attempt for this pod is still tracked and never reached Complete.
		// Take over its span below and end the stale one outside the lock so it is not
		// leaked when this attempt supersedes it.
		supersededSpan = scheduleState.span
		scheduleState.start = now
	}
	scheduleState.span = span
	StartMonitor(pod, &scheduleState)
	m.schedulingPods[pod.UID] = scheduleState
	m.lock.Unlock()

	endSpan(supersededSpan, codes.Error, "scheduling attempt superseded")
	return ctx
}

func (m *SchedulerMonitor) Complete(pod *corev1.Pod, status *fwktype.Status) {
	m.lock.Lock()
	state, ok := m.schedulingPods[pod.UID]
	delete(m.schedulingPods, pod.UID)
	m.lock.Unlock()

	if ok {
		now := time.Now()
		// End the span after releasing the lock: span.End hands the span off to the
		// exporter's batch processor (which may allocate/queue), and this runs on the
		// scheduling hot path, so it must stay outside the critical section.
		code, msg := codes.Unset, ""
		if status != nil && !status.IsSuccess() {
			code, msg = codes.Error, status.Message()
		}
		endSpan(state.span, code, msg)
		CompleteMonitor(pod, &state, now, m.timeout, status)
	}
}

// endSpan finishes the OpenTelemetry span owned by a scheduling attempt, if any. It is
// a no-op when span is nil (tracing disabled or no attempt started). callers must invoke
// it outside m.lock: span.End hands the span to the exporter's batch processor, which may
// allocate, and must not run under the scheduling hot-path lock.
func endSpan(span oteltrace.Span, code codes.Code, msg string) {
	if span == nil {
		return
	}
	if code != codes.Unset {
		span.SetStatus(code, msg)
	}
	span.End()
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

// recordIfSchedulingTimeout logs and records the metric when a started attempt has
// exceeded the timeout, and reports whether the timeout fired so the caller can end the
// attempt's span.
func recordIfSchedulingTimeout(uid types.UID, state *podScheduleState, now time.Time, timeout time.Duration) bool {
	if state.start.IsZero() {
		klog.V(5).Infof("scheduling pod %s/%s(%s) missing a start %v", state.namespace, state.name, uid, state.start)
		return false
	}
	if interval := now.Sub(state.start); interval > timeout {
		logWarningF("!!!CRITICAL TIMEOUT!!! scheduling pod %s/%s(%s) took longer (%s) than the timeout %v", state.namespace, state.name, uid, interval, timeout)
		metrics.SchedulingTimeout.WithLabelValues(state.schedulerName).Inc()
		return true
	}
	return false
}
