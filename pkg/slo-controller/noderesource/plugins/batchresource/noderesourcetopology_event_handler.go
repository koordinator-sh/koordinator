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

package batchresource

import (
	"context"
	"fmt"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var checkedNRTResourceSet = sets.NewString(string(corev1.ResourceCPU), string(corev1.ResourceMemory))

var _ handler.EventHandler = &NRTHandler{}

type NRTHandler struct {
	syncContext *framework.SyncContext
}

func (h *NRTHandler) Create(ctx context.Context, evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	nrt, ok := evt.Object.(*topologyv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}

	if !isNRTResourcesCreated(nrt) {
		return
	}

	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: nrt.Name,
		},
	})
}

func (h *NRTHandler) Update(ctx context.Context, evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	nrtOld, okOld := evt.ObjectOld.(*topologyv1alpha1.NodeResourceTopology)
	nrtNew, okNew := evt.ObjectNew.(*topologyv1alpha1.NodeResourceTopology)
	if !okOld || !okNew {
		return
	}

	if nrtOld.ResourceVersion == nrtNew.ResourceVersion {
		return
	}

	if !isNRTResourcesChanged(nrtOld, nrtNew) {
		return
	}

	q.Add(reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: nrtNew.Name,
		},
	})
}

func (h *NRTHandler) Delete(ctx context.Context, evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	nrt, ok := evt.Object.(*topologyv1alpha1.NodeResourceTopology)
	if !ok {
		return
	}

	if err := cleanupContextForNRT(h.syncContext, nrt); err != nil {
		klog.V(4).InfoS("failed to cleanup syncContext for NRT", "node", nrt.Name, "err", err)
	}
}

func (h *NRTHandler) Generic(ctx context.Context, evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func isNRTResourcesCreated(nrt *topologyv1alpha1.NodeResourceTopology) bool {
	if len(nrt.Zones) <= 0 {
		return false
	}

	// check if any zone has the target resource allocatable
	for _, zone := range nrt.Zones {
		for _, resourceInfo := range zone.Resources {
			if checkedNRTResourceSet.Has(resourceInfo.Name) {
				return true
			}
		}
	}

	return false
}

func isNRTResourcesChanged(nrtOld, nrtNew *topologyv1alpha1.NodeResourceTopology) bool {
	// check if target resources not equal
	return !util.IsZoneListResourceEqual(nrtOld.Zones, nrtNew.Zones, checkedNRTResourceSet.List()...)
}

func cleanupContextForNRT(syncContext *framework.SyncContext, nrt *topologyv1alpha1.NodeResourceTopology) error {
	if syncContext == nil {
		return fmt.Errorf("sync context not initialized")
	}

	// NRT name = node name
	syncContext.Delete(util.GenerateNodeKey(&nrt.ObjectMeta))
	return nil
}
