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

package nodenumaresource

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

var _ services.APIServiceProvider = &Plugin{}

type NodeResponse struct {
	Name                       string `json:"name,omitempty"`
	TopologyOptions            `json:",inline"`
	RemainingNUMANodeResources []NUMANodeResource `json:"remainingNUMANodeResources"`
	AllocatedNUMANodeResources []NUMANodeResource `json:"allocatedNUMANodeResources"`
	AvailableCPUs              cpuset.CPUSet      `json:"availableCPUs"`
	AllocatedCPUs              CPUDetails         `json:"allocatedCPUs"`
	AllocatedPods              []PodAllocation    `json:"allocatedPods"`
}

func (p *Plugin) RegisterEndpoints(group *gin.RouterGroup) {
	group.GET("/nodes/:nodeName", func(c *gin.Context) {
		nodeName := c.Param("nodeName")
		nodeLister := p.handle.SharedInformerFactory().Core().V1().Nodes().Lister()
		node, err := nodeLister.Get(nodeName)
		if err != nil {
			services.ResponseErrorMessage(c, http.StatusInternalServerError, err.Error())
			return
		}

		topologyOptions := p.topologyOptionsManager.GetTopologyOptions(nodeName)
		if !topologyOptions.CPUTopology.IsValid() {
			services.ResponseErrorMessage(c, http.StatusInternalServerError, "invalid topology, please check the NodeResourceTopology object")
			return
		}
		topologyOptions.NUMATopologyPolicy = getNUMATopologyPolicy(node.Labels, topologyOptions.NUMATopologyPolicy)
		if err := amplifyNUMANodeResources(node, &topologyOptions); err != nil {
			services.ResponseErrorMessage(c, http.StatusInternalServerError, "failed to amplify NUMANode Resources, err: %v", err)
			return
		}

		nodeAllocation := p.resourceManager.GetNodeAllocation(nodeName)
		if nodeAllocation == nil {
			services.ResponseErrorMessage(c, http.StatusNotFound, "cannot find target node")
			return
		}
		resp := dumpNodeAllocation(nodeAllocation, topologyOptions)
		c.JSON(http.StatusOK, resp)
	})
}

func dumpNodeAllocation(nodeAllocation *NodeAllocation, topologyOptions TopologyOptions) *NodeResponse {
	resp := &NodeResponse{
		Name:            nodeAllocation.nodeName,
		TopologyOptions: topologyOptions,
	}

	nodeAllocation.lock.RLock()
	defer nodeAllocation.lock.RUnlock()
	if len(nodeAllocation.allocatedPods) != 0 {
		podAllocations := make([]PodAllocation, 0, len(nodeAllocation.allocatedPods))
		for _, v := range nodeAllocation.allocatedPods {
			podAllocations = append(podAllocations, v)
		}
		resp.AllocatedPods = podAllocations
	}
	resp.AvailableCPUs, resp.AllocatedCPUs = nodeAllocation.getAvailableCPUs(topologyOptions.CPUTopology, topologyOptions.MaxRefCount, topologyOptions.ReservedCPUs, cpuset.CPUSet{})
	availableResources, allocatedResource := nodeAllocation.getAvailableNUMANodeResources(topologyOptions, nil)
	for nodeID, v := range availableResources {
		resp.RemainingNUMANodeResources = append(resp.RemainingNUMANodeResources, NUMANodeResource{
			Node:      nodeID,
			Resources: v.DeepCopy(),
		})
	}
	for nodeID, v := range allocatedResource {
		resp.AllocatedNUMANodeResources = append(resp.AllocatedNUMANodeResources, NUMANodeResource{
			Node:      nodeID,
			Resources: v.DeepCopy(),
		})
	}

	return resp
}
