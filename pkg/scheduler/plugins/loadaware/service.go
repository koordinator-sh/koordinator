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

package loadaware

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

var _ services.APIServiceProvider = &Plugin{}

type NodeAssignInfoData struct {
	Pods []PodAssignInfoData `json:"pods,omitempty"`

	ProdUsage         corev1.ResourceList `json:"prodUsage,omitempty"`
	NodeDelta         corev1.ResourceList `json:"nodeDelta,omitempty"`
	ProdDelta         corev1.ResourceList `json:"prodDelta,omitempty"`
	NodeEstimated     corev1.ResourceList `json:"nodeEstimated,omitempty"`
	NodeDeltaPods     []string            `json:"nodeDeltaPods,omitempty"`
	ProdDeltaPods     []string            `json:"prodDeltaPods,omitempty"`
	NodeEstimatedPods []string            `json:"nodeEstimatedPods,omitempty"`
}

type PodAssignInfoData struct {
	Timestamp time.Time   `json:"timestamp,omitempty"`
	Pod       *corev1.Pod `json:"pod,omitempty"`
}

func (p *Plugin) RegisterEndpoints(group *gin.RouterGroup) {
	group.GET("/node/:nodeName", func(c *gin.Context) {
		nodeName := c.Param("nodeName")
		nodeInfo, assignInfos := p.podAssignCache.getDataOnNode(nodeName)
		if len(assignInfos) == 0 && nodeInfo == nil {
			c.JSON(http.StatusOK, &NodeAssignInfoData{})
			return
		}

		resp := &NodeAssignInfoData{
			Pods: make([]PodAssignInfoData, 0, len(assignInfos)),
		}
		for i := range assignInfos {
			resp.Pods = append(resp.Pods, PodAssignInfoData{
				Timestamp: assignInfos[i].timestamp,
				Pod:       assignInfos[i].pod,
			})
		}
		if nodeInfo != nil {
			resp.ProdUsage = p.vectorizer.ToList(nodeInfo.prodUsage)
			resp.NodeDelta = p.vectorizer.ToList(nodeInfo.nodeDelta)
			resp.ProdDelta = p.vectorizer.ToList(nodeInfo.prodDelta)
			resp.NodeEstimated = p.vectorizer.ToList(nodeInfo.nodeEstimated)
			for _, pod := range nodeInfo.nodeDeltaPods.UnsortedList() {
				resp.NodeDeltaPods = append(resp.NodeDeltaPods, klog.KObj(pod).String())
			}
			sort.Slice(resp.NodeDeltaPods, func(i, j int) bool {
				return resp.NodeDeltaPods[i] < resp.NodeDeltaPods[j]
			})
			for _, pod := range nodeInfo.prodDeltaPods.UnsortedList() {
				resp.ProdDeltaPods = append(resp.ProdDeltaPods, klog.KObj(pod).String())
			}
			sort.Slice(resp.ProdDeltaPods, func(i, j int) bool {
				return resp.ProdDeltaPods[i] < resp.ProdDeltaPods[j]
			})
			for _, pod := range nodeInfo.nodeEstimatedPods.UnsortedList() {
				resp.NodeEstimatedPods = append(resp.NodeEstimatedPods, klog.KObj(pod).String())
			}
			sort.Slice(resp.NodeEstimatedPods, func(i, j int) bool {
				return resp.NodeEstimatedPods[i] < resp.NodeEstimatedPods[j]
			})
		}

		c.JSON(http.StatusOK, resp)
	})
}
