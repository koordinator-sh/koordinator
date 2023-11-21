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
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/koordinator-sh/koordinator/pkg/util/histogram"
)

func deltaPer(a, b float64) float64 {
	return math.Abs(a-b) / b
}

func TestDefaultHistogram(t *testing.T) {
	peakPrediction := NewPeakPredictServer(NewDefaultConfig())

	testCases := []struct {
		hist     histogram.Histogram
		minValue float64
		maxValue float64
	}{
		{
			hist:     peakPrediction.(*peakPredictServer).defaultCPUHistogram(),
			minValue: 0.05,
			maxValue: 1024,
		},
		{
			hist:     peakPrediction.(*peakPredictServer).defaultMemoryHistogram(),
			minValue: 10 << 20,
			maxValue: 1 << 31,
		},
	}

	now := time.Now()
	for _, tc := range testCases {
		tc.hist.AddSample(tc.minValue, 1, now)
		tc.hist.AddSample(tc.maxValue, 1, now)

		maxValue := tc.hist.Percentile(1.0)
		if deltaPer(maxValue, tc.maxValue) > DefaultHistogramBucketSizeGrowth {
			t.Errorf("failed to get max value, expected %v actual %v", tc.maxValue, maxValue)
		}

		minValue := tc.hist.Percentile(0.0)
		if deltaPer(minValue, tc.minValue) > DefaultHistogramBucketSizeGrowth {
			t.Errorf("failed to get min value, expected %v actual %v", tc.minValue, minValue)
		}
	}
}

var _ Informer = &mockInformer{}

type mockInformer struct {
	Pods []*v1.Pod
	Node *v1.Node
}

func (i *mockInformer) HasSynced() bool {
	return true
}

func (i *mockInformer) ListPods() []*v1.Pod {
	return i.Pods
}

func (i *mockInformer) GetNode() *v1.Node {
	return i.Node
}

var _ MetricServer = &mockMetricServer{}

type podUsage struct {
	PodCPUUsage    float64
	PodMemoryUsage float64
}

type mockMetricServer struct {
	PodUsage        map[UIDType]podUsage
	NodeCPUUsage    float64
	NodeMemoryUsage float64
}

func (ms *mockMetricServer) GetPodMetric(desc MetricDesc, m MetricKey) (float64, error) {
	usage, ok := ms.PodUsage[desc.UID]
	if !ok {
		return 0, fmt.Errorf("pod not found")
	}
	switch m {
	case CPUUsage:
		return usage.PodCPUUsage, nil
	case MemoryUsage:
		return usage.PodMemoryUsage, nil
	}
	return 0, fmt.Errorf("unsupported metric: %v", m)
}

func (ms *mockMetricServer) GetNodeMetric(desc MetricDesc, m MetricKey) (float64, error) {
	switch m {
	case CPUUsage:
		return ms.NodeCPUUsage, nil
	case MemoryUsage:
		return ms.NodeMemoryUsage, nil
	}
	return 0, fmt.Errorf("unsupported metric: %v", m)
}

func TestPredictServerMainLoop(t *testing.T) {
	informer := &mockInformer{}
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			UID:  "node1",
		},
		Spec: v1.NodeSpec{},
	}
	informer.Node = node

	pods := []*v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod1",
				UID:       "pod1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod2",
				UID:       "pod2",
			},
		},
	}
	informer.Pods = pods

	metricServer := &mockMetricServer{
		PodUsage: make(map[UIDType]podUsage),
	}
	nodeMemoryUsed := resource.NewQuantity(1024*1024*256, resource.BinarySI)
	metricServer.NodeMemoryUsage = float64(nodeMemoryUsed.Value())
	metricServer.PodUsage[UIDType(pods[0].UID)] = podUsage{PodCPUUsage: 1, PodMemoryUsage: 128 * 1024 * 1024}
	metricServer.PodUsage[UIDType(pods[1].UID)] = podUsage{PodCPUUsage: 3, PodMemoryUsage: 128 * 1024 * 1024}

	peakPrediction := NewPeakPredictServer(NewDefaultConfig())
	peakPrediction.(*peakPredictServer).informer = informer
	peakPrediction.(*peakPredictServer).metricServer = metricServer

	stopCh := make(chan struct{})
	defer close(stopCh)

	peakPrediction.(*peakPredictServer).training()

	expectCount := 9 // pods(2)+node(1)+priority(5)+sys(1)
	assert.Equal(t, expectCount, len(peakPrediction.(*peakPredictServer).models))

	gen := &generator{}
	peak, err := peakPrediction.GetPrediction(MetricDesc{UID: gen.Node()})
	if err != nil {
		t.Errorf("err %v", err)
	}
	p90CPU, ok := peak.Data["p90"][v1.ResourceCPU]
	if !ok || p90CPU.MilliValue() != 25 {
		t.Errorf("failed to get node cpu prediction, expected ~25 actual %v", p90CPU.MilliValue())
	}
	p90Mem, ok := peak.Data["p90"][v1.ResourceMemory]
	if !ok || p90Mem.Value() < 10<<20 {
		t.Errorf("failed to get node memory prediction, expected ~%v actual %v", nodeMemoryUsed.Value(), p90Mem.Value())
	}

	// add 100 samples
	for i := 2; i <= 100; i++ {
		metricServer.PodUsage[UIDType(pods[0].UID)] = podUsage{PodCPUUsage: float64(i), PodMemoryUsage: 128 * 1024 * 1024}
		peakPrediction.(*peakPredictServer).training()
	}
	peak, err = peakPrediction.GetPrediction(MetricDesc{UID: gen.Pod(pods[0])})
	if err != nil {
		t.Errorf("err %v", err)
	}
	p90CPU, ok = peak.Data["p90"][v1.ResourceCPU]
	if !ok || p90CPU.MilliValue() < 90 {
		t.Errorf("failed to get pods cpu prediction, expected ~1000 actual %v", p90CPU.Value())
	}
}

func TestPredictServerStop(t *testing.T) {
	informer := &mockInformer{}
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			UID:  "node1",
		},
		Spec: v1.NodeSpec{},
	}
	informer.Node = node

	pods := []*v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod1",
				UID:       "pod1",
			},
		},
	}
	informer.Pods = pods

	metricServer := &mockMetricServer{}

	peakPrediction := NewPeakPredictServer(NewDefaultConfig())
	peakPrediction.(*peakPredictServer).informer = informer
	peakPrediction.(*peakPredictServer).metricServer = metricServer

	exitedCh := make(chan struct{})
	stopCh := make(chan struct{})
	go func() {
		peakPrediction.Run(stopCh)
		close(exitedCh)
	}()
	close(stopCh)
	select {
	case <-exitedCh:
		return
	case <-time.After(time.Second):
		t.Errorf("failed to wait Run loop to exited")
	}
}

func TestPredictServerGCOldModels(t *testing.T) {
	informer := &mockInformer{}
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			UID:  "node1",
		},
		Spec: v1.NodeSpec{},
	}
	informer.Node = node
	informer.Pods = nil

	metricServer := &mockMetricServer{
		PodUsage: make(map[UIDType]podUsage),
	}
	nodeMemoryUsed := resource.NewQuantity(1024*1024*256, resource.BinarySI)
	metricServer.NodeMemoryUsage = float64(nodeMemoryUsed.Value())

	mockClock := clock.NewFakeClock(time.Now())
	peakPrediction := NewPeakPredictServer(NewDefaultConfig())
	peakPrediction.(*peakPredictServer).informer = informer
	peakPrediction.(*peakPredictServer).metricServer = metricServer
	peakPrediction.(*peakPredictServer).clock = mockClock
	peakPrediction.(*peakPredictServer).training()

	modelSize := len(peakPrediction.(*peakPredictServer).models)
	if modelSize != 7 { // 9 - 2
		t.Errorf("unexpected modelSize, expected 1 actual %v", modelSize)
	}
	mockClock.Step(peakPrediction.(*peakPredictServer).cfg.ModelExpirationDuration)
	mockClock.Step(time.Minute)
	peakPrediction.(*peakPredictServer).gcModels()

	modelSize = len(peakPrediction.(*peakPredictServer).models)
	if modelSize != 0 {
		t.Errorf("unexpected modelSize, expected 0 actual %v", modelSize)
	}
}

func makeTestHistogram() histogram.Histogram {
	options, _ := histogram.NewLinearHistogramOptions(100, 1024, 0.001)
	return histogram.NewHistogram(options)
}

type mockCheckpointer struct {
}

func (ckpt *mockCheckpointer) Save(checkpoint ModelCheckpoint) error {
	return nil
}

func (ckpt *mockCheckpointer) Remove(UID UIDType) error {
	return nil
}

func (ckpt *mockCheckpointer) Restore() ([]*ModelCheckpoint, error) {
	return nil, nil
}

func TestDoCheckpoint(t *testing.T) {
	now := time.Now()
	predictServer := &peakPredictServer{
		cfg:          NewDefaultConfig(),
		hasSynced:    &atomic.Bool{},
		informer:     &mockInformer{},
		metricServer: &mockMetricServer{},
		uidGenerator: &generator{},
		clock:        clock.NewFakeClock(now),
		models: map[UIDType]*PredictModel{
			UIDType("model1"): {
				CPU:    makeTestHistogram(),
				Memory: makeTestHistogram(),
			},
			UIDType("model2"): {
				CPU:    makeTestHistogram(),
				Memory: makeTestHistogram(),
			},
		},
		checkpointer: &mockCheckpointer{},
	}
	predictServer.hasSynced.Store(true)
	predictServer.doCheckpoint()

	model1 := predictServer.models[UIDType("model1")]
	assert.Equal(t, now, model1.LastCheckpointed)

	model2 := predictServer.models[UIDType("model2")]
	assert.Equal(t, now, model2.LastCheckpointed)
}

func TestDoCheckpoint_MaxPerStep(t *testing.T) {
	now := time.Now()
	cfg := NewDefaultConfig()
	cfg.ModelCheckpointMaxPerStep = 1
	predictServer := &peakPredictServer{
		cfg:          cfg,
		hasSynced:    &atomic.Bool{},
		informer:     &mockInformer{},
		metricServer: &mockMetricServer{},
		uidGenerator: &generator{},
		clock:        clock.NewFakeClock(now),
		models: map[UIDType]*PredictModel{
			UIDType("model1"): {
				CPU:    makeTestHistogram(),
				Memory: makeTestHistogram(),
			},
			UIDType("model2"): {
				CPU:    makeTestHistogram(),
				Memory: makeTestHistogram(),
			},
			UIDType("model3"): {
				CPU:    makeTestHistogram(),
				Memory: makeTestHistogram(),
			},
		},
		checkpointer: &mockCheckpointer{},
	}
	predictServer.hasSynced.Store(true)

	predictServer.doCheckpoint()
	updatedModelCount := countModelsCheckpointAtTime(predictServer.models, now)
	assert.Equal(t, 1, updatedModelCount)
}

func countModelsCheckpointAtTime(models map[UIDType]*PredictModel, t time.Time) int {
	updatedModelCount := 0
	for _, model := range models {
		if model.LastCheckpointed.Equal(t) {
			updatedModelCount++
		}
	}
	return updatedModelCount
}

func TestDoCheckpoint_CheckpointInterval(t *testing.T) {
	now := time.Now()
	mockClock := clock.NewFakeClock(now)
	predictServer := &peakPredictServer{
		cfg:          NewDefaultConfig(),
		hasSynced:    &atomic.Bool{},
		informer:     &mockInformer{},
		metricServer: &mockMetricServer{},
		uidGenerator: &generator{},
		clock:        mockClock,
		models: map[UIDType]*PredictModel{
			UIDType("model1"): {
				CPU:    makeTestHistogram(),
				Memory: makeTestHistogram(),
			},
			UIDType("model2"): {
				CPU:    makeTestHistogram(),
				Memory: makeTestHistogram(),
			},
			UIDType("model3"): {
				CPU:    makeTestHistogram(),
				Memory: makeTestHistogram(),
			},
		},
		checkpointer: &mockCheckpointer{},
	}
	predictServer.hasSynced.Store(true)
	predictServer.doCheckpoint()

	newTime := now.Add(predictServer.cfg.ModelCheckpointInterval / 2)
	mockClock.SetTime(newTime)
	predictServer.doCheckpoint()
	updatedModelCount := countModelsCheckpointAtTime(predictServer.models, newTime)
	assert.Equal(t, 0, updatedModelCount)

	newTime = now.Add(predictServer.cfg.ModelCheckpointInterval + time.Minute)
	mockClock.SetTime(newTime)
	predictServer.doCheckpoint()
	updatedModelCount = countModelsCheckpointAtTime(predictServer.models, newTime)
	assert.Equal(t, 3, updatedModelCount)
}

func TestDoCheckpoint_RestoreModels(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("/tmp", "checkpoints")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	now := time.Now()
	mockClock := clock.NewFakeClock(now)
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			UID:  "node1",
		},
		Spec: v1.NodeSpec{},
	}
	pods := []*v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod1",
				UID:       "pod1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod2",
				UID:       "pod2",
			},
		},
	}
	testModels := map[UIDType]*PredictModel{
		UIDType(DefaultNodeID): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
		UIDType("__node-sys__"): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
		UIDType("__node-koord-prod__"): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
		UIDType("__node-koord-mid__"): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
		UIDType("__node-koord-batch__"): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
		UIDType("__node-koord-free__"): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
		UIDType("__node-__"): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
		UIDType("pod1"): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
		UIDType("pod2"): {
			CPU:    makeTestHistogram(),
			Memory: makeTestHistogram(),
		},
	}
	cfg := NewDefaultConfig()
	cfg.ModelCheckpointMaxPerStep = 20
	predictServer := &peakPredictServer{
		cfg:          cfg,
		hasSynced:    &atomic.Bool{},
		informer:     &mockInformer{Pods: pods, Node: node},
		metricServer: &mockMetricServer{},
		uidGenerator: &generator{},
		clock:        mockClock,
		models:       testModels,
		checkpointer: NewFileCheckpointer(tempDir),
	}
	predictServer.hasSynced.Store(true)
	predictServer.doCheckpoint()

	// clear the models in memory and restore it
	predictServer.models = make(map[UIDType]*PredictModel)
	predictServer.restoreModels()
	assert.Equal(t, len(testModels), len(predictServer.models), "restore models from checkpoint")

	// mock another model and restore it to unknownUIDs
	predictServer.models["unknown"] = &PredictModel{
		CPU:    makeTestHistogram(),
		Memory: makeTestHistogram(),
	}
	predictServer.doCheckpoint()
	delete(predictServer.models, "unknown")

	unknownUIDs := predictServer.restoreModels()
	assert.Equal(t, 1, len(unknownUIDs), "unknown uids")
}
