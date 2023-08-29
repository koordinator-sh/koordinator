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
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestEndpointsQueryNode(t *testing.T) {
	suit := newPluginTestSuit(t, []*corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node-1",
			},
		},
	})
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
	p.topologyOptionsManager.UpdateTopologyOptions("test-node-1", func(options *TopologyOptions) {
		*options = topologyOptions
	})

	podUID := uuid.NewUUID()
	p.resourceManager.Update("test-node-1", &PodAllocation{
		UID:                podUID,
		CPUSet:             cpuset.MustParse("0,2,4,6"),
		CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
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

	expectedResponse := &NodeResponse{
		Name:            "test-node-1",
		TopologyOptions: topologyOptions,
		AvailableCPUs:   cpuset.MustParse("3,5,7-15"),
		AllocatedCPUs:   CPUDetails{},
		AllocatedPods: []PodAllocation{
			{
				UID:                podUID,
				CPUSet:             cpuset.MustParse("0,2,4,6"),
				CPUExclusivePolicy: schedulingconfig.CPUExclusivePolicyNone,
			},
		},
	}
	for _, v := range []int{0, 2, 4, 6} {
		cpuInfo := topologyOptions.CPUTopology.CPUDetails[v]
		cpuInfo.RefCount++
		cpuInfo.ExclusivePolicy = schedulingconfig.CPUExclusivePolicyNone
		expectedResponse.AllocatedCPUs[v] = cpuInfo
	}
	assert.Equal(t, expectedResponse, response)
}
