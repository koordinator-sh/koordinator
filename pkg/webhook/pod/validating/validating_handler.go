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

package validating

import (
	"context"
	"net/http"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
	"github.com/koordinator-sh/koordinator/pkg/webhook/metrics"
	"github.com/koordinator-sh/koordinator/pkg/webhook/quotaevaluate"
)

const (
	ClusterReservation       = "ClusterReservation"
	ClusterColocationProfile = "ClusterColocationProfile"
	EvaluateQuota            = "EvaluateQuota"
	DeviceResource           = "DeviceResource"
	EnhancedValidation       = "EnhancedValidation"
)

// PodValidatingHandler handles Pod
type PodValidatingHandler struct {
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder

	// QuotaEvaluator evaluate pod quota usage
	QuotaEvaluator quotaevaluate.Evaluator

	// PodEnhancedValidator manages pod enhanced validation configuration
	PodEnhancedValidator *PodEnhancedValidator
}

var _ admission.Handler = &PodValidatingHandler{}

func shouldIgnoreIfNotPod(req admission.Request) bool {
	// Ignore all calls to sub resources or resources other than pods.
	if len(req.AdmissionRequest.SubResource) != 0 ||
		req.AdmissionRequest.Resource.Resource != "pods" {
		return true
	}
	return false
}

func (h *PodValidatingHandler) validatingPodFn(ctx context.Context, req admission.Request) (allowed bool, reason string, err error) {
	allowed = true
	if shouldIgnoreIfNotPod(req) {
		return
	}
	if req.Operation == admissionv1.Delete && len(req.OldObject.Raw) == 0 {
		klog.Warningf("Skip to validate pod %s/%s deletion for no old object, maybe because of Kubernetes version < 1.16", req.Namespace, req.Name)
		return
	}
	pod := &corev1.Pod{}
	if err = h.Decoder.DecodeRaw(req.Object, pod); err != nil {
		return false, "", err
	}

	start := time.Now()
	_, reason, err = h.clusterReservationValidatingPod(ctx, req)
	metrics.RecordWebhookDurationMilliseconds(metrics.ValidatingWebhook,
		metrics.Pod, string(req.Operation), err, ClusterReservation, time.Since(start).Seconds())
	if err != nil {
		return false, reason, err
	}

	start = time.Now()
	_, reason, err = h.clusterColocationProfileValidatingPod(ctx, req)
	metrics.RecordWebhookDurationMilliseconds(metrics.ValidatingWebhook,
		metrics.Pod, string(req.Operation), err, ClusterColocationProfile, time.Since(start).Seconds())
	if err != nil {
		return false, reason, err
	}

	start = time.Now()
	plugin := elasticquota.NewPlugin(h.Decoder, h.Client)
	if err = plugin.ValidatePod(ctx, req); err != nil {
		metrics.RecordWebhookDurationMilliseconds(metrics.ValidatingWebhook,
			metrics.Pod, string(req.Operation), err, plugin.Name(), time.Since(start).Seconds())
		return false, "", err
	}
	metrics.RecordWebhookDurationMilliseconds(metrics.ValidatingWebhook,
		metrics.Pod, string(req.Operation), nil, plugin.Name(), time.Since(start).Seconds())

	start = time.Now()
	_, reason, err = h.evaluateQuota(ctx, req)
	metrics.RecordWebhookDurationMilliseconds(metrics.ValidatingWebhook,
		metrics.Pod, string(req.Operation), err, EvaluateQuota, time.Since(start).Seconds())

	if err != nil {
		return false, reason, err
	}

	start = time.Now()
	allowed, reason, err = h.deviceResourceValidatingPod(ctx, req)
	metrics.RecordWebhookDurationMilliseconds(metrics.ValidatingWebhook,
		metrics.Pod, string(req.Operation), err, DeviceResource, time.Since(start).Seconds())
	if err != nil {
		return false, reason, err
	}

	start = time.Now()
	reason, err = h.podEnhancedValidate(ctx, req)
	metrics.RecordWebhookDurationMilliseconds(metrics.ValidatingWebhook,
		metrics.Pod, string(req.Operation), err, EnhancedValidation, time.Since(start).Seconds())
	if err != nil {
		return false, reason, err
	}

	return
}

var _ admission.Handler = &PodValidatingHandler{}

// Handle handles admission requests.
func (h *PodValidatingHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	allowed, reason, err := h.validatingPodFn(ctx, req)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	return admission.ValidationResponse(allowed, reason)
}

// var _ inject.Client = &PodValidatingHandler{}

// InjectClient injects the client into the PodValidatingHandler
func (h *PodValidatingHandler) InjectClient(c client.Client) error {
	h.Client = c
	return nil
}

// var _ admission.DecoderInjector = &PodValidatingHandler{}

// InjectDecoder injects the decoder into the PodValidatingHandler
func (h *PodValidatingHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}
