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
	"context"
	"encoding/json"
	"testing"
	"time"

	nrtv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

func TestTopologyOptionsManager(t *testing.T) {
	suit := newPluginTestSuit(t, nil)

	expectCPUTopology := buildCPUTopologyForTest(2, 1, 4, 2)

	externalCPUTopology := &extension.CPUTopology{}
	for _, v := range expectCPUTopology.CPUDetails {
		externalCPUTopology.Detail = append(externalCPUTopology.Detail, extension.CPUInfo{
			ID:     int32(v.CPUID),
			Core:   int32(v.CoreID & 0xffff),
			Socket: int32(v.SocketID),
			Node:   int32(v.NodeID & 0xffff),
		})
	}
	data, err := json.Marshal(externalCPUTopology)
	assert.NoError(t, err)

	expectPolicy := &extension.KubeletCPUManagerPolicy{
		Policy: extension.KubeletCPUManagerPolicyStatic,
		Options: map[string]string{
			extension.KubeletCPUManagerPolicyStatic: "true",
		},
		ReservedCPUs: "0-1",
	}
	policyData, err := json.Marshal(expectPolicy)
	assert.NoError(t, err)

	podAllocs := extension.PodCPUAllocs{
		{
			Namespace:        "default",
			Name:             "pod-1",
			UID:              uuid.NewUUID(),
			CPUSet:           "0-3",
			ManagedByKubelet: true,
		},
	}
	podAllocsData, err := json.Marshal(podAllocs)
	assert.NoError(t, err)

	systemQOSResource := &extension.SystemQOSResource{
		CPUSet: "4-5",
	}
	systemQOSResourceData, err := json.Marshal(systemQOSResource)
	assert.NoError(t, err)

	nodeReservation := &extension.NodeReservation{
		ReservedCPUs: "6-7",
	}
	nodeReservationData, err := json.Marshal(nodeReservation)
	assert.NoError(t, err)

	nodeName := "test-node-1"
	topology := &nrtv1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Annotations: map[string]string{
				extension.AnnotationNodeCPUTopology:         string(data),
				extension.AnnotationKubeletCPUManagerPolicy: string(policyData),
				extension.AnnotationNodeCPUAllocs:           string(podAllocsData),
				extension.AnnotationNodeSystemQOSResource:   string(systemQOSResourceData),
				extension.AnnotationNodeReservation:         string(nodeReservationData),
			},
		},
	}

	_, err = suit.NRTClientset.TopologyV1alpha1().NodeResourceTopologies().Create(context.TODO(), topology, metav1.CreateOptions{})
	assert.NoError(t, err)

	topologyOptionsManager := NewTopologyOptionsManager()
	assert.NotNil(t, topologyOptionsManager)

	extendHandle := &frameworkHandleExtender{
		FrameworkExtender: suit.Extender,
		Clientset:         suit.NRTClientset,
	}
	nrtInformerFactory, err := initNRTInformerFactory(extendHandle)
	assert.NoError(t, err)
	err = registerNodeResourceTopologyEventHandler(nrtInformerFactory, topologyOptionsManager)
	assert.NoError(t, err)

	suit.start()

	topologyOptions := topologyOptionsManager.GetTopologyOptions(nodeName)
	assert.NotNil(t, topologyOptions.CPUTopology)
	for k, v := range expectCPUTopology.CPUDetails {
		v.CoreID = v.SocketID<<16 | v.CoreID
		expectCPUTopology.CPUDetails[k] = v
	}
	assert.Equal(t, expectCPUTopology, topologyOptions.CPUTopology)

	policy := topologyOptions.Policy
	assert.NotNil(t, policy)
	assert.Equal(t, expectPolicy, policy)

	assert.Equal(t, 1, topologyOptions.MaxRefCount)

	expectReservedCPUs := cpuset.MustParse("0-7")
	assert.Equal(t, expectReservedCPUs, topologyOptions.ReservedCPUs)

	delete(topology.Annotations, extension.AnnotationNodeCPUAllocs)
	_, err = suit.NRTClientset.TopologyV1alpha1().NodeResourceTopologies().Update(context.TODO(), topology, metav1.UpdateOptions{})
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	topologyOptions = topologyOptionsManager.GetTopologyOptions(nodeName)
	assert.Equal(t, "0-1,4-7", topologyOptions.ReservedCPUs.String())

	err = suit.NRTClientset.TopologyV1alpha1().NodeResourceTopologies().Delete(context.TODO(), topology.Name, metav1.DeleteOptions{})
	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	topologyOptions = topologyOptionsManager.GetTopologyOptions(nodeName)
	assert.Equal(t, TopologyOptions{}, topologyOptions)
}
