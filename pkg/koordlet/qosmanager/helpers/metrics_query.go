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

package helpers

import (
	"fmt"
	"time"

	"k8s.io/klog/v2"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

var (
	timeNow = time.Now
)

func CollectorNodeMetricLast(metricCache metriccache.MetricCache, queryMeta metriccache.MetricMeta, metricCollectInterval time.Duration) (float64, error) {
	queryParam := GenerateQueryParamsLast(metricCollectInterval * 2)
	result, err := CollectNodeMetrics(metricCache, *queryParam.Start, *queryParam.End, queryMeta)
	if err != nil {
		return 0, err
	}
	return result.Value(queryParam.Aggregate)
}

func CollectNodeMetrics(metricCache metriccache.MetricCache, start, end time.Time, queryMeta metriccache.MetricMeta) (metriccache.AggregateResult, error) {
	querier, err := metricCache.Querier(start, end)
	if err != nil {
		return nil, err
	}

	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err := querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return nil, err
	}
	return aggregateResult, nil
}

func CollectAllHostAppMetricsLast(hostApps []slov1alpha1.HostApplicationSpec, metricCache metriccache.MetricCache,
	metricResource metriccache.MetricResource, metricCollectInterval time.Duration) map[string]float64 {
	queryParam := GenerateQueryParamsLast(metricCollectInterval * 2)
	return CollectAllHostAppMetrics(hostApps, metricCache, *queryParam, metricResource)
}

func CollectAllHostAppMetrics(hostApps []slov1alpha1.HostApplicationSpec, metricCache metriccache.MetricCache,
	queryParam metriccache.QueryParam, metricResource metriccache.MetricResource) map[string]float64 {
	appsMetrics := make(map[string]float64)
	querier, err := metricCache.Querier(*queryParam.Start, *queryParam.End)
	if err != nil {
		klog.Warningf("build host application querier failed, error: %v", err)
		return appsMetrics
	}
	for _, hostApp := range hostApps {
		queryMeta, err := metricResource.BuildQueryMeta(metriccache.MetricPropertiesFunc.HostApplication(hostApp.Name))
		if err != nil || queryMeta == nil {
			klog.Warningf("build host application %s query meta failed, kind: %s, error: %v", hostApp.Name, queryMeta, err)
			continue
		}
		aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
		if err := querier.Query(queryMeta, nil, aggregateResult); err != nil {
			klog.Warningf("query host application %s metric failed, kind: %s, error: %v", hostApp.Name, queryMeta.GetKind(), err)
			continue
		}
		if aggregateResult.Count() == 0 {
			klog.V(5).Infof("query host application %s metric is empty, kind: %s", hostApp.Name, queryMeta.GetKind())
			continue
		}
		value, err := aggregateResult.Value(queryParam.Aggregate)
		if err != nil {
			klog.Warningf("aggregate host application %s metric failed, kind: %s, error: %v", hostApp.Name, queryMeta.GetKind(), err)
			continue
		}
		appsMetrics[hostApp.Name] = value
	}
	return appsMetrics
}

func CollectAllPodMetricsLast(statesInformer statesinformer.StatesInformer, metricCache metriccache.MetricCache,
	metricResource metriccache.MetricResource, metricCollectInterval time.Duration) map[string]float64 {
	queryParam := GenerateQueryParamsLast(metricCollectInterval * 2)
	return CollectAllPodMetrics(statesInformer, metricCache, *queryParam, metricResource)
}

func CollectAllPodMetrics(statesInformer statesinformer.StatesInformer, metricCache metriccache.MetricCache,
	queryParam metriccache.QueryParam, metricResource metriccache.MetricResource) map[string]float64 {
	podsMeta := statesInformer.GetAllPods()
	podsMetrics := make(map[string]float64)
	for _, podMeta := range podsMeta {
		queryMeta, err := metricResource.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(string(podMeta.Pod.UID)))
		if err != nil {
			klog.Warningf("build pod %s/%s query meta failed, kind: %s, error: %v", podMeta.Pod.Namespace, podMeta.Pod.Name, queryMeta.GetKind(), err)
			continue
		}
		podQueryResult, err := CollectPodMetric(metricCache, queryMeta, *queryParam.Start, *queryParam.End)
		if err != nil {
			klog.Warningf("query pod %s/%s metric failed, kind: %s, error: %v", podMeta.Pod.Namespace, podMeta.Pod.Name, queryMeta.GetKind(), err)
			continue
		}
		if podQueryResult.Count() == 0 {
			klog.V(5).Infof("query pod %s/%s metric is empty, kind: %s", podMeta.Pod.Namespace, podMeta.Pod.Name, queryMeta.GetKind())
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

func CollectPodMetricLast(metricCache metriccache.MetricCache, queryMeta metriccache.MetricMeta,
	metricCollectInterval time.Duration) (float64, error) {
	queryParam := GenerateQueryParamsLast(metricCollectInterval * 2)
	result, err := CollectPodMetric(metricCache, queryMeta, *queryParam.Start, *queryParam.End)
	if err != nil {
		return 0, err
	}
	return result.Value(queryParam.Aggregate)
}

func CollectPodMetric(metricCache metriccache.MetricCache, queryMeta metriccache.MetricMeta, start, end time.Time) (metriccache.AggregateResult, error) {
	querier, err := metricCache.Querier(start, end)
	if err != nil {
		return nil, err
	}
	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err := querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return nil, err
	}
	return aggregateResult, nil
}

func CollectContainerResMetricLast(metricCache metriccache.MetricCache, queryMeta metriccache.MetricMeta,
	metricCollectInterval time.Duration) (float64, error) {
	queryParam := GenerateQueryParamsLast(metricCollectInterval * 2)
	querier, err := metricCache.Querier(*queryParam.Start, *queryParam.End)
	if err != nil {
		return 0, err
	}
	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err := querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return 0, err
	}
	return aggregateResult.Value(queryParam.Aggregate)
}

func CollectContainerThrottledMetric(metricCache metriccache.MetricCache, containerID *string,
	metricCollectInterval time.Duration) (metriccache.AggregateResult, error) {
	if containerID == nil {
		return nil, fmt.Errorf("container is nil")
	}

	queryEndTime := time.Now()
	queryStartTime := queryEndTime.Add(-metricCollectInterval)
	querier, err := metricCache.Querier(queryStartTime, queryEndTime)
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

func GenerateQueryParamsAvg(windowDuration time.Duration) *metriccache.QueryParam {
	end := time.Now()
	start := end.Add(-windowDuration)
	queryParam := &metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeAVG,
		Start:     &start,
		End:       &end,
	}
	return queryParam
}

func GenerateQueryParamsLast(windowDuration time.Duration) *metriccache.QueryParam {
	end := time.Now()
	start := end.Add(-windowDuration)
	queryParam := &metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeLast,
		Start:     &start,
		End:       &end,
	}
	return queryParam
}

func Query(querier metriccache.Querier, resource metriccache.MetricResource, properties map[metriccache.MetricProperty]string) (metriccache.AggregateResult, error) {
	queryMeta, err := resource.BuildQueryMeta(properties)
	if err != nil {
		return nil, err
	}

	aggregateResult := metriccache.DefaultAggregateResultFactory.New(queryMeta)
	if err := querier.Query(queryMeta, nil, aggregateResult); err != nil {
		return nil, err
	}

	return aggregateResult, nil
}
