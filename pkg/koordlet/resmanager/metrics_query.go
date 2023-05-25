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

package resmanager

import (
	"fmt"
	"time"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

var (
	timeNow = time.Now
)

func (r *resmanager) collectorNodeMetricLast(queryMeta metriccache.MetricMeta) (float64, error) {
	queryParam := generateQueryParamsLast(r.collectResUsedIntervalSeconds * 2)
	result, err := r.collectorNodeMetrics(*queryParam.Start, *queryParam.End, queryMeta)
	if err != nil {
		return 0, err
	}
	return result.Value(queryParam.Aggregate)
}

func (r *resmanager) collectorNodeMetrics(start, end time.Time, queryMeta metriccache.MetricMeta) (metriccache.AggregateResult, error) {
	querier, err := r.metricCache.Querier(start, end)
	if err != nil {
		return nil, err
	}

	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err := querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return nil, err
	}
	return aggregateResult, nil
}

// query data for 2 * collectResUsedIntervalSeconds
func (r *resmanager) collectPodMetricLast() []*metriccache.PodResourceMetric {
	queryParam := generateQueryParamsLast(r.collectResUsedIntervalSeconds * 2)
	return r.collectPodMetrics(queryParam)
}

func (r *resmanager) collectPodMetrics(queryParam *metriccache.QueryParam) []*metriccache.PodResourceMetric {
	podsMeta := r.statesInformer.GetAllPods()
	podsMetrics := make([]*metriccache.PodResourceMetric, 0, len(podsMeta))
	for _, podMeta := range podsMeta {
		podQueryResult := r.collectPodMetric(podMeta, queryParam)
		podMetric := podQueryResult.Metric
		if podMetric != nil {
			podsMetrics = append(podsMetrics, podMetric)
		}
	}
	return podsMetrics
}

func (r *resmanager) collectPodMetric(podMeta *statesinformer.PodMeta, queryParam *metriccache.QueryParam) metriccache.PodResourceQueryResult {
	if podMeta == nil || podMeta.Pod == nil {
		return metriccache.PodResourceQueryResult{QueryResult: metriccache.QueryResult{Error: fmt.Errorf("pod is nil")}}
	}
	podUID := string(podMeta.Pod.UID)
	queryResult := r.metricCache.GetPodResourceMetric(&podUID, queryParam)
	if queryResult.Error != nil {
		klog.V(5).Infof("get pod %v resource metric failed, error %v", podUID, queryResult.Error)
		return queryResult
	}
	if queryResult.Metric == nil {
		klog.V(5).Infof("pod %v metric not exist", podUID)
		return queryResult
	}
	return queryResult
}

func (r *resmanager) collectContainerResMetricLast(containerID *string) metriccache.ContainerResourceQueryResult {
	if containerID == nil {
		return metriccache.ContainerResourceQueryResult{
			QueryResult: metriccache.QueryResult{Error: fmt.Errorf("container is nil")},
		}
	}
	queryParam := generateQueryParamsLast(r.collectResUsedIntervalSeconds * 2)
	queryResult := r.metricCache.GetContainerResourceMetric(containerID, queryParam)
	if queryResult.Error != nil {
		klog.Warningf("get container %v resource metric failed, error %v", containerID, queryResult.Error)
		return queryResult
	}
	if queryResult.Metric == nil {
		klog.Warningf("container %v metric not exist", containerID)
		return queryResult
	}
	return queryResult
}

func (r *resmanager) collectContainerThrottledMetric(containerID *string) (metriccache.AggregateResult, error) {
	if containerID == nil {
		return nil, fmt.Errorf("container is nil")
	}

	queryEndTime := time.Now()
	queryStartTime := queryEndTime.Add(-time.Duration(r.collectResUsedIntervalSeconds*5) * time.Second)
	querier, err := r.metricCache.Querier(queryStartTime, queryEndTime)
	if err != nil {
		return nil, err
	}

	queryParam := metriccache.MetricPropertiesFunc.Container(*containerID)
	queryMeta, err := metriccache.ContainerCPUThrottledMetric.BuildQueryMeta(queryParam)
	if err != nil {
		return nil, err
	}

	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err := querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return nil, err
	}

	return aggregateResult, nil
}

func generateQueryParamsAvg(windowSeconds int64) *metriccache.QueryParam {
	end := time.Now()
	start := end.Add(-time.Duration(windowSeconds) * time.Second)
	queryParam := &metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeAVG,
		Start:     &start,
		End:       &end,
	}
	return queryParam
}

func generateQueryParamsLast(windowSeconds int64) *metriccache.QueryParam {
	end := time.Now()
	start := end.Add(-time.Duration(windowSeconds) * time.Second)
	queryParam := &metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeLast,
		Start:     &start,
		End:       &end,
	}
	return queryParam
}
