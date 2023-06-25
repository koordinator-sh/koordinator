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

package prediction

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type mockPredictServer struct{}

func (m *mockPredictServer) Setup(statesInformer statesinformer.StatesInformer, metricCache metriccache.MetricCache) error {
	return nil
}
func (m *mockPredictServer) Run(stopCh <-chan struct{}) error {
	return nil
}
func (m *mockPredictServer) HasSynced() bool {
	return true
}

func (m *mockPredictServer) GetPrediction(desc MetricDesc) (Result, error) {
	// Mock implementation of GetPrediction function
	return Result{
		Data: map[string]v1.ResourceList{
			"p95": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
			"p98": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(800, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(768*1024*1024, resource.BinarySI),
			},
		},
	}, nil
}

func TestProdReclaimablePredictor_AddPod(t *testing.T) {
	predictServer := &mockPredictServer{}
	coldStartDuration := time.Hour

	factory := NewPredictorFactory(predictServer, coldStartDuration)
	predictor := factory.New(ProdReclaimablePredictor)

	pod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:               "pod-1-uid",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute)},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
							v1.ResourceMemory: *resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
						},
					},
				},
			},
		},
	}

	pod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:               "pod-2-uid",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
							v1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
						},
					},
				},
			},
		},
	}

	pod3 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:               "pod-3-uid",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
			DeletionTimestamp: &metav1.Time{Time: time.Now()},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
							v1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
						},
					},
				},
			},
		},
	}

	err := predictor.AddPod(pod1)
	assert.NoError(t, err)

	err = predictor.AddPod(pod2)
	assert.NoError(t, err)

	err = predictor.AddPod(pod3)
	assert.NoError(t, err)

	result, err := predictor.GetResult()
	assert.NoError(t, err)

	peak := 1.1 * 768 * 1024 * 1024
	expected := v1.ResourceList{
		v1.ResourceCPU:    *resource.NewMilliQuantity(2000-550, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024-int64(peak), resource.BinarySI),
	}

	assert.Equal(t, expected, result)
}
