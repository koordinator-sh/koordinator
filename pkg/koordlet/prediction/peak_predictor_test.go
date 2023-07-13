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
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var testZeroResult = Result{
	Data: map[string]v1.ResourceList{
		"p95": util.NewZeroResourceList(),
		"p98": util.NewZeroResourceList(),
	},
}

var testPredictionResult = Result{
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
}

type mockPredictServer struct {
	ResultMap     map[UIDType]Result
	DefaultResult Result
	DefaultErr    error
}

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
	if m.ResultMap == nil {
		return m.DefaultResult, m.DefaultErr
	}
	result, ok := m.ResultMap[desc.UID]
	if !ok {
		return m.DefaultResult, m.DefaultErr
	}
	return result, nil
}

func TestProdReclaimablePredictor_AddPod(t *testing.T) {
	priority := extension.PriorityProdValueMin
	sysPrediction := Result{
		Data: map[string]v1.ResourceList{
			"p95": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(300, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
			"p98": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
		},
	}
	prodPrediction := Result{
		Data: map[string]v1.ResourceList{
			"p95": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			"p98": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(1200, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
	}
	podPrediction := Result{
		Data: map[string]v1.ResourceList{
			"p95": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(768*1024*1024, resource.BinarySI),
			},
			"p98": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
	}
	pod1 := &v1.Pod{ // cold start
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
			Priority: &priority,
		},
	}
	pod2 := &v1.Pod{ // normal
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
			Priority: &priority,
		},
	}
	pod3 := &v1.Pod{ // to be deleted
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

	predictServer := &mockPredictServer{
		ResultMap: map[UIDType]Result{
			getNodeItemUID(string(extension.PriorityProd)):  prodPrediction,
			getNodeItemUID(string(extension.PriorityBatch)): testZeroResult,
			getNodeItemUID(SystemItemID):                    sysPrediction,
			UIDType(pod1.UID):                               podPrediction,
			UIDType(pod2.UID):                               podPrediction,
			UIDType(pod3.UID):                               testZeroResult,
		},
	}
	coldStartDuration := time.Hour

	factory := NewPredictorFactory(predictServer, coldStartDuration, 10)
	predictor := factory.New(ProdReclaimablePredictor)
	assert.Equal(t, 2, len(predictor.(*minPredictor).predictors))

	err := predictor.AddPod(pod1)
	assert.NoError(t, err)

	err = predictor.AddPod(pod2)
	assert.NoError(t, err)

	err = predictor.AddPod(pod3)
	assert.NoError(t, err)

	result, err := predictor.GetResult()
	assert.NoError(t, err)

	podMemPeak := 1.1 * 1024 * 1024 * 1024
	podPredictResult := v1.ResourceList{
		v1.ResourceCPU:    *resource.NewMilliQuantity(2000-500*1.1, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024-int64(podMemPeak), resource.BinarySI),
	}
	gotPodResult, err := predictor.(*minPredictor).predictors[0].GetResult()
	assert.NoError(t, err)
	assert.Equal(t, podPredictResult, gotPodResult)
	prodMemPeak := 1.1 * 1536 * 1024 * 1024
	priorityPredictResult := v1.ResourceList{
		v1.ResourceCPU:    *resource.NewMilliQuantity(3000-1300*1.1, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024-int64(prodMemPeak), resource.BinarySI),
	}
	gotPriorityResult, err := predictor.(*minPredictor).predictors[1].GetResult()
	assert.NoError(t, err)
	assert.Equal(t, priorityPredictResult, gotPriorityResult)

	// min()
	expected := util.MinResourceList(podPredictResult, priorityPredictResult)
	assert.Equal(t, expected, result)
}

func Test_podReclaimablePredictor(t *testing.T) {
	predictServer := &mockPredictServer{
		DefaultResult: testPredictionResult,
	}
	coldStartDuration := time.Hour
	priority := extension.PriorityProdValueMin
	priorityBatch := extension.PriorityBatchValueMin

	predictor := &podReclaimablePredictor{
		predictServer:       predictServer,
		coldStartDuration:   coldStartDuration,
		safetyMarginPercent: 10,
		podFilterFn:         isPodReclaimableForProd,
		reclaimable:         util.NewZeroResourceList(),
		pods:                make(map[string]bool),
	}

	pod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:               "pod-1-uid",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute)},
		},
		Spec: v1.PodSpec{
			Priority: &priority,
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
			Priority: &priority,
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
			Priority: &priority,
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

	pod4 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:               "pod-4-uid",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
		},
		Spec: v1.PodSpec{
			Priority: &priorityBatch,
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

	err = predictor.AddPod(pod4)
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

func Test_priorityReclaimablePredictor(t *testing.T) {
	sysPrediction := Result{
		Data: map[string]v1.ResourceList{
			"p95": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(300, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
			"p98": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(512*1024*1024, resource.BinarySI),
			},
		},
	}
	prodPrediction := Result{
		Data: map[string]v1.ResourceList{
			"p95": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(500, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(768*1024*1024, resource.BinarySI),
			},
			"p98": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(800, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
		},
	}
	batchPrediction := Result{
		Data: map[string]v1.ResourceList{
			"p95": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
			},
			"p98": {
				v1.ResourceCPU:    *resource.NewMilliQuantity(1500, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(2048*1024*1024, resource.BinarySI),
			},
		},
	}
	podProd := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prod-pod",
			UID:               "pod-1-uid",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute)},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
							v1.ResourceMemory: *resource.NewQuantity(2048*1024*1024, resource.BinarySI),
						},
					},
				},
			},
			Priority: pointer.Int32(extension.PriorityProdValueMin),
		},
	}
	podBatch := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "batch-pod",
			UID:               "pod-2-uid",
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							extension.BatchCPU:    *resource.NewMilliQuantity(2000, resource.DecimalSI),
							extension.BatchMemory: *resource.NewQuantity(2*1024*1024*1024, resource.BinarySI),
						},
					},
				},
			},
			Priority: pointer.Int32(extension.PriorityBatchValueMax),
		},
	}

	predictServer := &mockPredictServer{
		ResultMap: map[UIDType]Result{
			getNodeItemUID(string(extension.PriorityProd)):  prodPrediction,
			getNodeItemUID(string(extension.PriorityBatch)): batchPrediction,
			getNodeItemUID(SystemItemID):                    sysPrediction,
			UIDType(podProd.UID):                            prodPrediction,
			UIDType(podBatch.UID):                           batchPrediction,
		},
	}

	predictor := &priorityReclaimablePredictor{
		predictServer:         predictServer,
		safetyMarginPercent:   0,
		priorityClassFilterFn: isPriorityClassReclaimableForProd,
		reclaimRequest:        util.NewZeroResourceList(),
	}

	err := predictor.AddPod(podProd)
	assert.NoError(t, err)

	err = predictor.AddPod(podBatch)
	assert.NoError(t, err)

	got, err := predictor.GetResult()
	assert.NoError(t, err)
	expected := v1.ResourceList{
		v1.ResourceCPU:    *resource.NewMilliQuantity(1000-(300+500)*1.0, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity((2048-(512+1024)*1.0)*1024*1024, resource.BinarySI),
	}
	assert.Equal(t, expected, got)
}
