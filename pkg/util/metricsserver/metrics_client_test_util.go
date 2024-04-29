/*
Copyright 2023 The Koordinator Authors.
Copyright 2017 The Kubernetes Authors.

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

package metricsserver

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	core "k8s.io/client-go/testing"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	"k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

type metricsClientTestCase struct {
	snapshotTimestamp    time.Time
	snapshotWindow       time.Duration
	namespace            *corev1.Namespace
	pod1Snaps, pod2Snaps []*ContainerMetricsSnapshot
}

type containerID struct {
	namespace     string
	podname       string
	containerName string
}

func newMetricsClientTestCase() *metricsClientTestCase {
	namespaceName := "test-namespace"

	testCase := &metricsClientTestCase{
		snapshotTimestamp: time.Now(),
		snapshotWindow:    time.Duration(1234),
		namespace:         &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}},
	}

	id1 := containerID{namespace: namespaceName, podname: "Pod1", containerName: "Name1"}
	id2 := containerID{namespace: namespaceName, podname: "Pod1", containerName: "Name2"}
	id3 := containerID{namespace: namespaceName, podname: "Pod2", containerName: "Name1"}
	id4 := containerID{namespace: namespaceName, podname: "Pod2", containerName: "Name2"}

	testCase.pod1Snaps = append(testCase.pod1Snaps, testCase.newContainerMetricsSnapshot(id1, 400, 333))
	testCase.pod1Snaps = append(testCase.pod1Snaps, testCase.newContainerMetricsSnapshot(id2, 800, 666))
	testCase.pod2Snaps = append(testCase.pod2Snaps, testCase.newContainerMetricsSnapshot(id3, 401, 334))
	testCase.pod2Snaps = append(testCase.pod2Snaps, testCase.newContainerMetricsSnapshot(id4, 801, 667))

	return testCase
}

func newEmptyMetricsClientTestCase() *metricsClientTestCase {
	return &metricsClientTestCase{}
}

func (tc *metricsClientTestCase) newContainerMetricsSnapshot(id containerID, cpuUsage int64, memUsage int64) *ContainerMetricsSnapshot {
	return &ContainerMetricsSnapshot{
		Namespace:      id.namespace,
		PodName:        id.podname,
		ContainerName:  id.containerName,
		SnapshotTime:   tc.snapshotTimestamp,
		SnapshotWindow: tc.snapshotWindow,
		Usage: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewQuantity(cpuUsage, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(memUsage, resource.BinarySI),
		},
	}
}

func (tc *metricsClientTestCase) createFakeMetricsClient() MetricsClient {
	fakeMetricsGetter := &fake.Clientset{}
	fakeMetricsGetter.AddReactor("list", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		return true, tc.getFakePodMetricsList(), nil
	})
	fakeMetricsGetter.AddReactor("get", "pods", func(action core.Action) (handled bool, ret runtime.Object, err error) {
		return true, tc.getFakePodMetric(), nil
	})
	return NewMetricsClient(fakeMetricsGetter.MetricsV1beta1(), "")
}

func (tc *metricsClientTestCase) getFakePodMetricsList() *metricsapi.PodMetricsList {
	metrics := &metricsapi.PodMetricsList{}
	if tc.pod1Snaps != nil && tc.pod2Snaps != nil {
		metrics.Items = append(metrics.Items, makePodMetrics(tc.pod1Snaps))
		metrics.Items = append(metrics.Items, makePodMetrics(tc.pod2Snaps))
	}
	return metrics
}

func (tc *metricsClientTestCase) getFakePodMetric() *metricsapi.PodMetrics {
	metrics := &metricsapi.PodMetrics{}
	if tc.pod1Snaps != nil {
		*metrics = makePodMetrics(tc.pod1Snaps)
	}

	return metrics
}

func makePodMetrics(snaps []*ContainerMetricsSnapshot) metricsapi.PodMetrics {
	firstSnap := snaps[0]
	podMetrics := metricsapi.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: firstSnap.Namespace,
			Name:      firstSnap.PodName,
		},
		Timestamp:  metav1.Time{Time: firstSnap.SnapshotTime},
		Window:     metav1.Duration{Duration: firstSnap.SnapshotWindow},
		Containers: make([]metricsapi.ContainerMetrics, len(snaps)),
	}

	for i, snap := range snaps {
		podMetrics.Containers[i] = metricsapi.ContainerMetrics{
			Name:  snap.ContainerName,
			Usage: snap.Usage,
		}
	}
	return podMetrics
}

func (tc *metricsClientTestCase) getAllSnaps() []*ContainerMetricsSnapshot {
	return append(tc.pod1Snaps, tc.pod2Snaps...)
}
