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
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

var _ services.APIServiceProvider = &Plugin{}

type NodeAssignInfoData struct {
	Pods []PodAssignInfoData `json:"pods,omitempty"`
}

type PodAssignInfoData struct {
	Timestamp time.Time   `json:"timestamp,omitempty"`
	Pod       *corev1.Pod `json:"pod,omitempty"`
}

func (p *Plugin) RegisterEndpoints(group *gin.RouterGroup) {
	group.GET("/node/:nodeName", func(c *gin.Context) {
		nodeName := c.Param("nodeName")
		assignInfo := p.podAssignCache.getPodsAssignInfoOnNode(nodeName)
		if len(assignInfo) == 0 {
			c.JSON(http.StatusOK, &NodeAssignInfoData{})
			return
		}

		resp := &NodeAssignInfoData{
			Pods: make([]PodAssignInfoData, 0, len(assignInfo)),
		}
		for i := range assignInfo {
			resp.Pods = append(resp.Pods, PodAssignInfoData{
				Timestamp: assignInfo[i].timestamp,
				Pod:       assignInfo[i].pod,
			})
		}

		c.JSON(http.StatusOK, resp)
	})
}
