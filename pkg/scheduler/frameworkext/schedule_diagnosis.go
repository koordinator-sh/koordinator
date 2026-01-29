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

package frameworkext

import (
	"fmt"
	"strconv"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var (
	dumpDiagnosis         = false
	dumpDiagnosisBlocking = false
	diagnosisQueueSize    = 1000
	diagnosisWorkerCount  = 10 // Default number of workers
)

// DumpDiagnosisSetter set dumpDiagnosis
func DumpDiagnosisSetter(val string) (string, error) {
	toDumpDiagnosis, err := strconv.ParseBool(val)
	if err != nil {
		return "", fmt.Errorf("failed set debugFilterFailure %s: %v", val, err)
	}
	dumpDiagnosis = toDumpDiagnosis
	return fmt.Sprintf("successfully set debugFilterFailure to %s", val), nil
}

func DumpDiagnosisBlockingSetter(val string) (string, error) {
	toLogDiagnosisBlocking, err := strconv.ParseBool(val)
	if err != nil {
		return "", fmt.Errorf("failed set debugFilterFailure %s: %v", val, err)
	}
	dumpDiagnosisBlocking = toLogDiagnosisBlocking
	return fmt.Sprintf("successfully set debugFilterFailure to %s", val), nil
}

func DumpDiagnosis(state *framework.CycleState) string {
	if dumpDiagnosis == false {
		return ""
	}

	diagnosis := GetDiagnosis(state)
	if diagnosis == nil {
		return ""
	}

	// Handle blocking mode
	if dumpDiagnosisBlocking {
		// For blocking mode, we still process synchronously
		dumpMessage := diagnosisQueue.processDiagnosis(diagnosis)
		return dumpMessage
	}

	diagnosisQueue.StartWorker()
	// For non-blocking mode, enqueue for asynchronous processing
	diagnosisQueue.Enqueue(diagnosis)
	return ""
}

func GetDiagnosis(state *framework.CycleState) *Diagnosis {
	diagnosis, _ := state.Read(diagnosisStateKey)
	if diagnosis == nil {
		// just for test
		return &Diagnosis{}
	}
	return diagnosis.(*Diagnosis)
}

var nowFunc = metav1.Now

const (
	diagnosisStateKey = extension.SchedulingDomainPrefix + "/diagnosis"
)

func InitDiagnosis(state *framework.CycleState, pod *corev1.Pod) {
	questionKey := framework.GetNamespacedName(pod.Namespace, pod.Name)
	if reservation.IsReservePod(pod) {
		questionKey = reservation.GetReservationNameFromReservePod(pod)
	}
	state.Write(diagnosisStateKey, &Diagnosis{
		Timestamp:      nowFunc(),
		QuestionedKey:  questionKey,
		TargetPod:      pod,
		NominatedNode:  pod.Status.NominatedNodeName,
		IsRootCausePod: true,
	})
}

var (
	_ framework.StateData = &Diagnosis{}
)

func (d *Diagnosis) Clone() framework.StateData {
	return d
}

// Diagnosis Help diagnose the journey of the Pod in SchedulePod and PostFilter.
type Diagnosis struct {
	Timestamp            metav1.Time `json:"timestamp"`
	QuestionedKey        string      `json:"questionedKey,omitempty"`
	TargetPod            *corev1.Pod `json:"-"`
	NominatedNode        string      `json:"nominatedNode,omitempty"`
	PreFilterMessage     string      `json:"preFilterMessage,omitempty"`
	TopologyKeyToExplain string      `json:"topologyKeyToExplain,omitempty"`
	IsRootCausePod       bool        `json:"isRootCausePod"`
	// maybe modify framework.Status to cover addedNominatedPods, corresponding resourceView(such as requested and total) when failed
	ScheduleDiagnosis   *ScheduleDiagnosis   `json:"scheduleDiagnosis"`
	PreemptionDiagnosis *PreemptionDiagnosis `json:"preemptionDiagnosis"`
}

type ScheduleDiagnosis struct {
	SchedulingMode SchedulingMode `json:"-"`
	// AlreadyWaitForBoundPods and AlreadyWaitForBound only meaningful when PodSchedulingMode
	AlreadyWaitForBoundPods []*corev1.Pod             `json:"-"`
	AlreadyWaitForBound     int                       `json:"alreadyWaitForBound"`
	NodeOfferSlot           map[string]int            `json:"nodeOfferSlot,omitempty"`
	NodeToStatusMap         framework.NodeToStatusMap `json:"-"`
	// NodeFailedDetails
	NodeFailedDetails v1alpha1.NodeFailedDetails `json:"nodeFailedDetails,omitempty"`
}

type SchedulingMode string

const (
	PodSchedulingMode SchedulingMode = "Pod"
	JobSchedulingMode SchedulingMode = "Job"
)

type PreemptionDiagnosis struct {
	DryRunFilterDiagnosis *ScheduleDiagnosis `json:"dryRunFilterDiagnosis"`
	OtherDiagnosis        interface{}        `json:"otherDiagnosis"`
}

// DiagnosisQueue is a queue for handling diagnosis logs asynchronously
type DiagnosisQueue struct {
	queue chan *Diagnosis
	once  sync.Once
}

// Global diagnosis queue instance
var diagnosisQueue = &DiagnosisQueue{}

// StartWorker starts the worker goroutines for processing diagnosis logs
func (dq *DiagnosisQueue) StartWorker() {
	dq.once.Do(func() {
		diagnosisQueue.queue = make(chan *Diagnosis, diagnosisQueueSize)
		for i := 0; i < diagnosisWorkerCount; i++ {
			go dq.worker()
		}
	})
}

// worker processes diagnosis logs from the queue
func (dq *DiagnosisQueue) worker() {
	for diagnosis := range dq.queue {
		dq.processDiagnosis(diagnosis)
	}
}

// processDiagnosis handles the actual logging of diagnosis information
func (dq *DiagnosisQueue) processDiagnosis(diagnosis *Diagnosis) string {
	// Process NodeFailedDetails if empty
	if diagnosis.ScheduleDiagnosis != nil {
		if len(diagnosis.ScheduleDiagnosis.NodeFailedDetails) == 0 {
			diagnosis.ScheduleDiagnosis.NodeFailedDetails = convertStatusMapToFailedDetail(diagnosis.ScheduleDiagnosis.NodeToStatusMap)
		}

		if diagnosis.ScheduleDiagnosis.SchedulingMode == PodSchedulingMode {
			if len(diagnosis.ScheduleDiagnosis.AlreadyWaitForBoundPods) > 0 {
				diagnosis.ScheduleDiagnosis.NodeOfferSlot = make(map[string]int, len(diagnosis.ScheduleDiagnosis.AlreadyWaitForBoundPods))
				for _, pod := range diagnosis.ScheduleDiagnosis.AlreadyWaitForBoundPods {
					diagnosis.ScheduleDiagnosis.NodeOfferSlot[pod.Spec.NodeName] = diagnosis.ScheduleDiagnosis.NodeOfferSlot[pod.Spec.NodeName] + 1
				}
			}
		}
	}

	if diagnosis.PreemptionDiagnosis != nil && diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis != nil {
		if len(diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.NodeFailedDetails) == 0 {
			diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.NodeFailedDetails = convertStatusMapToFailedDetail(diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.NodeToStatusMap)
		}
		if diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.SchedulingMode == PodSchedulingMode {
			if len(diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.AlreadyWaitForBoundPods) > 0 {
				diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.NodeOfferSlot = make(map[string]int, len(diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.AlreadyWaitForBoundPods))
				for _, pod := range diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.AlreadyWaitForBoundPods {
					diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.NodeOfferSlot[pod.Spec.NodeName] = diagnosis.PreemptionDiagnosis.DryRunFilterDiagnosis.NodeOfferSlot[pod.Spec.NodeName] + 1
				}
			}
		}
	}
	dumpMessage := util.DumpJSON(diagnosis)
	klog.Infof("dump diagnosis for %s, targetPod: %s/%s/%s: $%s", diagnosis.QuestionedKey, diagnosis.TargetPod.Namespace, diagnosis.TargetPod.Name, diagnosis.TargetPod.UID, dumpMessage)
	return dumpMessage
}

func convertStatusMapToFailedDetail(statusMap framework.NodeToStatusMap) v1alpha1.NodeFailedDetails {
	if len(statusMap) == 0 {
		return nil
	}
	statusToNodeFailedDetails := map[v1alpha1.NodeFailedStatus]*v1alpha1.NodeFailedDetail{}
	for s, status := range statusMap {
		failedStatus := v1alpha1.NodeFailedStatus{
			Reason:           status.Message(),
			FailedPlugin:     status.FailedPlugin(),
			PreemptMightHelp: status.Code() != framework.UnschedulableAndUnresolvable,
		}
		failedDetail, ok := statusToNodeFailedDetails[failedStatus]
		if !ok {
			failedDetail = &v1alpha1.NodeFailedDetail{NodeFailedStatus: failedStatus}
			statusToNodeFailedDetails[failedStatus] = failedDetail
		}
		failedDetail.FailedNodes = append(failedDetail.FailedNodes, s)
	}
	var failedDetails v1alpha1.NodeFailedDetails
	for _, detail := range statusToNodeFailedDetails {
		failedDetails = append(failedDetails, detail)
	}
	extension.SortNodeFailedDetails(failedDetails)
	return failedDetails
}

// Enqueue adds a diagnosis to the queue for asynchronous processing
func (dq *DiagnosisQueue) Enqueue(diagnosis *Diagnosis) {
	select {
	case dq.queue <- diagnosis:
	default:
		// If the queue is full, drop the diagnosis to prevent blocking
		klog.Warningf("Diagnosis queue is full, dropping diagnosis for %s", diagnosis.QuestionedKey)
	}
}
