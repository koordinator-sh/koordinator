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
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
	resourceclient "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"
)

// ContainerMetricsSnapshot contains information about usage of certain container within defined time window.
type ContainerMetricsSnapshot struct {
	// identifies a specific container those metrics are coming from.
	Namespace     string
	PodName       string
	ContainerName string
	// End time of the measurement interval.
	SnapshotTime time.Time
	// Duration of the measurement interval, which is [SnapshotTime - SnapshotWindow, SnapshotTime].
	SnapshotWindow time.Duration
	// Actual usage of the resources over the measurement interval.
	Usage corev1.ResourceList
}

// MetricsClient provides simple metrics on resources usage on containter level.
type MetricsClient interface {
	// GetContainersMetrics returns an array of ContainerMetricsSnapshots,
	// representing resource usage for every running container in the cluster
	GetContainersMetrics() ([]*ContainerMetricsSnapshot, error)
	// GetContainersMetricsByPod returns an array of ContainerMetricsSnapshots,
	// representing resource usage for every running container in the given pod
	GetContainersMetricsByPod(podNs, podName string) ([]*ContainerMetricsSnapshot, error)
}

type metricsClient struct {
	metricsGetter resourceclient.PodMetricsesGetter
	namespace     string
}

// NewMetricsClient creates new instance of MetricsClient, which is used by recommender.
// It requires an instance of PodMetricsesGetter, which is used for underlying communication with metrics server.
// namespace limits queries to particular namespace, use core.NamespaceAll to select all namespaces.
func NewMetricsClient(metricsGetter resourceclient.PodMetricsesGetter, namespace string) MetricsClient {
	return &metricsClient{
		metricsGetter: metricsGetter,
		namespace:     namespace,
	}
}

func (c *metricsClient) GetContainersMetricsByPod(podNs, podName string) ([]*ContainerMetricsSnapshot, error) {
	var metricsSnapshots []*ContainerMetricsSnapshot
	podMetricsInterface := c.metricsGetter.PodMetricses(podNs)
	podMetric, err := podMetricsInterface.Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil || podMetric == nil {
		return nil, err
	}
	metricsSnapshotsForPod := createContainerMetricsSnapshots(*podMetric)
	metricsSnapshots = append(metricsSnapshots, metricsSnapshotsForPod...)
	return metricsSnapshots, nil
}

func (c *metricsClient) GetContainersMetrics() ([]*ContainerMetricsSnapshot, error) {
	var metricsSnapshots []*ContainerMetricsSnapshot

	podMetricsInterface := c.metricsGetter.PodMetricses(c.namespace)
	podMetricsList, err := podMetricsInterface.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, podMetrics := range podMetricsList.Items {
		metricsSnapshotsForPod := createContainerMetricsSnapshots(podMetrics)
		metricsSnapshots = append(metricsSnapshots, metricsSnapshotsForPod...)
	}

	return metricsSnapshots, nil
}

func createContainerMetricsSnapshots(podMetrics v1beta1.PodMetrics) []*ContainerMetricsSnapshot {
	snapshots := make([]*ContainerMetricsSnapshot, len(podMetrics.Containers))
	for i, containerMetrics := range podMetrics.Containers {
		snapshots[i] = newContainerMetricsSnapshot(containerMetrics, podMetrics)
	}
	return snapshots
}

func newContainerMetricsSnapshot(containerMetrics v1beta1.ContainerMetrics, podMetrics v1beta1.PodMetrics) *ContainerMetricsSnapshot {
	return &ContainerMetricsSnapshot{
		Namespace:      podMetrics.Namespace,
		PodName:        podMetrics.Name,
		ContainerName:  containerMetrics.Name,
		Usage:          containerMetrics.Usage,
		SnapshotTime:   podMetrics.Timestamp.Time,
		SnapshotWindow: podMetrics.Window.Duration,
	}
}
