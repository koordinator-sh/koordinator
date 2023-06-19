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

package prediction

import (
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type UIDType string

type UIDGenerator interface {
	Pod(pod *v1.Pod) UIDType
	Node(node *v1.Node) UIDType
}

type Options struct {
	Filepath string
	// TODO add configs here
}

// The kubernetes UID is unique within the Pod lifecycle, so use this first. If there
// are some special scenarios in the future, such as deleting a Pod and creating a Pod
// with the same name, consider using NamespacedName as the UID.
type generator struct {
}

func (gen *generator) Pod(pod *v1.Pod) UIDType {
	return UIDType(pod.GetUID())
}

func (gen *generator) Node(node *v1.Node) UIDType {
	return UIDType(node.GetUID())
}

type Result struct {
	// Use different quantile type as key, currently support "p60", "p90", "p95" "p98", "max".
	Data map[string]v1.ResourceList
}

// FIXME
// This is used for the agent's dependence on the basic data structure, and the
// basic data structure will be reconstructed later to better support testing.
type Informer interface {
	HasSynced() bool
	ListPods() []*v1.Pod
	GetNode() *v1.Node
}

func NewInformer(statesInformer statesinformer.StatesInformer) Informer {
	return &informer{
		statesInformer: statesInformer,
	}
}

type informer struct {
	statesInformer statesinformer.StatesInformer
}

func (i *informer) HasSynced() bool {
	return i.statesInformer.HasSynced()
}

func (i *informer) ListPods() []*v1.Pod {
	pods := i.statesInformer.GetAllPods()
	result := make([]*v1.Pod, len(pods))
	for i := range pods {
		result[i] = pods[i].Pod
	}
	return result
}

func (i *informer) GetNode() *v1.Node {
	return i.statesInformer.GetNode()
}

type MetricDesc struct {
	UID UIDType
}

type MetricKey int

const (
	CPUUsage MetricKey = iota
	MemoryUsage
)

type MetricServer interface {
	GetPodMetric(desc MetricDesc, m MetricKey) (float64, error)
	GetNodeMetric(desc MetricDesc, m MetricKey) (float64, error)
}

func NewMetricServer(metricCache metriccache.MetricCache) MetricServer {
	return &metricServer{
		metricCache: metricCache,
	}
}

type metricServer struct {
	metricCache metriccache.MetricCache
}

func (ms *metricServer) GetPodMetric(desc MetricDesc, m MetricKey) (float64, error) {
	now := time.Now()
	start := now.Add(-DefaultTrainingInterval)
	querier, err := ms.metricCache.Querier(start, now)
	if err != nil {
		return 0, err
	}

	podProperties := metriccache.MetricPropertiesFunc.Pod(string(desc.UID))
	queryPodMetric := func(m metriccache.MetricResource) (float64, error) {
		meta, err := m.BuildQueryMeta(podProperties)
		if err != nil {
			return 0, err
		}
		result := metriccache.DefaultAggregateResultFactory.New(meta)
		if err = querier.Query(meta, nil, result); err != nil {
			return 0, err
		}
		return result.Value(metriccache.AggregationTypeP90)
	}

	switch m {
	case CPUUsage:
		return queryPodMetric(metriccache.PodCPUUsageMetric)
	case MemoryUsage:
		return queryPodMetric(metriccache.PodMemUsageMetric)
	}

	return 0, fmt.Errorf("unsupported metric: %v", m)
}

func (ms *metricServer) GetNodeMetric(desc MetricDesc, m MetricKey) (float64, error) {
	now := time.Now()
	start := now.Add(-DefaultTrainingInterval)
	querier, err := ms.metricCache.Querier(start, now)
	if err != nil {
		return 0, err
	}

	queryNodeMetric := func(m metriccache.MetricResource) (float64, error) {
		meta, err := m.BuildQueryMeta(nil)
		if err != nil {
			return 0, err
		}
		result := metriccache.DefaultAggregateResultFactory.New(meta)
		if err = querier.Query(meta, nil, result); err != nil {
			return 0, err
		}
		return result.Value(metriccache.AggregationTypeP90)
	}

	switch m {
	case CPUUsage:
		return queryNodeMetric(metriccache.NodeCPUUsageMetric)
	case MemoryUsage:
		return queryNodeMetric(metriccache.NodeMemoryUsageMetric)
	}

	return 0, fmt.Errorf("unsupported metric: %v", m)
}
