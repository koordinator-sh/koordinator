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

package metrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestGenNodeLabels(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test",
			Labels: map[string]string{},
		},
	}
	Register(node)
	defer Register(nil)
	labels := genNodeLabels()
	assert.Equal(t, 1, len(labels))
	assert.Equal(t, "test", labels[NodeKey])
	RecordCollectNodeCPUInfoStatus(nil)
}

func TestCommonCollectors(t *testing.T) {
	testingNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("200"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10"),
				corev1.ResourceMemory: resource.MustParse("200"),
			},
		},
	}
	testingErr := fmt.Errorf("test error")
	testingNow := time.Now()
	testingContainer := &corev1.ContainerStatus{
		ContainerID: "containerd://1",
		Name:        "test_container",
	}
	testingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test_pod",
			Namespace: "test_pod_namespace",
			UID:       "test01",
		},
	}
	testingPSI := &koordletutil.PSIByResource{
		CPU: system.PSIStats{
			Some: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			Full: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			FullSupported: true,
		},
		Mem: system.PSIStats{
			Some: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			Full: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			FullSupported: true,
		},
		IO: system.PSIStats{
			Some: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			Full: &system.PSILine{
				Avg10:  1,
				Avg60:  1,
				Avg300: 1,
				Total:  1,
			},
			FullSupported: true,
		},
	}

	t.Run("test not panic", func(t *testing.T) {
		Register(testingNode)
		defer Register(nil)

		RecordKoordletStartTime(testingNode.Name, float64(testingNow.Unix()))
		RecordCollectNodeCPUInfoStatus(testingErr)
		RecordCollectNodeCPUInfoStatus(nil)
		RecordBESuppressCores("cfsQuota", float64(1000))
		RecordNonBEUsedCPU(1000)
		RecordNodeUsedCPU(2000)
		RecordPodEviction("evictByCPU")
		ResetContainerCPI()
		RecordContainerCPI(testingContainer, testingPod, 1, 1)
		ResetContainerPSI()
		RecordContainerPSI(testingContainer, testingPod, testingPSI)
		ResetPodPSI()
		RecordPodPSI(testingPod, testingPSI)
	})
}
