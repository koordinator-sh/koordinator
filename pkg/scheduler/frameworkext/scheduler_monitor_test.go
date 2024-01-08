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
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
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

	monitor.StartMonitoring(pod)
	time.Sleep(2 * timeout)
	monitor.monitor()
	if len(capturedLog) == 0 || !strings.Contains(capturedLog, "!!!CRITICAL TIMEOUT!!!") {
		t.Errorf("Expected a timeout log to be recorded, but got: %s", capturedLog)
	}
	monitor.Complete(pod)
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

	monitor.StartMonitoring(pod)
	monitor.monitor()
	if len(capturedLog) > 0 {
		t.Errorf("Expected no timeout log to be recorded, but got: %s", capturedLog)
	}
	monitor.Complete(pod)
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

	monitor.StartMonitoring(pod)
	state, ok := monitor.schedulingPods[pod.UID]
	if !ok {
		t.Fatal("Pod not found in schedulingPods after StartMonitoring")
	}
	if state.start.IsZero() {
		t.Errorf("Start time should not be zero after StartMonitoring")
	}
	time.Sleep(2 * timeout)
	monitor.Complete(pod)
	if _, exists := monitor.schedulingPods[pod.UID]; exists {
		t.Errorf("Pod should be removed from schedulingPods after Complete")
	}
}
