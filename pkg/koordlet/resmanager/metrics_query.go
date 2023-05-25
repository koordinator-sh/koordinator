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

func (r *resmanager) collectAllPodMetricsLast(metricResource metriccache.MetricResource) map[string]float64 {
	queryParam := generateQueryParamsLast(r.collectResUsedIntervalSeconds * 2)
	return r.collectAllPodMetrics(*queryParam, metricResource)
}

func (r *resmanager) collectAllPodMetrics(queryParam metriccache.QueryParam, metricResource metriccache.MetricResource) map[string]float64 {
	podsMeta := r.statesInformer.GetAllPods()
	podsMetrics := make(map[string]float64)
	for _, podMeta := range podsMeta {
		queryMeta, err := metricResource.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(podMeta.Pod.UID)))
		if err != nil {
			klog.Warningf("build pod %s/%s query meta failed, kind: %s, error: %v", podMeta.Pod.Namespace, podMeta.Pod.Name, queryMeta.GetKind(), err)
			continue
		}
		podQueryResult, err := r.collectPodMetric(queryMeta, *queryParam.Start, *queryParam.End)
		if err != nil {
			klog.Warningf("query pod %s/%s metric failed, kind: %s, error: %v", podMeta.Pod.Namespace, podMeta.Pod.Name, queryMeta.GetKind(), err)
			continue
		}
		value, err := podQueryResult.Value(queryParam.Aggregate)
		if err != nil {
			klog.Warningf("aggregate pod %s/%s metric failed, kind: %s, error: %v", podMeta.Pod.Namespace, podMeta.Pod.Name, queryMeta.GetKind(), err)
			continue
		}
		podsMetrics[string(podMeta.Pod.UID)] = value

	}
	return podsMetrics
}

func (r *resmanager) collectPodMetricLast(queryMeta metriccache.MetricMeta) (float64, error) {
	queryParam := generateQueryParamsLast(r.collectResUsedIntervalSeconds * 2)
	result, err := r.collectPodMetric(queryMeta, *queryParam.Start, *queryParam.End)
	if err != nil {
		return 0, err
	}
	return result.Value(queryParam.Aggregate)
}

func (r *resmanager) collectPodMetric(queryMeta metriccache.MetricMeta, start, end time.Time) (metriccache.AggregateResult, error) {
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

func (r *resmanager) collectContainerResMetricLast(queryMeta metriccache.MetricMeta) (float64, error) {
	queryParam := generateQueryParamsLast(r.collectResUsedIntervalSeconds * 2)
	querier, err := r.metricCache.Querier(*queryParam.Start, *queryParam.End)
	if err != nil {
		return 0, err
	}
	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err := querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return 0, err
	}
	return aggregateResult.Value(queryParam.Aggregate)
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
