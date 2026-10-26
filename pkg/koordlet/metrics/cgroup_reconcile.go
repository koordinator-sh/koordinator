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

package metrics

import "github.com/prometheus/client_golang/prometheus"

const (
	// MemoryReclaimResultKey is the label key for the result of a memory reclaim round.
	MemoryReclaimResultKey = "result"
	// MemoryReclaimResult constants; recorded as label values on MemoryReclaimRounds.
	MemoryReclaimResultSuccess = "success"
	MemoryReclaimResultGated   = "gated"   // PSI pressure below threshold, reclaim skipped
	MemoryReclaimResultLimited = "limited" // EAGAIN backoff active, reclaim skipped
	MemoryReclaimResultError   = "error"   // memory.reclaim write failed
)

var (
	MemoryReclaimRounds = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: KoordletSubsystem,
		Name:      "memory_reclaim_rounds_total",
		Help:      "Number of BE memory reclaim rounds by result (success/gated/limited/error)",
	}, []string{NodeKey, MemoryReclaimResultKey})

	CgroupReconcileCollector = []prometheus.Collector{
		MemoryReclaimRounds,
	}
)

func RecordMemoryReclaimRound(result string) {
	labels := genNodeLabels()
	if labels == nil {
		return
	}
	labels[MemoryReclaimResultKey] = result
	MemoryReclaimRounds.With(labels).Inc()
}
