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

package reservation

import (
	"net/http"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

var _ services.APIServiceProvider = &Plugin{}

type ReservationItem struct {
	Name      string    `json:"name,omitempty"`
	Namespace string    `json:"namespace,omitempty"`
	UID       types.UID `json:"uid,omitempty"`
	Kind      string    `json:"kind,omitempty"`

	Available        bool                                         `json:"available,omitempty"`
	AllocateOnce     bool                                         `json:"allocateOnce,omitempty"`
	Allocatable      corev1.ResourceList                          `json:"allocatable,omitempty"`
	Allocated        corev1.ResourceList                          `json:"allocated,omitempty"`
	AllocatablePorts framework.HostPortInfo                       `json:"allocatablePorts,omitempty"`
	AllocatedPorts   framework.HostPortInfo                       `json:"allocatedPorts,omitempty"`
	Owners           []schedulingv1alpha1.ReservationOwner        `json:"owners,omitempty"`
	AllocatePolicy   schedulingv1alpha1.ReservationAllocatePolicy `json:"allocatePolicy,omitempty"`
	AssignedPods     []*frameworkext.PodRequirement               `json:"assignedPods,omitempty"`
}

type NodeReservations struct {
	Items []ReservationItem `json:"items,omitempty"`
}

func (pl *Plugin) RegisterEndpoints(group *gin.RouterGroup) {
	group.GET("/nodeReservations/:nodeName", func(c *gin.Context) {
		nodeName := c.Param("nodeName")
		rInfos := pl.reservationCache.listAvailableReservationInfosOnNode(nodeName)
		if len(rInfos) == 0 {
			c.JSON(http.StatusOK, &NodeReservations{Items: []ReservationItem{}})
			return
		}

		resp := &NodeReservations{
			Items: make([]ReservationItem, 0, len(rInfos)),
		}

		for _, r := range rInfos {
			item := ReservationItem{
				Name:             r.GetName(),
				Namespace:        r.GetNamespace(),
				UID:              r.UID(),
				AllocateOnce:     r.IsAllocateOnce(),
				Available:        r.IsAvailable(),
				Owners:           r.GetPodOwners(),
				AllocatePolicy:   r.GetAllocatePolicy(),
				Allocatable:      r.Allocatable,
				Allocated:        r.Allocated,
				AllocatablePorts: r.AllocatablePorts,
				AllocatedPorts:   r.AllocatedPorts,
			}
			switch r.GetObject().(type) {
			case *schedulingv1alpha1.Reservation:
				item.Kind = "Reservation"
			case *corev1.Pod:
				item.Kind = "Pod"
			default:
				item.Kind = "Unknown"
			}
			for _, p := range r.AssignedPods {
				item.AssignedPods = append(item.AssignedPods, p)
			}
			resp.Items = append(resp.Items, item)
		}
		c.JSON(http.StatusOK, resp)
	})
}
