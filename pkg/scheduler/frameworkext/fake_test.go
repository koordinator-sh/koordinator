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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestFakeNominator_NominatedReservePodForNode(t *testing.T) {
	tests := []struct {
		name             string
		nominatedPods    map[string][]*corev1.Pod
		nodeName         string
		expectedPodCount int
	}{
		{
			name:             "empty nominator",
			nominatedPods:    map[string][]*corev1.Pod{},
			nodeName:         "test-node",
			expectedPodCount: 0,
		},
		{
			name: "single nominated pod on node",
			nominatedPods: map[string][]*corev1.Pod{
				"test-node": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pod-1",
							Namespace: "default",
							UID:       types.UID("pod-1-uid"),
						},
					},
				},
			},
			nodeName:         "test-node",
			expectedPodCount: 1,
		},
		{
			name: "multiple nominated pods on same node",
			nominatedPods: map[string][]*corev1.Pod{
				"test-node": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pod-1",
							Namespace: "default",
							UID:       types.UID("pod-1-uid"),
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pod-2",
							Namespace: "default",
							UID:       types.UID("pod-2-uid"),
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pod-3",
							Namespace: "default",
							UID:       types.UID("pod-3-uid"),
						},
					},
				},
			},
			nodeName:         "test-node",
			expectedPodCount: 3,
		},
		{
			name: "nominated pods on different nodes",
			nominatedPods: map[string][]*corev1.Pod{
				"test-node": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pod-1",
							Namespace: "default",
							UID:       types.UID("pod-1-uid"),
						},
					},
				},
				"other-node": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pod-2",
							Namespace: "default",
							UID:       types.UID("pod-2-uid"),
						},
					},
				},
			},
			nodeName:         "test-node",
			expectedPodCount: 1,
		},
		{
			name: "query non-existent node",
			nominatedPods: map[string][]*corev1.Pod{
				"other-node": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pod-1",
							Namespace: "default",
							UID:       types.UID("pod-1-uid"),
						},
					},
				},
			},
			nodeName:         "test-node",
			expectedPodCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nominator := NewFakeReservationNominator()

			// Add nominated pods
			for nodeName, pods := range tt.nominatedPods {
				for _, pod := range pods {
					nominator.AddNominatedReservePod(pod, nodeName)
				}
			}

			// Get nominated pods for the test node
			result := nominator.NominatedReservePodForNode(tt.nodeName)

			// Verify the count
			assert.Equal(t, tt.expectedPodCount, len(result), "unexpected number of nominated pods")

			// Verify that the result is a deep copy
			if len(result) > 0 {
				// Modify the returned PodInfo to ensure it doesn't affect the internal state
				originalUID := result[0].Pod.UID
				result[0].Pod.UID = "modified-uid"

				// Get the pods again to verify the original data is unchanged
				resultAgain := nominator.NominatedReservePodForNode(tt.nodeName)
				assert.Equal(t, originalUID, resultAgain[0].Pod.UID, "deep copy should protect internal state")

				// Verify that the pods match what was added
				expectedPods := tt.nominatedPods[tt.nodeName]
				for i, podInfo := range resultAgain {
					assert.Equal(t, expectedPods[i].UID, podInfo.Pod.UID, "pod UID should match")
					assert.Equal(t, expectedPods[i].Name, podInfo.Pod.Name, "pod name should match")
					assert.Equal(t, expectedPods[i].Namespace, podInfo.Pod.Namespace, "pod namespace should match")
				}
			}
		})
	}
}

func TestFakeNominator_NominatedReservePodForNode_Concurrency(t *testing.T) {
	nominator := NewFakeReservationNominator()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       types.UID("test-pod-uid"),
		},
	}

	nodeName := "test-node"
	nominator.AddNominatedReservePod(pod, nodeName)

	// Test concurrent reads to ensure thread safety
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			result := nominator.NominatedReservePodForNode(nodeName)
			assert.Equal(t, 1, len(result))
			assert.Equal(t, pod.UID, result[0].Pod.UID)
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestFakeNominator_NominatedReservePodForNode_AfterDelete(t *testing.T) {
	nominator := NewFakeReservationNominator()

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			UID:       types.UID("pod-1-uid"),
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			UID:       types.UID("pod-2-uid"),
		},
	}

	nodeName := "test-node"

	// Add two pods
	nominator.AddNominatedReservePod(pod1, nodeName)
	nominator.AddNominatedReservePod(pod2, nodeName)

	result := nominator.NominatedReservePodForNode(nodeName)
	assert.Equal(t, 2, len(result))

	// Delete one pod
	nominator.DeleteNominatedReservePod(pod1)

	result = nominator.NominatedReservePodForNode(nodeName)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, pod2.UID, result[0].Pod.UID)

	// Delete the second pod
	nominator.DeleteNominatedReservePod(pod2)

	result = nominator.NominatedReservePodForNode(nodeName)
	assert.Equal(t, 0, len(result))
}

func TestFakeNominator_NominatedReservePodForNode_UpdatePod(t *testing.T) {
	nominator := NewFakeReservationNominator()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       types.UID("test-pod-uid"),
		},
	}

	nodeName1 := "node-1"
	nodeName2 := "node-2"

	// Add pod to node-1
	nominator.AddNominatedReservePod(pod, nodeName1)

	result := nominator.NominatedReservePodForNode(nodeName1)
	assert.Equal(t, 1, len(result))

	// Re-add the same pod to node-2 (should remove from node-1)
	nominator.AddNominatedReservePod(pod, nodeName2)

	result = nominator.NominatedReservePodForNode(nodeName1)
	assert.Equal(t, 0, len(result), "pod should be removed from node-1")

	result = nominator.NominatedReservePodForNode(nodeName2)
	assert.Equal(t, 1, len(result), "pod should be on node-2")
	assert.Equal(t, pod.UID, result[0].Pod.UID)
}

func TestFakeNominator_NominatedReservePodForNode_EmptyNodeName(t *testing.T) {
	nominator := NewFakeReservationNominator()

	result := nominator.NominatedReservePodForNode("")
	assert.NotNil(t, result, "should return non-nil slice")
	assert.Equal(t, 0, len(result), "should return empty slice for empty node name")
}
