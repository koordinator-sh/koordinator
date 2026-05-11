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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestDumpDiagnosis(t *testing.T) {
	nowFunc = func() metav1.Time {
		return metav1.NewTime(time.Time{})
	}
	tests := []struct {
		name             string
		pod              *corev1.Pod
		setDiagnosisFunc func(state fwktype.CycleState)
		wantDumpMessage  string
	}{
		{
			name: "normal flow",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Labels: map[string]string{
						extension.LabelQuestionedObjectKey: "default/test-pod",
					},
				},
				Status: corev1.PodStatus{
					NominatedNodeName: "nominatedNode",
				},
			},
			setDiagnosisFunc: func(state fwktype.CycleState) {
				diagnosis := GetDiagnosis(state)
				diagnosis.PreFilterMessage = "preFilterMessage"
				diagnosis.TopologyKeyToExplain = "topologyKeyToExplain"
				diagnosis.ScheduleDiagnosis = &ScheduleDiagnosis{}
				diagnosis.ScheduleDiagnosis.NodeToStatusMap = map[string]*fwktype.Status{
					"node1": fwktype.NewStatus(fwktype.Success),
					"node2": fwktype.NewStatus(fwktype.Unschedulable, "node2-reason"),
				}
				diagnosis.ScheduleDiagnosis.AlreadyWaitForBound = 2
				diagnosis.ScheduleDiagnosis.AlreadyWaitForBoundPods = []*corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod-1",
							Namespace: "default",
						},
						Spec: corev1.PodSpec{
							NodeName: "node1",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-pod-2",
							Namespace: "default",
						},
						Spec: corev1.PodSpec{
							NodeName: "node2",
						},
					},
				}
				diagnosis.ScheduleDiagnosis.SchedulingMode = PodSchedulingMode
				diagnosis.PreemptionDiagnosis = &PreemptionDiagnosis{
					DryRunFilterDiagnosis: &ScheduleDiagnosis{
						NodeOfferSlot: map[string]int{
							"node1": 1,
							"node2": 2,
						},
						NodeToStatusMap: map[string]*fwktype.Status{
							"node1": fwktype.NewStatus(fwktype.Success),
							"node2": fwktype.NewStatus(fwktype.Unschedulable, "node2-reason"),
						},
					},
					OtherDiagnosis: struct {
						TriggerPodKey string `json:"TriggerPodKey,omitempty"`
						PreemptorKey  string `json:"preemptorKey,omitempty"`
					}{
						TriggerPodKey: "default/test-pod",
						PreemptorKey:  "default/test-pod",
					},
				}
			},
			wantDumpMessage: `{"timestamp":null,"questionedKey":"default/test-pod","nominatedNode":"nominatedNode","preFilterMessage":"preFilterMessage","topologyKeyToExplain":"topologyKeyToExplain","isRootCausePod":true,"scheduleDiagnosis":{"alreadyWaitForBound":2,"nodeOfferSlot":{"node1":1,"node2":1},"nodeFailedDetails":[{"preemptMightHelp":true,"failedNodes":["node1"]},{"reason":"node2-reason","preemptMightHelp":true,"failedNodes":["node2"]}]},"preemptionDiagnosis":{"dryRunFilterDiagnosis":{"alreadyWaitForBound":0,"nodeOfferSlot":{"node1":1,"node2":2},"nodeFailedDetails":[{"preemptMightHelp":true,"failedNodes":["node1"]},{"reason":"node2-reason","preemptMightHelp":true,"failedNodes":["node2"]}]},"otherDiagnosis":{"TriggerPodKey":"default/test-pod","preemptorKey":"default/test-pod"}}}`,
		},
		{
			name: "with suggestion",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			setDiagnosisFunc: func(state fwktype.CycleState) {
				diagnosis := GetDiagnosis(state)
				diagnosis.SetSuggestion(&ScheduleSuggestion{
					Type:    SuggestionEvictWorkloadSelf,
					Message: "pod is unschedulable, please delete and resubmit",
				})
			},
			wantDumpMessage: `{"timestamp":null,"questionedKey":"default/test-pod","isRootCausePod":true,"scheduleDiagnosis":null,"preemptionDiagnosis":null,"suggestion":{"type":"EvictWorkloadSelf","message":"pod is unschedulable, please delete and resubmit"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dumpDiagnosis = true
			dumpDiagnosisBlocking = true
			cycleState := framework.NewCycleState()
			InitDiagnosis(cycleState, tt.pod)
			tt.setDiagnosisFunc(cycleState)
			gotDumpMessage := DumpDiagnosis(GetDiagnosis(cycleState))
			assert.Equal(t, tt.wantDumpMessage, gotDumpMessage)
		})
	}
}

func TestSetSuggestion(t *testing.T) {
	tests := []struct {
		name           string
		initSuggestion *ScheduleSuggestion
		setSuggestion  *ScheduleSuggestion
		wantOk         bool
		wantSuggestion *ScheduleSuggestion
	}{
		{
			name: "set suggestion on empty diagnosis",
			setSuggestion: &ScheduleSuggestion{
				Type:    SuggestionEvictWorkloadSelf,
				Message: "pod is unschedulable, please resubmit",
			},
			wantOk: true,
			wantSuggestion: &ScheduleSuggestion{
				Type:    SuggestionEvictWorkloadSelf,
				Message: "pod is unschedulable, please resubmit",
			},
		},
		{
			name: "set suggestion when already set returns false",
			initSuggestion: &ScheduleSuggestion{
				Type:    SuggestionWaitingVictimReleased,
				Message: "waiting for victims",
			},
			setSuggestion: &ScheduleSuggestion{
				Type:    SuggestionEvictWorkloadSelf,
				Message: "should not overwrite",
			},
			wantOk: false,
			wantSuggestion: &ScheduleSuggestion{
				Type:    SuggestionWaitingVictimReleased,
				Message: "waiting for victims",
			},
		},
		{
			name: "set DeleteConflictPVC suggestion",
			setSuggestion: &ScheduleSuggestion{
				Type:    SuggestionDeleteConflictPVC,
				Message: "PVC my-pvc conflicts",
			},
			wantOk: true,
			wantSuggestion: &ScheduleSuggestion{
				Type:    SuggestionDeleteConflictPVC,
				Message: "PVC my-pvc conflicts",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Diagnosis{}
			if tt.initSuggestion != nil {
				d.Suggestion = tt.initSuggestion
			}
			gotOk := d.SetSuggestion(tt.setSuggestion)
			assert.Equal(t, tt.wantOk, gotOk)
			assert.Equal(t, tt.wantSuggestion, d.Suggestion)
		})
	}
}

func TestSetSuggestion_Concurrent(t *testing.T) {
	d := &Diagnosis{}
	const goroutines = 100
	var wg sync.WaitGroup
	var successCount int32

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ok := d.SetSuggestion(&ScheduleSuggestion{
				Type:    SuggestionEvictWorkloadSelf,
				Message: "from goroutine",
			})
			if ok {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}
	wg.Wait()
	assert.Equal(t, int32(1), successCount, "exactly one goroutine should succeed")
	assert.NotNil(t, d.Suggestion)
}

// BenchmarkDumpDiagnosis benchmarks the DumpDiagnosis function with large datasets
func BenchmarkDumpDiagnosis(b *testing.B) {
	// Create a test pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelQuestionedObjectKey: "default/test-pod",
			},
		},
		Status: corev1.PodStatus{
			NominatedNodeName: "nominatedNode",
		},
	}

	// Create large datasets
	nodeCount := 5000
	nodeToStatusMap := make(map[string]*fwktype.Status, nodeCount)
	nodeOfferSlot := make(map[string]int, nodeCount)

	for i := 0; i < nodeCount; i++ {
		nodeName := "node" + string(rune(i))
		nodeToStatusMap[nodeName] = fwktype.NewStatus(fwktype.Unschedulable, "insufficient resources")
		nodeOfferSlot[nodeName] = i
	}

	// Benchmark case 1: dumpDiagnosisBlocking = true
	b.Run("Blocking", func(b *testing.B) {
		/*
			goos: darwin
			goarch: arm64
			cpu: Apple M1 Pro
			BenchmarkDumpDiagnosis/Blocking-10         	      37	  30166972 ns/op
		*/
		dumpDiagnosis = true
		dumpDiagnosisBlocking = true

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			// Create a fresh cycle state for each iteration
			cycleState := framework.NewCycleState()
			InitDiagnosis(cycleState, pod)

			// Set up diagnosis data
			diagnosis := GetDiagnosis(cycleState)
			diagnosis.PreFilterMessage = "preFilterMessage"
			diagnosis.TopologyKeyToExplain = "topologyKeyToExplain"
			diagnosis.ScheduleDiagnosis = &ScheduleDiagnosis{
				NodeToStatusMap:   nodeToStatusMap,
				NodeOfferSlot:     nodeOfferSlot,
				NodeFailedDetails: v1alpha1.NodeFailedDetails{}, // Will be populated in DumpDiagnosis
				SchedulingMode:    JobSchedulingMode,
			}

			// Set PreemptionDiagnosis to the same content as ScheduleDiagnosis
			diagnosis.PreemptionDiagnosis = &PreemptionDiagnosis{
				DryRunFilterDiagnosis: diagnosis.ScheduleDiagnosis,
			}

			// Run the function being benchmarked
			DumpDiagnosis(GetDiagnosis(cycleState))
		}
	})

	// Benchmark case 2: dumpDiagnosisBlocking = false
	b.Run("NonBlocking", func(b *testing.B) {
		dumpDiagnosis = true
		dumpDiagnosisBlocking = false

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			// Create a fresh cycle state for each iteration
			cycleState := framework.NewCycleState()
			InitDiagnosis(cycleState, pod)

			// Set up diagnosis data
			diagnosis := GetDiagnosis(cycleState)
			diagnosis.PreFilterMessage = "preFilterMessage"
			diagnosis.TopologyKeyToExplain = "topologyKeyToExplain"
			diagnosis.ScheduleDiagnosis = &ScheduleDiagnosis{
				NodeToStatusMap:   nodeToStatusMap,
				NodeOfferSlot:     nodeOfferSlot,
				NodeFailedDetails: v1alpha1.NodeFailedDetails{}, // Will be populated in DumpDiagnosis
				SchedulingMode:    JobSchedulingMode,
			}

			// Set PreemptionDiagnosis to the same content as ScheduleDiagnosis
			diagnosis.PreemptionDiagnosis = &PreemptionDiagnosis{
				DryRunFilterDiagnosis: diagnosis.ScheduleDiagnosis,
			}

			// Run the function being benchmarked
			DumpDiagnosis(GetDiagnosis(cycleState))
		}
	})
}

// BenchmarkDumpDiagnosisWorkerCount measures the efficiency of processing 1000 diagnoses
// with different worker counts in non-blocking mode
func BenchmarkDumpDiagnosisWorkerCount(b *testing.B) {
	// Enable diagnosis in non-blocking mode
	dumpDiagnosis = true
	dumpDiagnosisBlocking = false

	// Create a test pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Labels: map[string]string{
				extension.LabelQuestionedObjectKey: "default/test-pod",
			},
		},
		Status: corev1.PodStatus{
			NominatedNodeName: "nominatedNode",
		},
	}

	// Create datasets
	nodeCount := 5000
	nodeToStatusMap := make(map[string]*fwktype.Status, nodeCount)
	nodeOfferSlot := make(map[string]int, nodeCount)

	for i := 0; i < nodeCount; i++ {
		nodeName := "node" + string(rune(i))
		nodeToStatusMap[nodeName] = fwktype.NewStatus(fwktype.Unschedulable, "insufficient resources")
		nodeOfferSlot[nodeName] = i
	}

	// Helper function to create diagnosis data
	createDiagnosis := func() *Diagnosis {
		return &Diagnosis{
			QuestionedKey:        "default/test-pod",
			TargetPod:            pod,
			PreFilterMessage:     "preFilterMessage",
			TopologyKeyToExplain: "topologyKeyToExplain",
			IsRootCausePod:       true,
			ScheduleDiagnosis: &ScheduleDiagnosis{
				NodeToStatusMap:   nodeToStatusMap,
				NodeOfferSlot:     nodeOfferSlot,
				NodeFailedDetails: v1alpha1.NodeFailedDetails{},
				SchedulingMode:    JobSchedulingMode,
			},
		}
	}

	// Benchmark with 1 worker
	b.Run("1Worker", func(b *testing.B) {
		// Set worker count to 1
		originalWorkerCount := diagnosisWorkerCount
		diagnosisWorkerCount = 1

		// Restart the diagnosis queue with new worker count
		diagnosisQueue = &DiagnosisQueue{}
		diagnosisQueue.StartWorker()

		b.ResetTimer()

		/*
			goos: darwin
			goarch: arm64
			pkg: github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext
			cpu: Apple M1 Pro
			BenchmarkDumpDiagnosisWorkerCount/1Worker-10	21	  55328246 ns/op	51861147 B/op	  200365 allocs/op
		*/
		for i := 0; i < b.N; i++ {
			// Process 1000 diagnoses
			for j := 0; j < 10; j++ {
				diagnosis := createDiagnosis()
				diagnosis.PreemptionDiagnosis = &PreemptionDiagnosis{
					DryRunFilterDiagnosis: diagnosis.ScheduleDiagnosis,
				}
				diagnosisQueue.Enqueue(diagnosis)
			}

			// Wait until the queue is empty
			for len(diagnosisQueue.queue) > 0 {
				time.Sleep(1 * time.Millisecond)
			}
		}

		// Restore original worker count
		diagnosisWorkerCount = originalWorkerCount
	})

	// Benchmark with 10 workers
	b.Run("10Workers", func(b *testing.B) {
		// Set worker count to 10
		originalWorkerCount := diagnosisWorkerCount
		diagnosisWorkerCount = 10

		// Restart the diagnosis queue with new worker count
		diagnosisQueue = &DiagnosisQueue{}
		diagnosisQueue.StartWorker()

		b.ResetTimer()

		/*
			goos: darwin
			goarch: arm64
			pkg: github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext
			cpu: Apple M1 Pro
			BenchmarkDumpDiagnosisWorkerCount/1Worker-10	100	  11389174 ns/op	58240364 B/op	  200894 allocs/op
		*/

		for i := 0; i < b.N; i++ {
			// Process 1000 diagnoses
			for j := 0; j < 10; j++ {
				diagnosis := createDiagnosis()
				diagnosis.PreemptionDiagnosis = &PreemptionDiagnosis{
					DryRunFilterDiagnosis: diagnosis.ScheduleDiagnosis,
				}
				diagnosisQueue.Enqueue(diagnosis)
			}

			// Wait until the queue is empty
			for len(diagnosisQueue.queue) > 0 {
				time.Sleep(1 * time.Millisecond)
			}
		}

		// Restore original worker count
		diagnosisWorkerCount = originalWorkerCount
	})
}
