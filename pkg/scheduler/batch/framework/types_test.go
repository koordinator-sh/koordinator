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

package framework

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fwktype "k8s.io/kube-scheduler/framework"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
)

func newPod(namespace, name, resourceVersion string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, ResourceVersion: resourceVersion},
	}
}

func podRequest(namespace, name, node string) PodRequest {
	return PodRequest{NodeName: node, Pod: newPod(namespace, name, "1")}
}

func TestGetPodKey(t *testing.T) {
	assert.Equal(t, "ns/p1", GetPodKey(newPod("ns", "p1", "1")))
}

func TestJobRequestStringAndID(t *testing.T) {
	var nilJR *JobRequest
	assert.Equal(t, "", nilJR.String())
	assert.Equal(t, "", nilJR.ID())

	jr := &JobRequest{Namespace: "ns", JobName: "job"}
	assert.Equal(t, "ns/job", jr.String())
	assert.Equal(t, "ns/job", jr.ID())
}

func TestJobRequestClone(t *testing.T) {
	jr := &JobRequest{
		Version:       2,
		SchedulerName: "koord",
		Namespace:     "ns",
		JobName:       "job",
		MinMember:     2,
		Active:        true,
		Requeued:      true,
		PodsByNode: map[string]map[string]PodRequest{
			"node-a": {"ns/p1": podRequest("ns", "p1", "node-a")},
			"node-b": nil,
		},
	}
	clone := jr.Clone()
	assert.Equal(t, jr.Version, clone.Version)
	assert.Equal(t, jr.SchedulerName, clone.SchedulerName)
	assert.Equal(t, jr.MinMember, clone.MinMember)
	assert.True(t, clone.Active)
	assert.True(t, clone.Requeued)
	assert.Contains(t, clone.PodsByNode, "node-a")
	assert.Nil(t, clone.PodsByNode["node-b"])

	// Mutating the clone's pod map must not affect the original (deep copy of the node maps).
	delete(clone.PodsByNode["node-a"], "ns/p1")
	assert.Contains(t, jr.PodsByNode["node-a"], "ns/p1")
}

func TestJobRequestSub(t *testing.T) {
	jr := &JobRequest{PodsByNode: map[string]map[string]PodRequest{
		"node-a": {"ns/p1": podRequest("ns", "p1", "node-a"), "ns/p2": podRequest("ns", "p2", "node-a")},
		"node-b": {"ns/p3": podRequest("ns", "p3", "node-b")},
		"node-c": {}, // empty -> skipped by the len<=0 guard
	}}
	req := &JobRequest{PodsByNode: map[string]map[string]PodRequest{
		"node-a": {"ns/p1": podRequest("ns", "p1", "node-a")}, // remove one, keep p2
		// node-b absent in req -> whole node removed
	}}
	updated := jr.Sub(req)
	assert.True(t, updated)
	assert.Contains(t, jr.PodsByNode["node-a"], "ns/p2")
	assert.NotContains(t, jr.PodsByNode["node-a"], "ns/p1")
	_, hasNodeB := jr.PodsByNode["node-b"]
	assert.False(t, hasNodeB)

	// Removing everything on a node deletes the node entry.
	req2 := &JobRequest{PodsByNode: map[string]map[string]PodRequest{
		"node-a": {"ns/p2": podRequest("ns", "p2", "node-a")},
	}}
	jr.Sub(req2)
	_, hasNodeA := jr.PodsByNode["node-a"]
	assert.False(t, hasNodeA)

	// No-op sub returns false.
	assert.False(t, (&JobRequest{PodsByNode: map[string]map[string]PodRequest{}}).Sub(req2))
}

func TestJobRequestMerge(t *testing.T) {
	jr := &JobRequest{
		MinMember:  1,
		PodsByNode: map[string]map[string]PodRequest{"node-a": {"ns/p1": podRequest("ns", "p1", "node-a")}},
	}
	req := &JobRequest{
		MinMember: 3,
		Active:    true,
		PodsByNode: map[string]map[string]PodRequest{
			"node-a": {"ns/p1": podRequest("ns", "p1", "node-a"), "ns/p2": podRequest("ns", "p2", "node-a")},
			"node-b": {"ns/p3": podRequest("ns", "p3", "node-b")},
			"node-c": {}, // empty -> skipped
		},
	}
	updated := jr.Merge(req)
	assert.True(t, updated)
	assert.Equal(t, 3, jr.MinMember)
	assert.True(t, jr.Active)
	assert.Contains(t, jr.PodsByNode["node-a"], "ns/p2")
	assert.Contains(t, jr.PodsByNode["node-b"], "ns/p3")

	// Merging identical content is a no-op.
	assert.False(t, jr.Merge(&JobRequest{MinMember: 1, PodsByNode: map[string]map[string]PodRequest{
		"node-a": {"ns/p1": podRequest("ns", "p1", "node-a")},
	}}))
}

func TestJobRequestUpdate(t *testing.T) {
	base := func() *JobRequest {
		return &JobRequest{
			Version:       2,
			SchedulerName: "koord",
			Namespace:     "ns",
			JobName:       "job",
			MinMember:     1,
			PodsByNode:    map[string]map[string]PodRequest{"node-a": {"ns/p1": podRequest("ns", "p1", "node-a")}},
		}
	}

	// Different job -> false.
	jr := base()
	assert.False(t, jr.Update(&JobRequest{Namespace: "ns", JobName: "other", SchedulerName: "koord"}))
	// Different scheduler -> false.
	assert.False(t, jr.Update(&JobRequest{Namespace: "ns", JobName: "job", SchedulerName: "other"}))
	// Older version -> false.
	assert.False(t, jr.Update(&JobRequest{Namespace: "ns", JobName: "job", SchedulerName: "koord", Version: 1}))

	// Newer version -> replaced with clone.
	jr = base()
	newer := &JobRequest{
		Version: 3, Namespace: "ns", JobName: "job", SchedulerName: "koord", MinMember: 5,
		PodsByNode: map[string]map[string]PodRequest{"node-z": {"ns/p9": podRequest("ns", "p9", "node-z")}},
	}
	assert.True(t, jr.Update(newer))
	assert.Equal(t, int64(3), jr.Version)
	assert.Equal(t, 5, jr.MinMember)
	assert.Contains(t, jr.PodsByNode, "node-z")

	// Same version -> merge.
	jr = base()
	assert.True(t, jr.Update(&JobRequest{
		Version: 2, Namespace: "ns", JobName: "job", SchedulerName: "koord",
		PodsByNode: map[string]map[string]PodRequest{"node-b": {"ns/p2": podRequest("ns", "p2", "node-b")}},
	}))
	assert.Contains(t, jr.PodsByNode, "node-b")
}

func TestJobRequestIsActive(t *testing.T) {
	// Already active.
	assert.True(t, (&JobRequest{Active: true}).IsActive())
	// Reaches min member -> becomes active.
	jr := &JobRequest{MinMember: 2, PodsByNode: map[string]map[string]PodRequest{
		"node-a": {"ns/p1": podRequest("ns", "p1", "node-a"), "ns/p2": podRequest("ns", "p2", "node-a")},
	}}
	assert.True(t, jr.IsActive())
	assert.True(t, jr.Active)
	// Below min member -> not active.
	assert.False(t, (&JobRequest{MinMember: 5, PodsByNode: map[string]map[string]PodRequest{
		"node-a": {"ns/p1": podRequest("ns", "p1", "node-a")},
	}}).IsActive())
}

func TestJobRequestIsEmpty(t *testing.T) {
	assert.True(t, (&JobRequest{}).IsEmpty())
	assert.False(t, (&JobRequest{PodsByNode: map[string]map[string]PodRequest{
		"node-a": {"ns/p1": podRequest("ns", "p1", "node-a")},
	}}).IsEmpty())
}

func TestPodRequest(t *testing.T) {
	var nilPR *PodRequest
	assert.Equal(t, "", nilPR.ID())
	assert.Equal(t, "", nilPR.String())
	assert.False(t, nilPR.IsUpdated(nil))

	pr := podRequest("ns", "p1", "node-a")
	assert.Equal(t, "ns/p1/node-a", pr.ID())
	assert.Equal(t, "ns/p1/node-a", pr.String())

	clone := pr.Clone()
	assert.Equal(t, pr.NodeName, clone.NodeName)
	assert.Same(t, pr.Pod, clone.Pod)

	// ID/String with nil pod.
	empty := &PodRequest{NodeName: "node-a"}
	assert.Equal(t, "", empty.ID())

	// IsUpdated: same node + same resourceVersion -> not updated.
	same := &PodRequest{NodeName: "node-a", Pod: newPod("ns", "p1", "1")}
	assert.False(t, pr.IsUpdated(same))
	// Different resourceVersion -> updated.
	diff := &PodRequest{NodeName: "node-a", Pod: newPod("ns", "p1", "2")}
	assert.True(t, pr.IsUpdated(diff))
	// Different node -> updated.
	diffNode := &PodRequest{NodeName: "node-b", Pod: newPod("ns", "p1", "1")}
	assert.True(t, pr.IsUpdated(diffNode))
	// nil arg -> false.
	assert.False(t, pr.IsUpdated(nil))
}

func TestJobResult(t *testing.T) {
	var nilJR *JobResult
	assert.Equal(t, "", nilJR.Message())

	jr := &JobResult{}
	// No status yet -> empty message.
	assert.Equal(t, "", jr.Message())

	// Success status -> empty message.
	jr.Status = fwktype.NewStatus(fwktype.Success)
	assert.Equal(t, "", jr.Message())

	// Record per-pod statuses.
	p1 := newPod("ns", "p1", "1")
	p2 := newPod("ns", "p2", "1")
	p3 := newPod("ns", "p3", "1")
	jr.SetPodStatus(p1, fwktype.NewStatus(fwktype.Unschedulable, "no fit"))
	jr.SetPodStatus(p2, fwktype.NewStatus(fwktype.Success))
	jr.SetPodStatus(p3, fwktype.NewStatus(fwktype.Skip))

	assert.Equal(t, fwktype.Unschedulable, jr.PodStatus("ns/p1").Code())
	assert.Nil(t, jr.PodStatus("ns/absent"))

	// Failing job message includes the failing pod but not the success/skip pods.
	jr.Status = fwktype.NewStatus(fwktype.Unschedulable, "job failed")
	msg := jr.Message()
	assert.Contains(t, msg, "job failed due to")
	assert.Contains(t, msg, "ns/p1")
	assert.NotContains(t, msg, "ns/p2")
	assert.NotContains(t, msg, "ns/p3")
}

func TestAssumeContext(t *testing.T) {
	state := schedulerframework.NewCycleState()
	ac := AssumeContext{Pod: newPod("ns", "p1", "1"), NodeName: "node-a", CycleState: state}
	assert.Equal(t, "node-a", ac.NodeName)
	assert.Equal(t, "p1", ac.Pod.Name)
	assert.Same(t, state, ac.CycleState)
}
