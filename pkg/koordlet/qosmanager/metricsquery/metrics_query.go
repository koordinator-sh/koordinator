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

package metricsquery

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

type MetricsQuery interface {
	CollectContainerResMetricLast(containerID *string, collectResUsedIntervalSeconds int64) metriccache.ContainerResourceQueryResult

	CollectPodMetric(podMeta *statesinformer.PodMeta, queryParam *metriccache.QueryParam) metriccache.PodResourceQueryResult
}

var _ MetricsQuery = &metricsQuery{}

// NewMetricsQuery creates an instance which implements interface MetricsQuery.
func NewMetricsQuery(metricCache metriccache.MetricCache, statesInformer statesinformer.StatesInformer) MetricsQuery {
	return &metricsQuery{
		metricCache:    metricCache,
		statesInformer: statesInformer,
	}
}

type metricsQuery struct {
	metricCache    metriccache.MetricCache
	statesInformer statesinformer.StatesInformer
}

func (r *metricsQuery) CollectPodMetric(podMeta *statesinformer.PodMeta,
	queryParam *metriccache.QueryParam) metriccache.PodResourceQueryResult {

	if podMeta == nil || podMeta.Pod == nil {
		return metriccache.PodResourceQueryResult{QueryResult: metriccache.QueryResult{Error: fmt.Errorf("pod is nil")}}
	}
	podUID := string(podMeta.Pod.UID)
	queryResult := r.metricCache.GetPodResourceMetric(&podUID, queryParam)
	if queryResult.Error != nil {
		klog.Warningf("get pod %v resource metric failed, error %v", podUID, queryResult.Error)
		return queryResult
	}
	if queryResult.Metric == nil {
		klog.Warningf("pod %v metric not exist", podUID)
		return queryResult
	}
	return queryResult
}

// CollectContainerResMetricLast creates an instance which implements interface MetricsQuery.
func (r *metricsQuery) CollectContainerResMetricLast(containerID *string, collectResUsedIntervalSeconds int64) metriccache.ContainerResourceQueryResult {
	if containerID == nil {
		return metriccache.ContainerResourceQueryResult{
			QueryResult: metriccache.QueryResult{Error: fmt.Errorf("container is nil")},
		}
	}
	queryParam := GenerateQueryParamsLast(collectResUsedIntervalSeconds * 2)
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

func GenerateQueryParamsAvg(windowSeconds int64) *metriccache.QueryParam {
	end := timeNow()
	start := end.Add(-time.Duration(windowSeconds) * time.Second)
	queryParam := &metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeAVG,
		Start:     &start,
		End:       &end,
	}
	return queryParam
}

func GenerateQueryParamsLast(windowSeconds int64) *metriccache.QueryParam {
	end := timeNow()
	start := end.Add(-time.Duration(windowSeconds) * time.Second)
	queryParam := &metriccache.QueryParam{
		Aggregate: metriccache.AggregationTypeLast,
		Start:     &start,
		End:       &end,
	}
	return queryParam
}
