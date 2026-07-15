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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func TestSchedulerMonitor_Timeout(t *testing.T) {
	timeout := 10 * time.Millisecond

	var capturedLog string
	mockLogFunction := func(format string, args ...interface{}) {
		capturedLog = fmt.Sprintf(format, args...)
	}
	logWarningF = mockLogFunction
	defer func() {
		logWarningF = klog.Warningf
	}()

	monitor := NewSchedulerMonitor(schedulerMonitorPeriod, timeout)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
	}

	monitor.StartMonitoring(context.TODO(), pod)
	time.Sleep(2 * timeout)
	monitor.monitor()
	if len(capturedLog) == 0 || !strings.Contains(capturedLog, "!!!CRITICAL TIMEOUT!!!") {
		t.Errorf("Expected a timeout log to be recorded, but got: %s", capturedLog)
	}
	monitor.Complete(pod, nil)
}

func TestSchedulerMonitor_NoTimeout(t *testing.T) {
	timeout := 1 * time.Second

	var capturedLog string
	mockLogFunction := func(format string, args ...interface{}) {
		capturedLog = fmt.Sprintf(format, args...)
	}
	logWarningF = mockLogFunction
	defer func() {
		logWarningF = klog.Warningf
	}()

	monitor := NewSchedulerMonitor(schedulerMonitorPeriod, timeout)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
	}

	monitor.StartMonitoring(context.TODO(), pod)
	monitor.monitor()
	if len(capturedLog) > 0 {
		t.Errorf("Expected no timeout log to be recorded, but got: %s", capturedLog)
	}
	monitor.Complete(pod, nil)
}

// TestSchedulerMonitor_Tracing verifies that StartMonitoring starts a root span for the
// scheduling attempt, carries it in the returned context, and Complete ends it, marking
// the span as failed when the attempt is unschedulable.
func TestSchedulerMonitor_Tracing(t *testing.T) {
	// Install an in-memory tracer provider to capture emitted spans, and restore the
	// previous global provider afterwards so other tests are unaffected.
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(previous)

	monitor := NewSchedulerMonitor(schedulerMonitorPeriod, 10*time.Millisecond)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
	}

	ctx := monitor.StartMonitoring(context.TODO(), pod)
	// The returned context must carry the attempt span so downstream extension points
	// can nest their spans under it.
	assert.True(t, oteltrace.SpanFromContext(ctx).SpanContext().IsValid(), "expected the attempt span to be carried in the returned context")
	// Until Complete is called, the span is still open and must not be recorded.
	assert.Empty(t, recorder.Ended(), "span should not be ended before Complete")

	monitor.Complete(pod, fwktype.NewStatus(fwktype.Unschedulable, "node(s) is unschedulable"))

	var found sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() == "SchedulingCycle" {
			found = s
			break
		}
	}
	if assert.NotNil(t, found, "expected a SchedulingCycle span to be recorded after Complete") {
		attrs := map[string]string{}
		for _, kv := range found.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		assert.Equal(t, "test-ns", attrs["pod.namespace"])
		assert.Equal(t, "test-pod", attrs["pod.name"])
		// An unschedulable attempt should mark the span status as error.
		assert.Equal(t, codes.Error, found.Status().Code)
	}
}

func TestSchedulerMonitor_DropUnhandledTimeout(t *testing.T) {
	monitor := NewSchedulerMonitor(schedulerMonitorPeriod, schedulingTimeout)
	monitor.unhandledTimeout = 10 * time.Millisecond

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
	}

	monitor.RecordNextPod(&framework.QueuedPodInfo{
		PodInfo: &framework.PodInfo{
			Pod: pod,
		},
	})
	monitor.lock.Lock()
	state, ok := monitor.schedulingPods[pod.UID]
	monitor.lock.Unlock()
	if !ok {
		t.Errorf("failed to record NextPod, expect QueuedPodInfo exists")
		return
	}
	if state.dequeued.IsZero() {
		t.Errorf("failed to record NextPod, expect dequeued is set")
		return
	}
	if !state.start.IsZero() {
		t.Errorf("failed to record NextPod, expect start is not set")
		return
	}

	time.Sleep(2 * monitor.unhandledTimeout)
	monitor.monitor()
	monitor.lock.Lock()
	_, ok = monitor.schedulingPods[pod.UID]
	monitor.lock.Unlock()
	if ok {
		t.Errorf("failed to drop unhandled pod, expect QueuedPodInfo is deleted")
		return
	}
}

func TestSchedulerMonitor_StartAndCompleteMonitoring(t *testing.T) {
	timeout := 10 * time.Millisecond

	monitor := NewSchedulerMonitor(schedulerMonitorPeriod, timeout)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-ns",
			UID:       types.UID("test-uid"),
		},
	}
	podInfo, err := framework.NewPodInfo(pod)
	if err != nil {
		t.Errorf("Failed to create podInfo for pod, err: %s", err)
	}
	queuePodInfo := &framework.QueuedPodInfo{
		Timestamp: time.Now(),
		PodInfo:   podInfo,
	}
	monitor.RecordNextPod(queuePodInfo)

	monitor.StartMonitoring(context.TODO(), pod)
	state, ok := monitor.schedulingPods[pod.UID]
	if !ok {
		t.Fatal("Pod not found in schedulingPods after StartMonitoring")
	}
	if state.start.IsZero() {
		t.Errorf("Start time should not be zero after StartMonitoring")
	}
	time.Sleep(2 * timeout)
	monitor.Complete(pod, fwktype.NewStatus(fwktype.Unschedulable, "node(s) is unschedulable"))
	if _, exists := monitor.schedulingPods[pod.UID]; exists {
		t.Errorf("Pod should be removed from schedulingPods after Complete")
	}

	terminatingPod := podInfo.DeepCopy()
	deleted := metav1.Now()
	terminatingPod.Pod.DeletionTimestamp = &deleted
	queuePodInfo = &framework.QueuedPodInfo{
		Timestamp: time.Now(),
		PodInfo:   terminatingPod,
	}
	monitor.RecordNextPod(queuePodInfo)
	_, ok = monitor.schedulingPods[pod.UID]
	if ok {
		t.Fatal("Pod should be removed in schedulingPods when terminating")
	}
}
