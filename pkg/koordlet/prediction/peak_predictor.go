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
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// PredictorType defines constants for different types of predictors.
type PredictorType int

const (
	// ProdReclaimablePredictor represents the type of a reclaimable production predictor.
	ProdReclaimablePredictor PredictorType = iota
)

// PredictorFactory is an interface for creating predictors of different types.
type PredictorFactory interface {
	New(PredictorType) Predictor
}

type Predictor interface {
	AddPod(pod *v1.Pod) error

	GetResult() (v1.ResourceList, error)
}

type predictorFactory struct {
	predictServer       PredictServer
	coldStartDuration   time.Duration
	safetyMarginPercent int
}

// NewPredictorFactory creates a new instance of PredictorFactory.
func NewPredictorFactory(predictServer PredictServer, coldStartDuration time.Duration, safetyMarginPercent int) PredictorFactory {
	return &predictorFactory{
		predictServer:       predictServer,
		coldStartDuration:   coldStartDuration,
		safetyMarginPercent: safetyMarginPercent,
	}
}

// New creates a new instance of a predictor based on the given type.
func (f *predictorFactory) New(t PredictorType) Predictor {
	switch t {
	case ProdReclaimablePredictor:
		return &prodReclaimablePredictor{
			predictServer:       f.predictServer,
			coldStartDuration:   f.coldStartDuration,
			safetyMarginPercent: f.safetyMarginPercent,
			reclaimable:         util.NewZeroResourceList(),
			pods:                make(map[string]bool),
		}
	default:
		return &emptyPredictor{}
	}
}

type emptyPredictor struct {
}

// AddPod adds a pod to the predictor for resource prediction.
func (p *emptyPredictor) AddPod(pod *v1.Pod) error {
	return nil
}

// GetResult returns an error indicating that the predictor is empty.
func (p *emptyPredictor) GetResult() (v1.ResourceList, error) {
	return nil, fmt.Errorf("empty pridictor")
}

func NewEmptyPredictorFactory() PredictorFactory {
	return &emptyPredictorFactory{}
}

type emptyPredictorFactory struct {
}

func (f *emptyPredictorFactory) New(t PredictorType) Predictor {
	return &emptyPredictor{}
}

type prodReclaimablePredictor struct {
	predictServer       PredictServer
	coldStartDuration   time.Duration
	safetyMarginPercent int

	reclaimable v1.ResourceList
	pods        map[string]bool
}

// AddPod adds a pod to the predictor for resource prediction.
func (p *prodReclaimablePredictor) AddPod(pod *v1.Pod) error {
	// prodReclaimablePredictor process only PriorityProd pods.
	if extension.GetPodPriorityClassWithDefault(pod) != extension.PriorityProd {
		return nil
	}

	if p.pods[string(pod.UID)] {
		return fmt.Errorf("Pod %v already exists", pod.UID)
	} else {
		p.pods[string(pod.UID)] = true
	}

	// Pods in cold start have 0 reclaimable resources
	if time.Since(pod.CreationTimestamp.Time) <= p.coldStartDuration {
		return nil
	}

	// Pods in terminating stage have 0 reclaimable resources
	if pod.DeletionTimestamp != nil {
		return nil
	}

	result, err := p.predictServer.GetPrediction(MetricDesc{UID: UIDType(pod.UID)})
	if err != nil {
		return err
	}
	p95Resources := result.Data["p95"]
	p98Resources := result.Data["p98"]

	podRequests := util.GetPodRequest(pod, v1.ResourceCPU, v1.ResourceMemory)
	podCPURequest := podRequests[v1.ResourceCPU]
	podMemoryRequest := podRequests[v1.ResourceMemory]

	reclaimableCPUMilli := int64(0)
	reclaimableMemoryBytes := int64(0)

	ratioAfterSafetyMargin := float64(100+p.safetyMarginPercent) / 100
	if p95CPU, ok := p95Resources[v1.ResourceCPU]; ok {
		peakCPU := util.MultiplyMilliQuant(p95CPU, ratioAfterSafetyMargin)
		reclaimableCPUMilli = podCPURequest.MilliValue() - peakCPU.MilliValue()
	}
	if p98Memory, ok := p98Resources[v1.ResourceMemory]; ok {
		peakMemory := util.MultiplyQuant(p98Memory, ratioAfterSafetyMargin)
		reclaimableMemoryBytes = podMemoryRequest.Value() - peakMemory.Value()
	}

	if reclaimableCPUMilli > 0 {
		cpu := p.reclaimable[v1.ResourceCPU]
		reclaimableCPU := resource.NewMilliQuantity(reclaimableCPUMilli, resource.DecimalSI)
		cpu.Add(*reclaimableCPU)
		p.reclaimable[v1.ResourceCPU] = cpu
	}
	if reclaimableMemoryBytes > 0 {
		memory := p.reclaimable[v1.ResourceMemory]
		reclaimableMemory := resource.NewQuantity(reclaimableMemoryBytes, resource.BinarySI)
		memory.Add(*reclaimableMemory)
		p.reclaimable[v1.ResourceMemory] = memory
	}

	return nil
}

// GetResult returns the predicted resource list for the added pods.
func (p *prodReclaimablePredictor) GetResult() (v1.ResourceList, error) {
	return p.reclaimable, nil
}
