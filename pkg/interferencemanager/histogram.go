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

package interferencemanager

import (
	"math"
	"sort"
	"time"
)

// A Histogram counts individual observations from an event or sample stream in
// configurable buckets. Similar to a summary, it also provides a sum of
// observations and an observation count.
//
// Histogram also provides functions to calculate miu, sigma, percentiles, etc.
//
// Histograms need to implement algorithms to automatically define suitable
// buckets in acceptable accuracy.
//
// To create Histogram instances, use NewHistogram.
type Histogram struct {
	Label HistogramLabels
	Data  HistogramData

	HistogramCalculator
}

type HistogramCalculator interface {
	calculateSigma() float64
	calculateMiu() float64
	calculatePercentile(degree float64) float64
}

// HistogramLabels bundles the options for creating a Histogram metric.
type HistogramLabels struct {
	// A Histogram represents one Container or Pod, related to one workload.
	// And normally it is created every 24 hours(can be configured), aggregating metrics data during this time.
	PodUID      string
	ContainerID string
	Workload    string
	CreateTime  time.Time

	OtherLabels map[string]string
}

type HistogramData struct {
	// BucketUpperBounds defines the buckets into which observations are counted. Each
	// element in the slice is the upper inclusive bound of a bucket. The
	// values must be sorted in strictly increasing order. There is no need
	// to add a highest bucket with +Inf bound, it will be added
	// implicitly.
	BucketUpperBounds []float64

	// Counts' length should be len(BucketUpperBounds) + 1 for the last +Inf bucket
	Counts []float64

	TotalCount float64
}

func (h *Histogram) initBucket(min, max float64, count int) []float64 {
	return nil
}

func (h *Histogram) Record(value float64) {
	bucket := h.findBucket(value)
	h.Data.Counts[bucket] += 1
	h.Data.TotalCount += 1
}

// findBucket returns the index of the bucket for the provided value, or
// len(BucketUpperBounds) for the +Inf bucket.
func (h *Histogram) findBucket(value float64) int {
	// uses binary search, much more effective when histogram has >30 buckets
	return sort.SearchFloat64s(h.Data.BucketUpperBounds, value)
}

// Reset set Histogram Counts to zero, then the same Histogram can be used to record metrics in next time window
func (h *Histogram) Reset() {
	h.Data.Counts = make([]float64, len(h.Data.BucketUpperBounds))
}

// AddHistogram add 'addend' on itself. 'addend' must has same shape with 'h'
func (h *Histogram) AddHistogram(addend Histogram) {
	for i := range h.Data.Counts {
		h.Data.Counts[i] += addend.Data.Counts[i]
	}
}

type ExponentialHistogram struct {
	Histogram
	BucketMidValue []float64
}

func (eh *ExponentialHistogram) NewHistogram(labels HistogramLabels, min, max float64, count int) Histogram {
	if count < 1 {

	}
	if min >= max {

	}
	return Histogram{
		Label: labels,
		Data: HistogramData{
			BucketUpperBounds: eh.initBucket(min, max, count),
			Counts:            make([]float64, count+1),
		},
	}
}

// ExponentialHistogram includes 'count' buckets, where the lowest bucket is
// 'min' and the highest bucket is 'max'. The final +Inf bucket is not included
// in the returned slice. The returned slice is meant to be used for the
// BucketUpperBounds field of HistogramData.
func (eh *ExponentialHistogram) initBucket(min, max float64, count int) []float64 {
	// Formula for exponential buckets.
	// max = min*growthFactor^(bucketCount-1)
	growthFactor := math.Pow(max/min, 1.0/float64(count-1))
	buckets := make([]float64, count)
	for i := 1; i <= count; i++ {
		buckets[i-1] = min * math.Pow(growthFactor, float64(i-1))
	}
	eh.BucketMidValue = make([]float64, count+1)
	eh.BucketMidValue[0] = buckets[0] / 2
	for i := 1; i < count; i++ {
		eh.BucketMidValue[i] = buckets[i-1] + (buckets[i]-buckets[i-1])/2
	}
	eh.BucketMidValue[count] = buckets[count-1] + (buckets[count-1]*growthFactor - buckets[count-1]/2)
	return buckets
}

// Use middle value of a bucket to represent every metric count in this bucket
func (eh *ExponentialHistogram) calculateMiu() float64 {
	var sum float64
	for i := range eh.Data.Counts {
		sum = sum + eh.Data.Counts[i]*eh.BucketMidValue[i]
	}
	return sum / eh.Data.TotalCount
}

func (eh *ExponentialHistogram) calculateSigma() float64 {
	miu := eh.calculateMiu()
	var sum float64
	for i := range eh.Data.Counts {
		sum = sum + eh.Data.Counts[i]*math.Pow(eh.BucketMidValue[i]-miu, 2)
	}
	return sum / eh.Data.TotalCount
}

// E.G., to compute P95 of 10000 metrics, find the 9501th metric placed in the 8th bucket,
// the 8th bucket has 368 metrics and the 9501th metric is the 93rd metric in this bucket,
// then the estimated P95 is BucketLeftBound + BucketWidth * (93/368)
func (eh *ExponentialHistogram) calculatePercentile(degree float64) float64 {
	rank := math.Ceil(degree * eh.Data.TotalCount)
	var bucketStart float64
	countBefore, bucket := eh.findRankBucket(rank)
	if bucket == 0 {
		return eh.Data.BucketUpperBounds[0] * (rank / eh.Data.Counts[0])
	}
	bucketStart = eh.Data.BucketUpperBounds[bucket-1]
	bucketWidth := (eh.BucketMidValue[bucket] - bucketStart) * 2
	return bucketStart + bucketWidth*((rank-countBefore)/eh.Data.Counts[bucket])
}

func (eh *ExponentialHistogram) findRankBucket(rank float64) (float64, int) {
	countSum := 0.0
	for i := range eh.Data.Counts {
		if countSum+eh.Data.Counts[i] >= rank {
			return countSum, i
		}
		countSum += eh.Data.Counts[i]
	}
	// unreachable statement
	return eh.Data.TotalCount - eh.Data.Counts[len(eh.Data.Counts)-1], len(eh.Data.Counts) - 1
}
