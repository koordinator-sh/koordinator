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
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
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
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(3000, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI),
			},
		},
	}
	node_huge := node.DeepCopy()
	node_huge.Status.Capacity = v1.ResourceList{
		v1.ResourceCPU:    *resource.NewMilliQuantity(8000, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
	}
	node_small := node.DeepCopy()
	node_small.Status.Capacity = v1.ResourceList{
		v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
		v1.ResourceMemory: *resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
	}
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

	number1 := 1.1 * 1024 * 1024 * 1024
	number2 := 1.1 * 1536 * 1024 * 1024
	testCases := []struct {
		name                          string
		predictor                     Predictor
		podsList                      []*v1.Pod
		expectedPodPredictResult      v1.ResourceList
		expectedPriorityPredictResult v1.ResourceList
	}{
		{
			name:      "node capacity == pods' requests",
			predictor: factory.New(ProdReclaimablePredictor, &ProdReclaimablePredictorOptions{node}),
			podsList:  []*v1.Pod{pod1, pod2, pod3},
			expectedPodPredictResult: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(2000-500*1.1, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024-int64(number1), resource.BinarySI),
			},
			expectedPriorityPredictResult: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(3000-1300*1.1, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024-int64(number2), resource.BinarySI),
			},
		},
		{
			name:      "node capacity > pods' requests",
			predictor: factory.New(ProdReclaimablePredictor, &ProdReclaimablePredictorOptions{node_huge}),
			podsList:  []*v1.Pod{pod1, pod2, pod3},
			expectedPodPredictResult: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(2000-500*1.1, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024-int64(number1), resource.BinarySI),
			},
			expectedPriorityPredictResult: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(3000-1300*1.1, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024-int64(number2), resource.BinarySI),
			},
		},
		{
			name:      "node capacity < pods' requests",
			predictor: factory.New(ProdReclaimablePredictor, &ProdReclaimablePredictorOptions{node_small}),
			podsList:  []*v1.Pod{pod1, pod2, pod3},
			expectedPodPredictResult: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(1000-500*1.1, resource.DecimalSI),
				v1.ResourceMemory: resource.MustParse("0"),
			},
			expectedPriorityPredictResult: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("0"),
				v1.ResourceMemory: resource.MustParse("0"),
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			predictor := tc.predictor
			assert.Equal(t, 2, len(predictor.(*minPredictor).predictors))
			for _, pod := range tc.podsList {
				err := predictor.AddPod(pod)
				assert.NoError(t, err)
			}
			result, err := predictor.GetResult()
			assert.NoError(t, err)
			gotPodResult, err := predictor.(*minPredictor).predictors[0].GetResult()
			assert.NoError(t, err)
			assert.Equal(t, true, quotav1.Equals(tc.expectedPodPredictResult, gotPodResult))
			gotPriorityResult, err := predictor.(*minPredictor).predictors[1].GetResult()
			assert.NoError(t, err)
			assert.Equal(t, true, quotav1.Equals(tc.expectedPriorityPredictResult, gotPriorityResult))
			expected := util.MinResourceList(tc.expectedPodPredictResult, tc.expectedPriorityPredictResult)
			assert.Equal(t, expected, result)
			assert.Equal(t, true, quotav1.Equals(expected, result))
		})
	}
}

func TestPodReclaimablePredictor(t *testing.T) {
	predictServer := &mockPredictServer{
		DefaultResult: testPredictionResult,
	}
	coldStartDuration := time.Hour
	priority := extension.PriorityProdValueMin
	priorityBatch := extension.PriorityBatchValueMin

	pod1 := &v1.Pod{ // cool start
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

	pod2 := &v1.Pod{ // normal
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

	pod3 := &v1.Pod{ // deleted
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

	pod4 := &v1.Pod{ // batch
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

	newPodReclaimablePredictor := func(node *v1.Node) *podReclaimablePredictor {
		return &podReclaimablePredictor{
			predictServer:       predictServer,
			coldStartDuration:   coldStartDuration,
			safetyMarginPercent: 10,
			podFilterFn:         isPodReclaimableForProd,
			reclaimable:         util.NewZeroResourceList(),
			unReclaimable:       util.NewZeroResourceList(),
			node:                node,
			pods:                make(map[string]bool),
		}
	}
	peak := 1.1 * 768 * 1024 * 1024
	testCase := []struct {
		name                string
		predictor           Predictor
		podList             []*v1.Pod
		expectedReclaimable v1.ResourceList
	}{
		{
			name: "node capacity > pods' requests ",
			predictor: newPodReclaimablePredictor(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
				Status: v1.NodeStatus{
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(3000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI),
					},
				},
			}),
			podList: []*v1.Pod{pod1, pod2, pod3, pod4},
			expectedReclaimable: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(2000-550, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(2*1024*1024*1024-int64(peak), resource.BinarySI),
			},
		},
		{
			name: "node capacity  < pods' requests ",
			predictor: newPodReclaimablePredictor(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-2",
				},
				Status: v1.NodeStatus{
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(1000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
					},
				},
			}),
			podList: []*v1.Pod{pod1, pod2, pod3, pod4},
			expectedReclaimable: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(1000-550, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity(1024*1024*1024-int64(peak), resource.BinarySI),
			},
		},
	}
	for _, tc := range testCase {
		t.Run(tc.name, func(t *testing.T) {
			predictor := tc.predictor
			for _, pod := range tc.podList {
				err := predictor.AddPod(pod)
				assert.NoError(t, err)
			}
			result, err := predictor.GetResult()
			assert.NoError(t, err)
			assert.Equal(t, true, quotav1.Equals(tc.expectedReclaimable, result))
		})
	}
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
	newPodReclaimablePredictor := func(node *v1.Node) *priorityReclaimablePredictor {
		return &priorityReclaimablePredictor{
			predictServer:         predictServer,
			node:                  node,
			safetyMarginPercent:   0,
			priorityClassFilterFn: isPriorityClassReclaimableForProd,
			reclaimRequest:        util.NewZeroResourceList(),
		}
	}
	testCase := []struct {
		name                string
		predictor           Predictor
		podList             []*v1.Pod
		expectedReclaimable v1.ResourceList
	}{
		{
			name: "node capacity > pods' requests ",
			predictor: newPodReclaimablePredictor(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
				Status: v1.NodeStatus{
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(3000, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(3*1024*1024*1024, resource.BinarySI),
					},
				},
			}),
			podList: []*v1.Pod{podProd, podBatch},
			expectedReclaimable: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(1000-(300+500)*1.0, resource.DecimalSI),
				v1.ResourceMemory: *resource.NewQuantity((2048-(512+1024)*1.0)*1024*1024, resource.BinarySI),
			},
		},
		{
			name: "node capacity < pods' requests ",
			predictor: newPodReclaimablePredictor(&v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
				Status: v1.NodeStatus{
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewMilliQuantity(900, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
					},
				},
			}),
			podList: []*v1.Pod{podProd, podBatch},
			expectedReclaimable: v1.ResourceList{
				v1.ResourceCPU:    *resource.NewMilliQuantity(900-(300+500)*1.0, resource.DecimalSI),
				v1.ResourceMemory: resource.MustParse("0"),
			},
		},
	}
	for _, tc := range testCase {
		t.Run(tc.name, func(t *testing.T) {
			predictor := tc.predictor
			for _, pod := range tc.podList {
				err := predictor.AddPod(pod)
				assert.NoError(t, err)
			}
			got, err := predictor.GetResult()
			assert.NoError(t, err)
			assert.Equal(t, true, quotav1.Equals(tc.expectedReclaimable, got))
		})
	}
}
