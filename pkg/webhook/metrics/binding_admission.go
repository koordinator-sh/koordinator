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

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	DecisionKey = "decision"

	DecisionAllowedByUserName = "allowed_username"
	DecisionDenied            = "denied"
	DecisionDryRun            = "dry_run"
	DecisionBypassLabel       = "bypass_label"
	DecisionBypassAnnotation  = "bypass_annotation"
	DecisionExcluded          = "excluded_ns"
	DecisionOutOfScope        = "out_of_scope"
)

var (
	BindingAdmissionDecisions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: KoordManagerWebhookSubsystem,
			Name:      "binding_admission_decisions_total",
			Help:      "Total number of binding admission decisions.",
		},
		[]string{DecisionKey},
	)
	BindingAdmissionCollectors = []prometheus.Collector{
		BindingAdmissionDecisions,
	}
)

func RecordBindingAdmissionDecision(decision string) {
	BindingAdmissionDecisions.WithLabelValues(decision).Inc()
}
