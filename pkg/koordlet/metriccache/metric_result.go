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

package metriccache

import (
	"math"
	"time"

	promstorage "github.com/prometheus/prometheus/storage"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

// MetricResult contains s set of series, it can also produce final result like aggregation value
type MetricResult interface {
	MetricMeta
	// AddSeries receives series and saves in MetricResult, which can be used for generating the final value
	AddSeries(promstorage.Series) error
}

// AggregateResultFactory generates AggregateResult according to MetricMeta
type AggregateResultFactory interface {
	New(meta MetricMeta) AggregateResult
}

var DefaultAggregateResultFactory AggregateResultFactory = &aggregateResultFactory{}

// aggregateResultFactory implements AggregateResultFactory
type aggregateResultFactory struct{}

func (f *aggregateResultFactory) New(meta MetricMeta) AggregateResult {
	return newAggregateResult(meta)
}

// AggregateResult inherits MetricResult, which can also generate value according to the give AggregationType
type AggregateResult interface {
	MetricResult
	Count() int
	Value(t AggregationType) (float64, error)
	TimeRangeDuration() time.Duration
}

var _ AggregateResult = &aggregateResult{}

func newAggregateResult(meta MetricMeta) AggregateResult {
	return &aggregateResult{
		metricKind:       meta.GetKind(),
		metricProperties: meta.GetProperties(),
	}
}

// aggregateResult implements AggregateResult
type aggregateResult struct {
	metricKind       string
	metricProperties map[string]string
	points           []*Point
	metricStart      time.Time
	metricsEnd       time.Time
}

type AggregationType string

const (
	AggregationTypeAVG   AggregationType = "avg"
	AggregationTypeP99   AggregationType = "p99"
	AggregationTypeP95   AggregationType = "P95"
	AggregationTypeP90   AggregationType = "P90"
	AggregationTypeP50   AggregationType = "p50"
	AggregationTypeLast  AggregationType = "last"
	AggregationTypeCount AggregationType = "count"
)

// AggregateParam defines the field name of value and time in series struct
type AggregateParam struct {
	ValueFieldName string
	TimeFieldName  string
}

// AggregationFunc receives a list of series and generate the final value according to AggregateParam
type AggregationFunc func(interface{}, AggregateParam) (float64, error)

func (r *aggregateResult) AddSeries(series promstorage.Series) error {
	r.metricProperties = series.Labels().Map()
	delete(r.metricProperties, r.metricKind)

	tsStart := int64(math.MaxInt64)
	tsEnd := int64(0)

	if r.points == nil {
		r.points = make([]*Point, 0)
	}
	it := series.Iterator()
	for it.Next() {
		if it.Err() != nil {
			return it.Err()
		}
		t, v := it.At()
		r.points = append(r.points, &Point{
			Timestamp: time.UnixMilli(t),
			Value:     v,
		})
		tsStart = util.MinInt64(tsStart, t)
		tsEnd = util.MaxInt64(tsEnd, t)
	}
	r.metricStart = time.UnixMilli(tsStart)
	r.metricsEnd = time.UnixMilli(tsEnd)
	return nil
}

func (r *aggregateResult) GetKind() string {
	return r.metricKind
}

func (r *aggregateResult) GetProperties() map[string]string {
	return r.metricProperties
}

// Count return the size of metric series
func (r *aggregateResult) Count() int {
	return len(r.points)
}

// Value returns the result by AggregationType
func (r *aggregateResult) Value(t AggregationType) (float64, error) {
	aggregateFunc := getAggregateFunc(t)
	return aggregateFunc(r.points, pointsDefaultAggregateParam)
}

// TimeRangeDuration returns the time duration of metric series
func (r *aggregateResult) TimeRangeDuration() time.Duration {
	if r != nil {
		return r.metricsEnd.Sub(r.metricStart)
	}
	return time.Duration(0)
}

var pointsDefaultAggregateParam = AggregateParam{
	ValueFieldName: "Value",
	TimeFieldName:  "Timestamp",
}

func getAggregateFunc(aggregationType AggregationType) AggregationFunc {
	switch aggregationType {
	case AggregationTypeAVG:
		return fieldAvgOfMetricList
	case AggregationTypeP99:
		return percentileFuncOfMetricList(0.99)
	case AggregationTypeP95:
		return percentileFuncOfMetricList(0.95)
	case AggregationTypeP90:
		return percentileFuncOfMetricList(0.9)
	case AggregationTypeP50:
		return percentileFuncOfMetricList(0.5)
	case AggregationTypeLast:
		return fieldLastOfMetricList
	case AggregationTypeCount:
		return fieldCountOfMetricList
	default:
		return fieldAvgOfMetricList
	}
}

func count(metrics interface{}) (float64, error) {
	aggregateFunc := getAggregateFunc(AggregationTypeCount)
	return aggregateFunc(metrics, AggregateParam{})
}
