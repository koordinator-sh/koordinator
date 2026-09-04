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
			Help:      "Webhook duration in milliseconds, labeled by webhook type, object type, operation, plugin name, and status. Status values include allowed, rejected, validation_failed, timeout, canceled, policy_denied, internal_error, and normalized Kubernetes API status reasons.",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 20),
		},
		[]string{WebhookTypeKey, ObjectTypeKey, OperationKey, PluginNameKey, StatusKey},
	)
	WebhookDurationCollectors = []prometheus.Collector{
		WebhookDurationMilliseconds,
	}
)

var statusReasonLabels = map[metav1.StatusReason]string{
	metav1.StatusReasonUnauthorized:          StatusUnauthorized,
	metav1.StatusReasonForbidden:             StatusPolicyDenied,
	metav1.StatusReasonNotFound:              StatusNotFound,
	metav1.StatusReasonAlreadyExists:         StatusAlreadyExists,
	metav1.StatusReasonConflict:              StatusConflict,
	metav1.StatusReasonGone:                  StatusGone,
	metav1.StatusReasonInvalid:               StatusValidationFailed,
	metav1.StatusReasonServerTimeout:         StatusTimeout,
	metav1.StatusReasonStoreReadError:        StatusStorageReadError,
	metav1.StatusReasonTimeout:               StatusTimeout,
	metav1.StatusReasonTooManyRequests:       StatusTooManyRequests,
	metav1.StatusReasonBadRequest:            StatusBadRequest,
	metav1.StatusReasonMethodNotAllowed:      StatusMethodNotAllowed,
	metav1.StatusReasonNotAcceptable:         StatusNotAcceptable,
	metav1.StatusReasonRequestEntityTooLarge: StatusRequestEntityTooLarge,
	metav1.StatusReasonUnsupportedMediaType:  StatusUnsupportedMediaType,
	metav1.StatusReasonInternalError:         StatusInternalError,
	metav1.StatusReasonExpired:               StatusExpired,
	metav1.StatusReasonServiceUnavailable:    StatusServiceUnavailable,
}

func statusForError(err error) string {
	if err == nil {
		return StatusAllowed
	}

	if errors.Is(err, context.DeadlineExceeded) || apierrors.IsServerTimeout(err) {
		return StatusTimeout
	}

	if errors.Is(err, context.Canceled) {
		return StatusCanceled
	}

	if status, ok := statusReasonLabels[apierrors.ReasonForError(err)]; ok {
		return status
	}

	return StatusRejected
}

func RecordWebhookDurationMilliseconds(webhookType, objectType, operation string, err error, pluginName string, seconds float64) {
	labels := prometheus.Labels{}
	labels[WebhookTypeKey] = webhookType
	labels[ObjectTypeKey] = objectType
	labels[OperationKey] = operation
	labels[PluginNameKey] = pluginName
	labels[StatusKey] = statusForError(err)

	WebhookDurationMilliseconds.With(labels).Observe(seconds * 1000)
}
