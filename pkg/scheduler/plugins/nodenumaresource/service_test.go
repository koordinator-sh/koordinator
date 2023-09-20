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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestEndpointsQueryNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-1",
		},
	}
	extension.SetNodeResourceAmplificationRatios(node, map[corev1.ResourceName]extension.Ratio{
		corev1.ResourceCPU: 1.5,
	})
	suit := newPluginTestSuit(t, nil, []*corev1.Node{node})
	plugin, err := suit.proxyNew(suit.nodeNUMAResourceArgs, suit.Handle)
	assert.NoError(t, err)
	assert.NotNil(t, plugin)
	p := plugin.(*Plugin)
	_ = p.handle.SharedInformerFactory().Core().V1().Nodes().Informer()
	p.handle.SharedInformerFactory().Start(nil)
	p.handle.SharedInformerFactory().WaitForCacheSync(nil)

	topologyOptions := TopologyOptions{
		CPUTopology:  buildCPUTopologyForTest(2, 1, 4, 2),
		ReservedCPUs: cpuset.MustParse("0-1"),
		MaxRefCount:  1,
		Policy: &extension.KubeletCPUManagerPolicy{
			Policy: extension.KubeletCPUManagerPolicyStatic,
			Options: map[string]string{
				extension.KubeletCPUManagerPolicyFullPCPUsOnlyOption: "true",
			},
		},
	}
	for i := 0; i < topologyOptions.CPUTopology.NumNodes; i++ {
		topologyOptions.NUMANodeResources = append(topologyOptions.NUMANodeResources, NUMANodeResource{
			Node: i,
			Resources: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(int64(topologyOptions.CPUTopology.CPUsPerNode()), resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(32*1024*1024*1024, resource.BinarySI),
			}})
	}
	p.topologyOptionsManager.UpdateTopologyOptions("test-node-1", func(options *TopologyOptions) {
		*options = topologyOptions
	})

	podUID := uuid.NewUUID()
	p.resourceManager.Update("test-node-1", &PodAllocation{
		UID:                podUID,
		CPUSet:             cpuset.MustParse("2,3,4,5"),
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
		NUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU: *resource.NewQuantity(4, resource.DecimalSI),
				},
			},
		},
	})

	engine := gin.Default()
	p.RegisterEndpoints(engine.Group("/"))
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/nodes/test-node-1", nil)
	engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	response := &NodeResponse{}
	err = json.NewDecoder(w.Result().Body).Decode(response)
	assert.NoError(t, err)
	sort.Slice(response.RemainingNUMANodeResources, func(i, j int) bool {
		return response.RemainingNUMANodeResources[i].Node < response.RemainingNUMANodeResources[j].Node
	})
	sort.Slice(response.AllocatedNUMANodeResources, func(i, j int) bool {
		return response.AllocatedNUMANodeResources[i].Node < response.AllocatedNUMANodeResources[j].Node
	})

	topologyOptions.NUMANodeResources = nil
	for i := 0; i < topologyOptions.CPUTopology.NumNodes; i++ {
		topologyOptions.NUMANodeResources = append(topologyOptions.NUMANodeResources, NUMANodeResource{
			Node: i,
			Resources: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("12"),
				corev1.ResourceMemory: *resource.NewQuantity(32*1024*1024*1024, resource.BinarySI),
			}})
	}
	topologyOptions.AmplificationRatios = map[corev1.ResourceName]extension.Ratio{
		corev1.ResourceCPU: 1.5,
	}

	expectedResponse := &NodeResponse{
		Name:            "test-node-1",
		TopologyOptions: topologyOptions,
		AvailableCPUs:   cpuset.MustParse("6-15"),
		AllocatedCPUs:   CPUDetails{},
		AllocatedPods: []PodAllocation{
			{
				UID:                podUID,
				CPUSet:             cpuset.MustParse("2,3,4,5"),
				CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
				NUMANodeResources: []NUMANodeResource{
					{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}},
				},
			},
		},
		AllocatedNUMANodeResources: []NUMANodeResource{
			{Node: 0, Resources: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("6")}},
		},
		RemainingNUMANodeResources: []NUMANodeResource{
			{
				Node: 0,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("6"),
					corev1.ResourceMemory: *resource.NewQuantity(32*1024*1024*1024, resource.BinarySI),
				},
			},
			{
				Node: 1,
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("12"),
					corev1.ResourceMemory: *resource.NewQuantity(32*1024*1024*1024, resource.BinarySI)},
			},
		},
	}
	for _, v := range []int{2, 3, 4, 5} {
		cpuInfo := topologyOptions.CPUTopology.CPUDetails[v]
		cpuInfo.RefCount++
		cpuInfo.ExclusivePolicy = schedulingconfig.CPUExclusivePolicyNone
		expectedResponse.AllocatedCPUs[v] = cpuInfo
	}
	assert.Equal(t, expectedResponse, response)
}
