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

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/webhook/node/plugins"
	nodesloconfig "github.com/koordinator-sh/koordinator/pkg/webhook/node/plugins/sloconfig"
)

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

type NodeValidatingHandler struct {
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder
}

func NewNodeValidatingHandler(c client.Client, d *admission.Decoder) *NodeValidatingHandler {
	handler := &NodeValidatingHandler{
		Client:  c,
		Decoder: d,
	}
	return handler
}

var _ admission.Handler = &NodeValidatingHandler{}

func ShouldIgnoreIfNotNode(req admission.Request) bool {
	// Ignore all calls to sub resources or resources other than nodes.
	if len(req.AdmissionRequest.SubResource) != 0 ||
		req.AdmissionRequest.Resource.Resource != "nodes" {
		return true
	}
	return false
}

// Handle handles admission requests.
func (h *NodeValidatingHandler) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	klog.V(3).Infof("enter validating handler,type:%v,name:%v,user:%s", req.Kind, req.Name, req.UserInfo.Username)
	if ShouldIgnoreIfNotNode(req) {
		return admission.ValidationResponse(true, "")
	}

	obj, oldObj := newDecodeObj()
	var err error
	if req.Operation != admissionv1.Delete {
		err = h.Decoder.Decode(req, obj)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	} else {
		if len(req.OldObject.Raw) != 0 {
			if err = h.Decoder.DecodeRaw(req.OldObject, obj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
		}
	}

	if req.Operation == admissionv1.Update {
		err = h.Decoder.DecodeRaw(req.OldObject, oldObj)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	}

	defer func() {
		if !resp.Allowed {
			klog.Warningf("Webhook finish validating info %s, allowed: %v, result: %v",
				obj.Name, resp.Allowed, util.DumpJSON(resp.Result))
		}
	}()

	pls := h.getPlugins()

	for _, plugin := range pls {
		if err = plugin.Validate(ctx, req, obj, oldObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	}

	return admission.ValidationResponse(true, "")
}

func (h *NodeValidatingHandler) getPlugins() []plugins.NodePlugin {
	return []plugins.NodePlugin{nodesloconfig.NewPlugin(h.Decoder, h.Client)}
}

// var _ inject.Client = &NodeValidatingHandler{}

// InjectClient injects the client into the ValidatingHandler
func (h *NodeValidatingHandler) InjectClient(c client.Client) error {
	h.Client = c
	return nil
}

// var _ admission.DecoderInjector = &NodeValidatingHandler{}

// InjectDecoder injects the decoder into the ValidatingHandler
func (h *NodeValidatingHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}

func newDecodeObj() (obj, oldObj *corev1.Node) {
	obj = &corev1.Node{}
	oldObj = &corev1.Node{}
	return obj, oldObj
}
