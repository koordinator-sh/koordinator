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

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware/estimator"
)

func TestQueryNode(t *testing.T) {
	t.Run("test", func(t *testing.T) {

		preTimeNowFn := timeNowFn
		defer func() {
			timeNowFn = preTimeNowFn
		}()
		timeNowFn = fakeTimeNowFn
		e, _ := estimator.NewDefaultEstimator(&config.LoadAwareSchedulingArgs{}, nil)
		pl := &Plugin{
			podAssignCache: newPodAssignCache(e, NewResourceVectorizer(corev1.ResourceCPU, corev1.ResourceMemory), &config.LoadAwareSchedulingArgs{}),
		}
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
					Timestamp: timeNowFn(),
					Pod:       assignedPod,
				},
			},
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
		expectedResp = &NodeAssignInfoData{}
		assert.Equal(t, expectedResp, got)
	})
}
