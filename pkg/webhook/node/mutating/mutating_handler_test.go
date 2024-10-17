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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/scheme"
	"github.com/koordinator-sh/koordinator/pkg/webhook/node/plugins"
)

type mockNodePlugin struct {
}

func (n *mockNodePlugin) Name() string {
	return "mockNodeResourceAmplificationPlugin"
}

func (n *mockNodePlugin) Validate(ctx context.Context, req admission.Request, node, oldNode *corev1.Node) error {
	return fmt.Errorf("not implemented")
}

// Admit makes an admission decision based on the request attributes
func (n *mockNodePlugin) Admit(ctx context.Context, req admission.Request, node, oldNode *corev1.Node) error {
	if req.Operation == admissionv1.Delete {
		return nil
	}

	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations["fake-key"] = "fake-value"
	return nil
}

func TestNodeMutatingHandler_Handle(t *testing.T) {
	decoder := admission.NewDecoder(scheme.Scheme)
	handler := NewNodeStatusMutatingHandler(fake.NewClientBuilder().Build(), decoder)

	mockRequest := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Resource: metav1.GroupVersionResource{
				Resource: "nodes",
			},
			SubResource: "status",
			Operation:   admissionv1.Update,
		},
	}

	t.Run("NormalMutation", func(t *testing.T) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
			Spec:       corev1.NodeSpec{ /* populate as needed */ },
		}
		nodeRaw, _ := json.Marshal(node)

		mockPlugin := &mockNodePlugin{}
		nodeMutatingPlugins = []plugins.NodePlugin{mockPlugin}
		mockRequest.Object = runtime.RawExtension{Raw: nodeRaw}
		mockRequest.OldObject = runtime.RawExtension{Raw: nodeRaw}

		result := handler.Handle(context.Background(), mockRequest)

		assert.Equal(t, true, result.AdmissionResponse.Allowed)
	})

	t.Run("IgnoreRequest", func(t *testing.T) {
		otherResourceRequest := mockRequest
		otherResourceRequest.Resource.Resource = "pods"

		result := handler.Handle(context.Background(), otherResourceRequest)
		assert.Equal(t, admission.Allowed(""), result)
	})

	t.Run("HandleDeleteOperation", func(t *testing.T) {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		}
		nodeRaw, _ := json.Marshal(node)

		mockRequest.OldObject = runtime.RawExtension{Raw: nodeRaw}
		mockRequest.Operation = admissionv1.Delete

		result := handler.Handle(context.Background(), mockRequest)
		assert.Equal(t, admission.Allowed(""), result)
	})
}
