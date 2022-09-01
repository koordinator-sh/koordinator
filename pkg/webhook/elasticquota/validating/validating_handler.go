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

	v1 "k8s.io/api/admission/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

type ElasticQuotaValidatingHandler struct {
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder
}

var _ admission.Handler = &ElasticQuotaValidatingHandler{}

func shouldIgnoreIfNotElasticQuotas(req admission.Request) bool {
	// Ignore all calls to sub resources or resources other than pods.
	if len(req.AdmissionRequest.SubResource) != 0 ||
		req.AdmissionRequest.Resource.Resource != "elasticQuotas" {
		return true
	}
	return false
}

func (h *ElasticQuotaValidatingHandler) Handle(ctx context.Context, request admission.Request) (resp admission.Response) {
	if shouldIgnoreIfNotElasticQuotas(request) {
		return admission.Allowed("")
	}

	obj := &v1alpha1.ElasticQuota{}

	var err error
	if request.Operation != v1.Delete {
		if err = h.Decoder.Decode(request, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	} else {
		if len(request.OldObject.Raw) != 0 {
			if err = h.Decoder.DecodeRaw(request.OldObject, obj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
		}
	}

	defer func() {
		if !resp.Allowed {
			klog.Warningf("Webhook finish validating quota %s, allowed: %v, result: %v",
				obj.Name, resp.Allowed, util.DumpJSON(resp.Result))
		}
	}()

	for _, pluginFunc := range defaultValidatingPlugins {
		plugin := pluginFunc(h.Decoder, h.Client)
		klog.V(5).Infof("[quota-validating] quota: %v, plugin: %v, start", obj.Name, plugin.Name())
		if err = plugin.Validate(ctx, request, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		klog.V(5).Infof("[quota-validating] quota: %v, plugin: %v, end", obj.Name, plugin.Name())
	}

	return admission.ValidationResponse(true, "")
}

var _ inject.Client = &ElasticQuotaValidatingHandler{}

// InjectClient injects the client into the ElasticQuotaValidatingHandler
func (h *ElasticQuotaValidatingHandler) InjectClient(c client.Client) error {
	h.Client = c
	return nil
}

var _ admission.DecoderInjector = &ElasticQuotaValidatingHandler{}

// InjectDecoder injects the client into the ElasticQuotaValidatingHandler
func (h *ElasticQuotaValidatingHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}
