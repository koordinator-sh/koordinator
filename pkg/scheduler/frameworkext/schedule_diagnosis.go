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
	"sort"
	"strconv"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var (
	dumpDiagnosis         = false
	dumpDiagnosisBlocking = false
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
	blocking := dumpDiagnosisBlocking
	var wg sync.WaitGroup
	if blocking {
		wg.Add(1)
	}
	var dumpMessage string
	go func() {
		// TODO make this logic to be executed asynchronously and orderly
		if len(diagnosis.ScheduleDiagnosis.NodeFailedDetails) == 0 {
			var scheduleFailedDetails []v1alpha1.NodeFailedDetail
			for s, status := range diagnosis.ScheduleDiagnosis.NodeToStatusMap {
				scheduleFailedDetails = append(scheduleFailedDetails, v1alpha1.NodeFailedDetail{
					NodeName:         s,
					FailedPlugin:     status.FailedPlugin(),
					Reason:           status.Message(),
					NominatedPods:    nil,
					PreemptMightHelp: status.Code() != framework.UnschedulableAndUnresolvable,
				})
			}
			sort.Slice(scheduleFailedDetails, func(i, j int) bool { return scheduleFailedDetails[i].NodeName < scheduleFailedDetails[j].NodeName })
			diagnosis.ScheduleDiagnosis.NodeFailedDetails = scheduleFailedDetails
		}
		if len(diagnosis.PreemptionDiagnosis.NodeFailedDetails) == 0 {
			var preemptFailedDetails []v1alpha1.NodeFailedDetail
			for s, status := range diagnosis.PreemptionDiagnosis.NodeToStatusMap {
				preemptFailedDetails = append(preemptFailedDetails, v1alpha1.NodeFailedDetail{
					NodeName:         s,
					FailedPlugin:     status.FailedPlugin(),
					Reason:           status.Message(),
					NominatedPods:    nil,
					PreemptMightHelp: status.Code() != framework.UnschedulableAndUnresolvable,
				})
			}
			sort.Slice(preemptFailedDetails, func(i, j int) bool { return preemptFailedDetails[i].NodeName < preemptFailedDetails[j].NodeName })
			diagnosis.PreemptionDiagnosis.NodeFailedDetails = preemptFailedDetails
		}
		dumpMessage = util.DumpJSON(diagnosis)
		klog.Infof("dump diagnosis for %s/%s/%s: %s", diagnosis.TargetPod.Namespace, diagnosis.TargetPod.Name, diagnosis.TargetPod.UID, dumpMessage)
		// TODO export it to APIServer asynchronously
		if blocking {
			wg.Done()
		}
	}()
	if blocking {
		wg.Wait()
	}
	return dumpMessage
}

func GetDiagnosis(state *framework.CycleState) *Diagnosis {
	diagnosis, _ := state.Read(diagnosisStateKey)
	return diagnosis.(*Diagnosis)
}

var nowFunc = metav1.Now

const (
	diagnosisStateKey = extension.SchedulingDomainPrefix + "/diagnosis"
)

func initDiagnosis(state *framework.CycleState, pod *corev1.Pod) {
	state.Write(diagnosisStateKey, &Diagnosis{
		Timestamp:     nowFunc(),
		QuestionedKey: extension.GetExplanationKey(pod.Labels),
		TargetPod:     pod,
		NominatedNode: pod.Status.NominatedNodeName,
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
	// maybe modify framework.Status to cover addedNominatedPods, corresponding resourceView(such as requested and total) when failed
	ScheduleDiagnosis ScheduleDiagnosis `json:"scheduleDiagnosis"`
	// NodePossibleVictims indicates the possible victims for members that can't be scheduled.
	NodePossibleVictims []v1alpha1.NodePossibleVictim `json:"nodePossibleVictims,omitempty"`
	PreemptionDiagnosis ScheduleDiagnosis             `json:"preemptionDiagnosis"`
	NominatedPodToNodes map[string]string             `json:"nominatedPodToNodes,omitempty"`
	ToRemoveVictims     []v1alpha1.PossibleVictim     `json:"toRemoveVictims,omitempty"`
}

type ScheduleDiagnosis struct {
	SchedulingMode SchedulingMode `json:"schedulingMode,omitempty"`
	// use this when PodSchedulingMode
	AlreadyWaitForBound int `json:"alreadyWaitForBound,omitempty"`
	// use this when JobSchedulingMode
	NodeOfferSlot   map[string]int            `json:"nodeOfferSlot,omitempty"`
	NodeToStatusMap framework.NodeToStatusMap `json:"-"`
	// NodeFailedDetails
	NodeFailedDetails []v1alpha1.NodeFailedDetail `json:"nodeFailedDetails,omitempty"`
}

type SchedulingMode string

const (
	PodSchedulingMode SchedulingMode = "Pod"
	JobSchedulingMode SchedulingMode = "Job"
)
