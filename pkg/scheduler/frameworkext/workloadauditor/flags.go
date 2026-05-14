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

package workloadauditor

import (
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// WorkloadAuditorConfig holds configuration for the workloadAuditorImpl.
var (
	WorkloadAuditorEnabled      = false
	WorkloadAuditorMetricLabels = "" // comma-separated metricLabel=podLabelKey pairs

	// Anomaly detection thresholds
	VictimRescheduleDuration = 10 * time.Second
	VictimDeletionDuration   = 30 * time.Second
	VictimDeletingRetries    = 3
	SchedulingEventInterval  = 5 * time.Minute
)

// AddFlags registers the workloadAuditorImpl command-line flags.
func AddFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&WorkloadAuditorEnabled, "enable-workload-auditor", WorkloadAuditorEnabled, "enable workload auditor for tracking scheduling lifecycle and detecting anomalies")
	fs.StringVar(&WorkloadAuditorMetricLabels, "workload-auditor-metric-labels", WorkloadAuditorMetricLabels, "comma-separated metricLabel=podLabelKey pairs for metric dimensions, e.g. quota_name=quota.scheduling.koordinator.sh/name")
	fs.DurationVar(&VictimRescheduleDuration, "workload-auditor-victim-reschedule-duration", VictimRescheduleDuration, "max time from victimAllDeleted to next scheduling result before ALERT")
	fs.DurationVar(&VictimDeletionDuration, "workload-auditor-victim-deletion-duration", VictimDeletionDuration, "max time from preemptNominated to victimAllDeleted before ALERT")
	fs.IntVar(&VictimDeletingRetries, "workload-auditor-victim-deleting-retries", VictimDeletingRetries, "max preemptVictimDeleting count per preemption cycle before ALERT")
	fs.DurationVar(&SchedulingEventInterval, "workload-auditor-scheduling-event-interval", SchedulingEventInterval, "max time between consecutive scheduling events within a dequeue round before ALERT")
}

type WorkloadAuditorConfig struct {
	Enabled                  bool
	VictimRescheduleDuration time.Duration
	VictimDeletionDuration   time.Duration
	VictimDeletingRetries    int
	SchedulingEventInterval  time.Duration
	MetricLabelNames         []string // ["priority", "gpu", "quota_name", ...]
	PodLabelKeys             []string // parallel to MetricLabelNames[1:]
}

// parseMetricLabels parses the comma-separated metricLabel=podLabelKey pairs.
// Returns the full label name list (always starting with "priority") and the
// parallel pod-label-key list (for custom labels only).
func parseMetricLabels(raw string) (names []string, podKeys []string) {
	names = []string{"priority"}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		key := strings.TrimSpace(parts[1])
		if name == "" || key == "" {
			continue
		}
		names = append(names, name)
		podKeys = append(podKeys, key)
	}
	return
}

// DefaultWorkloadAuditorConfig returns the configuration built from package-level vars.
func DefaultWorkloadAuditorConfig() WorkloadAuditorConfig {
	names, podKeys := parseMetricLabels(WorkloadAuditorMetricLabels)
	return WorkloadAuditorConfig{
		Enabled:                  WorkloadAuditorEnabled,
		VictimRescheduleDuration: VictimRescheduleDuration,
		VictimDeletionDuration:   VictimDeletionDuration,
		VictimDeletingRetries:    VictimDeletingRetries,
		SchedulingEventInterval:  SchedulingEventInterval,
		MetricLabelNames:         names,
		PodLabelKeys:             podKeys,
	}
}
