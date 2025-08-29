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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	clock "k8s.io/utils/clock/testing"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
)

func TestQueryNode(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		now := metav1.Now().Rfc3339Copy().Time
		args := &config.LoadAwareSchedulingArgs{
			EstimatedScalingFactors: map[corev1.ResourceName]int64{
				corev1.ResourceCPU:    100,
				corev1.ResourceMemory: 100,
			},
		}
		v := NewResourceVectorizerFromArgs(args)
		e, _ := estimator.NewDefaultEstimator(args, nil)
		pl := &Plugin{
			vectorizer:     v,
			podAssignCache: newPodAssignCache(e, v, args),
		}
		pl.podAssignCache.clock = clock.NewFakeClock(now)
		testNodeName := "test-node"
		pendingPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
				UID:  uuid.NewUUID(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("4"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
			},
		}
		pl.podAssignCache.assign("", pendingPod)
		assignedPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod-1",
				UID:  uuid.NewUUID(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("4"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
						},
					},
				},
				NodeName: testNodeName,
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		}
		pl.podAssignCache.assign(testNodeName, assignedPod)
		terminatedPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-terminated-pod-1",
				UID:  uuid.NewUUID(),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "main",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("4"),
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
						},
					},
				},
				NodeName: testNodeName,
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodFailed,
			},
		}
		pl.podAssignCache.unAssign(testNodeName, terminatedPod)
		pl.podAssignCache.AddOrUpdateNodeMetric(&slov1alpha1.NodeMetric{ObjectMeta: metav1.ObjectMeta{Name: testNodeName}})

		// got unknown node
		engine := gin.Default()
		pl.RegisterEndpoints(engine.Group("/"))
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/node/unknown-node", nil)
		engine.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		got := &NodeAssignInfoData{}
		err := json.NewDecoder(w.Result().Body).Decode(got)
		assert.NoError(t, err)
		expectedResp := &NodeAssignInfoData{}
		assert.Equal(t, expectedResp, got)

		// got the assign pod
		w = httptest.NewRecorder()
		req, _ = http.NewRequest("GET", "/node/test-node", nil)
		engine.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		got = &NodeAssignInfoData{}
		err = json.NewDecoder(w.Result().Body).Decode(got)
		assert.NoError(t, err)
		expectedResp = &NodeAssignInfoData{
			Pods: []PodAssignInfoData{
				{
					Timestamp: now,
					Pod:       assignedPod,
				},
			},
			ProdUsage:         corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0"), corev1.ResourceMemory: resource.MustParse("0")},
			NodeDelta:         corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi")},
			ProdDelta:         corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi")},
			NodeEstimated:     corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi")},
			NodeDeltaPods:     []string{"test-pod-1"},
			ProdDeltaPods:     []string{"test-pod-1"},
			NodeEstimatedPods: []string{"test-pod-1"},
		}
		assert.Equal(t, expectedResp, got)

		// got no assign pod
		pl.podAssignCache.unAssign(testNodeName, assignedPod)
		w = httptest.NewRecorder()
		req, _ = http.NewRequest("GET", "/node/test-node", nil)
		engine.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		got = &NodeAssignInfoData{}
		err = json.NewDecoder(w.Result().Body).Decode(got)
		assert.NoError(t, err)
		expectedResp = &NodeAssignInfoData{
			ProdUsage:     corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0"), corev1.ResourceMemory: resource.MustParse("0")},
			NodeDelta:     corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0"), corev1.ResourceMemory: resource.MustParse("0")},
			ProdDelta:     corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0"), corev1.ResourceMemory: resource.MustParse("0")},
			NodeEstimated: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0"), corev1.ResourceMemory: resource.MustParse("0")},
		}
		assert.Equal(t, expectedResp, got)
	})
}
