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
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
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
	GetPredictorName() string
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
		podPredictor := &podReclaimablePredictor{
			predictServer:       f.predictServer,
			coldStartDuration:   f.coldStartDuration,
			safetyMarginPercent: f.safetyMarginPercent,
			podFilterFn:         isPodReclaimableForProd,
			reclaimable:         util.NewZeroResourceList(),
			pods:                make(map[string]bool),
		}
		priorityPredictor := &priorityReclaimablePredictor{
			predictServer:         f.predictServer,
			safetyMarginPercent:   f.safetyMarginPercent,
			priorityClassFilterFn: isPriorityClassReclaimableForProd,
			reclaimRequest:        util.NewZeroResourceList(),
		}
		return &minPredictor{
			predictors: []Predictor{
				podPredictor,
				priorityPredictor,
			},
		}
	default:
		return &emptyPredictor{}
	}
}

var _ Predictor = (*emptyPredictor)(nil)

type emptyPredictor struct {
}

func (p *emptyPredictor) GetPredictorName() string {
	return "emptyPredictor"
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

var _ Predictor = (*podReclaimablePredictor)(nil)

// podReclaimablePredictor predicts the peak according to historical metrics of the pods.
// e.g. A podReclaimablePredictor for Prod pods calculates the result based on the sum of the percentile of Prod pods.
type podReclaimablePredictor struct {
	predictServer       PredictServer
	coldStartDuration   time.Duration
	safetyMarginPercent int
	podFilterFn         func(pod *v1.Pod) bool // return true if the pod is reclaimable

	reclaimable v1.ResourceList
	pods        map[string]bool
}

// GetPredictorName is used to obtain the predictor name.
func (p *podReclaimablePredictor) GetPredictorName() string {
	return "podReclaimablePredictor"
}

// AddPod adds a pod to the predictor for resource prediction.
func (p *podReclaimablePredictor) AddPod(pod *v1.Pod) error {
	// podReclaimablePredictor process only specified PriorityClass pods.
	if !p.podFilterFn(pod) {
		klog.V(6).Infof("podReclaimablePredictor skip pod %s which is not reclaimable", util.GetPodKey(pod))
		return nil
	}

	if p.pods[string(pod.UID)] {
		return fmt.Errorf("Pod %s already exist in the pod reclaimable predictor", util.GetPodKey(pod))
	}
	p.pods[string(pod.UID)] = true

	// Pods in cold start have 0 reclaimable resources
	if time.Since(pod.CreationTimestamp.Time) <= p.coldStartDuration {
		return nil
	}

	// Pods in terminating stage have 0 reclaimable resources
	// Terminated pods are not running and do not need to predict.
	if pod.DeletionTimestamp != nil || util.IsPodTerminated(pod) {
		return nil
	}

	result, err := p.predictServer.GetPrediction(MetricDesc{UID: UIDType(pod.UID)})
	if err != nil {
		klog.V(5).Infof("podReclaimablePredictor failed to get prediction for pod %s, err: %s",
			util.GetPodKey(pod), err)
		return err
	}
	// TODO: customize the percentile
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
func (p *podReclaimablePredictor) GetResult() (v1.ResourceList, error) {
	metrics.RecordNodePredictedResourceReclaimable(string(v1.ResourceCPU), metrics.UnitCore, p.GetPredictorName(), float64(p.reclaimable.Cpu().MilliValue())/1000)
	metrics.RecordNodePredictedResourceReclaimable(string(v1.ResourceMemory), metrics.UnitByte, p.GetPredictorName(), float64(p.reclaimable.Memory().Value()))
	return p.reclaimable, nil
}

var _ Predictor = (*priorityReclaimablePredictor)(nil)

// priorityReclaimablePredictor predicts the peak according to historical metrics of the node priority resources.
// e.g. A priorityReclaimablePredictor for Prod calculates the result based on the sum of the percentile of the
// Prod-tier and the system components parts.
type priorityReclaimablePredictor struct {
	predictServer         PredictServer
	safetyMarginPercent   int
	priorityClassFilterFn func(p extension.PriorityClass) bool // return true if the priority class is reclaimable

	reclaimRequest v1.ResourceList
}

// GetPredictorName is used to obtain the predictor name.
func (n *priorityReclaimablePredictor) GetPredictorName() string {
	return "priorityReclaimablePredictor"
}

func (n *priorityReclaimablePredictor) AddPod(pod *v1.Pod) error {
	priorityClass := extension.GetPodPriorityClassWithDefault(pod)
	if !n.priorityClassFilterFn(priorityClass) {
		klog.V(6).Infof("priorityReclaimablePredictor skip pod %s whose priority %s is not reclaimable",
			pod.UID, priorityClass)
		return nil
	}
	// TBD: handle the cold start pods if necessary.

	// Pods in terminating stage have 0 reclaimable resources
	// Terminated pods are not running and do not need to predict.
	if pod.DeletionTimestamp != nil || util.IsPodTerminated(pod) {
		return nil
	}

	podRequests := util.GetPodRequest(pod, v1.ResourceCPU, v1.ResourceMemory)
	n.reclaimRequest = quotav1.Add(n.reclaimRequest, podRequests)

	return nil
}

func (n *priorityReclaimablePredictor) GetResult() (v1.ResourceList, error) {
	// get sys prediction
	sysResult, err := n.predictServer.GetPrediction(MetricDesc{UID: getNodeItemUID(SystemItemID)})
	if err != nil {
		return nil, fmt.Errorf("failed to get prediction of sys, err: %w", err)
	}
	sysResultForCPU := sysResult.Data["p95"]
	sysResultForMemory := sysResult.Data["p98"]
	reclaimPredict := v1.ResourceList{
		v1.ResourceCPU:    *sysResultForCPU.Cpu(),
		v1.ResourceMemory: *sysResultForMemory.Memory(),
	}

	// get reclaimable priority class prediction
	for _, priorityClass := range extension.KnownPriorityClasses {
		if !n.priorityClassFilterFn(priorityClass) {
			continue
		}

		result, err := n.predictServer.GetPrediction(MetricDesc{UID: getNodeItemUID(string(priorityClass))})
		if err != nil {
			return nil, fmt.Errorf("failed to get prediction of priority %s, err: %s", priorityClass, err)
		}

		resultForCPU := result.Data["p95"]
		resultForMemory := result.Data["p98"]
		predictResource := v1.ResourceList{
			v1.ResourceCPU:    *resultForCPU.Cpu(),
			v1.ResourceMemory: *resultForMemory.Memory(),
		}
		reclaimPredict = quotav1.Add(reclaimPredict, predictResource)
	}

	// scale with the safety margin
	ratioAfterSafetyMargin := float64(100+n.safetyMarginPercent) / 100
	reclaimPredict = v1.ResourceList{
		v1.ResourceCPU:    util.MultiplyMilliQuant(*reclaimPredict.Cpu(), ratioAfterSafetyMargin),
		v1.ResourceMemory: util.MultiplyQuant(*reclaimPredict.Memory(), ratioAfterSafetyMargin),
	}

	// reclaimable[P] := max(request[P] - peak[P], 0)
	reclaimable := quotav1.Max(quotav1.Subtract(n.reclaimRequest, reclaimPredict), util.NewZeroResourceList())
	metrics.RecordNodePredictedResourceReclaimable(string(v1.ResourceCPU), metrics.UnitCore, n.GetPredictorName(), float64(reclaimable.Cpu().MilliValue())/1000)
	metrics.RecordNodePredictedResourceReclaimable(string(v1.ResourceMemory), metrics.UnitByte, n.GetPredictorName(), float64(reclaimable.Memory().Value()))
	return reclaimable, nil
}

var _ Predictor = (*minPredictor)(nil)

// minPredictor predicts the peak according to the minimal of the results of the sub-predictors.
type minPredictor struct {
	predictors []Predictor
}

// GetPredictorName is used to obtain the predictor name.
func (m *minPredictor) GetPredictorName() string {
	return "minPredictor"
}

func (m *minPredictor) AddPod(pod *v1.Pod) error {
	for _, p := range m.predictors {
		err := p.AddPod(pod)
		if err != nil {
			return fmt.Errorf("failed to add pod (%s/%s) to %v. error: %v", pod.Namespace, pod.Name, p.GetPredictorName(), err)
		}
	}
	return nil
}

func (m *minPredictor) GetResult() (v1.ResourceList, error) {
	if len(m.predictors) <= 0 {
		return util.NewZeroResourceList(), nil
	}

	minimal, err := m.predictors[0].GetResult()
	if err != nil {
		return nil, fmt.Errorf("failed to get predictor %s result, error: %v", m.predictors[0].GetPredictorName(), err)
	}
	for i := 1; i < len(m.predictors); i++ {
		result, err := m.predictors[i].GetResult()
		if err != nil {
			return nil, fmt.Errorf("failed to get predictor %s result, error: %v", m.predictors[i].GetPredictorName(), err)
		}

		minimal = util.MinResourceList(minimal, result)
	}

	klog.V(6).Infof("minPredictor get result: %+v", minimal)
	metrics.RecordNodePredictedResourceReclaimable(string(v1.ResourceCPU), metrics.UnitCore, m.GetPredictorName(), float64(minimal.Cpu().MilliValue())/1000)
	metrics.RecordNodePredictedResourceReclaimable(string(v1.ResourceMemory), metrics.UnitByte, m.GetPredictorName(), float64(minimal.Memory().Value()))
	return minimal, nil
}

func isPodReclaimableForProd(pod *v1.Pod) bool {
	priorityClass := extension.GetPodPriorityClassWithDefault(pod)
	return isPriorityClassReclaimableForProd(priorityClass)
}

func isPriorityClassReclaimableForProd(priorityClass extension.PriorityClass) bool {
	return priorityClass == extension.PriorityProd || priorityClass == extension.PriorityNone
}
