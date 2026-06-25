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
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	WebhookDurationMilliseconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: KoordManagerWebhookSubsystem,
			Name:      "webhook_duration_milliseconds",
			Help:      "webhook_duration_milliseconds",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 20),
		},
		[]string{WebhookTypeKey, ObjectTypeKey, OperationKey, PluginNameKey, StatusKey},
	)
	WebhookDurationCollectors = []prometheus.Collector{
		WebhookDurationMilliseconds,
	}
)

func getErrorCode(err error) string {
	if err == nil {
		return StatusAllowed
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return StatusTimeout
	}

	reason := apierrors.ReasonForError(err)
	if reason != metav1.StatusReasonUnknown {
		switch reason {
		case metav1.StatusReasonInvalid:
			return StatusValidationFailed
		case metav1.StatusReasonTimeout:
			return StatusTimeout
		case metav1.StatusReasonForbidden:
			return StatusPolicyDenied
		case metav1.StatusReasonInternalError:
			return StatusInternalError
		default:
			return string(reason)
		}
	}

	return StatusRejected
}

func RecordWebhookDurationMilliseconds(webhookType, objectType, operation string, err error, pluginName string, seconds float64) {
	labels := prometheus.Labels{}
	labels[WebhookTypeKey] = webhookType
	labels[ObjectTypeKey] = objectType
	labels[OperationKey] = operation
	labels[PluginNameKey] = pluginName
	labels[StatusKey] = getErrorCode(err)

	WebhookDurationMilliseconds.With(labels).Observe(seconds * 1000)
}
