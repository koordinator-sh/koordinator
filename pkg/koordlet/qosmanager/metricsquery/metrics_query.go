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
	"time"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

var (
	timeNow = time.Now
)

type MetricsQuery interface {
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
