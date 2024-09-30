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

package cpunormalization

import (
	"context"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

var _ handler.EventHandler = &nrtHandler{}

type nrtHandler struct{}

func (h *nrtHandler) Create(ctx context.Context, evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	nrt, ok := evt.Object.(*topologyv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}

	if !isNRTCPUBasicInfoCreated(nrt) {
		return
	}

	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: nrt.Name,
		},
	})
}

func (h *nrtHandler) Update(ctx context.Context, evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	nrtOld, okOld := evt.ObjectOld.(*topologyv1alpha1.NodeResourceTopology)
	nrtNew, okNew := evt.ObjectNew.(*topologyv1alpha1.NodeResourceTopology)
	if !okOld || !okNew {
		return
	}

	if nrtOld.ResourceVersion == nrtNew.ResourceVersion {
		return
	}

	if !isNRTCPUBasicInfoChanged(nrtOld, nrtNew) {
		return
	}

	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: nrtNew.Name,
		},
	})
}

func (h *nrtHandler) Delete(ctx context.Context, evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
}

func (h *nrtHandler) Generic(ctx context.Context, evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func isNRTCPUBasicInfoCreated(nrt *topologyv1alpha1.NodeResourceTopology) bool {
	info, err := extension.GetCPUBasicInfo(nrt.Annotations)
	if err != nil {
		klog.V(4).InfoS("failed to get CPUBasicInfo in created NRT", "node", nrt.Name, "err", err)
		return false
	}
	if info == nil {
		klog.V(6).InfoS("skip node has no CPUBasicInfo in created NRT", "node", nrt.Name)
		return false
	}

	return true
}

func isNRTCPUBasicInfoChanged(nrtOld, nrtNew *topologyv1alpha1.NodeResourceTopology) bool {
	infoNew, err := extension.GetCPUBasicInfo(nrtNew.Annotations)
	if err != nil {
		klog.ErrorS(err, "failed to get new CPUBasicInfo in updated NRT", "node", nrtNew.Name)
		return false
	}
	infoOld, err := extension.GetCPUBasicInfo(nrtOld.Annotations)
	if err != nil { // ignore old error
		klog.V(4).InfoS("aborted to get old CPUBasicInfo in updated NRT", "node", nrtOld.Name, "err", err)
		return true
	}

	isChanged, msg := isCPUBasicInfoChanged(infoOld, infoNew)
	if isChanged {
		klog.V(5).InfoS("got CPUBasicInfo changed in updated NRT", "node", nrtNew.Name, "message", msg)
		return true
	}

	return false
}
