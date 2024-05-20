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

package mutating

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/webhook/node/plugins"
	"github.com/koordinator-sh/koordinator/pkg/webhook/node/plugins/resourceamplification"
)

var (
	nodeMutatingPlugins = []plugins.NodePlugin{
		resourceamplification.NewPlugin(),
	}
)

type IgnoreFilter func(req admission.Request) bool

// NodeMutatingHandler handles Node
type NodeMutatingHandler struct {
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder

	ignoreFilter IgnoreFilter
}

/* Uncomment the following lines if you want to enable mutating for node
// NewNodeMutatingHandler creates a new handler for node/status.
func NewNodeMutatingHandler() *NodeMutatingHandler {
	handler := &NodeMutatingHandler{
		ignoreFilter: shouldIgnoreIfNotNode,
	}
	return handler
}

func shouldIgnoreIfNotNode(req admission.Request) bool {
	// Ignore all calls to nodes status or resources other than node.
	if len(req.AdmissionRequest.SubResource) != 0 || req.AdmissionRequest.Resource.Resource != "nodes"{
		return true
	}
	return false
}
*/

// NewNodeStatusMutatingHandler creates a new handler for node/status.
func NewNodeStatusMutatingHandler(c client.Client, d *admission.Decoder) *NodeMutatingHandler {
	handler := &NodeMutatingHandler{
		ignoreFilter: shouldIgnoreIfNotNodeStatus,
		Client:       c,
		Decoder:      d,
	}
	return handler
}

var _ admission.Handler = &NodeMutatingHandler{}

func shouldIgnoreIfNotNodeStatus(req admission.Request) bool {
	// Ignore all calls to nodes or resources other than node status.
	return req.AdmissionRequest.Resource.Resource != "nodes" || req.AdmissionRequest.SubResource != "status"
}

// Handle handles admission requests.
func (h *NodeMutatingHandler) Handle(ctx context.Context, req admission.Request) (resp admission.Response) {
	if h.ignoreFilter(req) {
		return admission.Allowed("")
	}

	obj := &corev1.Node{}
	var oldObj *corev1.Node

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
		oldObj = &corev1.Node{}
		err = h.Decoder.DecodeRaw(req.OldObject, oldObj)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	}

	clone := obj.DeepCopy()

	for _, plugin := range nodeMutatingPlugins {
		if err := plugin.Admit(ctx, req, obj, oldObj); err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
	}

	if reflect.DeepEqual(obj, clone) {
		return admission.Allowed("")
	}
	marshaled, err := json.Marshal(obj)
	if err != nil {
		klog.Errorf("Failed to marshal mutated Node %s, err: %v", obj.Name, err)
		return admission.Errored(http.StatusInternalServerError, err)
	}
	original, err := json.Marshal(clone)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(original, marshaled)
}

// var _ inject.Client = &NodeMutatingHandler{}

// InjectClient injects the client into the PodMutatingHandler
func (n *NodeMutatingHandler) InjectClient(c client.Client) error {
	n.Client = c
	return nil
}

// var _ admission.DecoderInjector = &NodeMutatingHandler{}

// InjectDecoder injects the decoder into the PodMutatingHandler
func (n *NodeMutatingHandler) InjectDecoder(d *admission.Decoder) error {
	n.Decoder = d
	return nil
}
