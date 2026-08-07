package jobnomination

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/stretchr/testify/assert"
)

func TestJobNomination_CreationAndExpiration(t *testing.T) {
	jn := &JobNomination{
		retentionDuration: 50 * time.Millisecond,
		cache:             make(map[string]map[string][]*Nomination),
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("pod-1"),
			OwnerReferences: []metav1.OwnerReference{
				{
					UID:        types.UID("job-1"),
					Controller: func(b bool) *bool { return &b }(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	// Test creation
	jn.trackPodRetention(pod)
	jn.lock.RLock()
	assert.Equal(t, 1, len(jn.cache["job-1"]["node-1"]))
	jn.lock.RUnlock()

	// Test expiration
	time.Sleep(100 * time.Millisecond)
	jn.cleanup()
	jn.lock.RLock()
	assert.Equal(t, 0, len(jn.cache["job-1"]["node-1"]))
	jn.lock.RUnlock()
}

func TestJobNomination_FilterAndScore(t *testing.T) {
	ctx := context.Background()
	jn := &JobNomination{
		retentionDuration: 5 * time.Minute,
		cache:             make(map[string]map[string][]*Nomination),
	}

	job1Pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("pod-1"),
			OwnerReferences: []metav1.OwnerReference{
				{UID: types.UID("job-1"), Controller: func(b bool) *bool { return &b }(true)},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
					},
				},
			},
		},
	}

	jn.trackPodRetention(job1Pod1)

	// Node has 10 CPU allocatable, 0 requested
	nodeInfo := framework.NewNodeInfo()
	nodeInfo.SetNode(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10")},
		},
	})

	job2Pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("pod-2"),
			OwnerReferences: []metav1.OwnerReference{
				{UID: types.UID("job-2"), Controller: func(b bool) *bool { return &b }(true)},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")},
					},
				},
			},
		},
	}

	// 1. Conflict handling: Job2 asks for 8 CPU, but 4 is retained by Job1. Only 6 available. Should fail.
	status := jn.Filter(ctx, nil, job2Pod1, nodeInfo)
	assert.False(t, status.IsSuccess())
	assert.Equal(t, fwktype.Unschedulable, status.Code())

	// 2. Replacement Pod Preference: Job1 replacement asks for 4 CPU. Should fit.
	job1Pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("pod-3"),
			OwnerReferences: []metav1.OwnerReference{
				{UID: types.UID("job-1"), Controller: func(b bool) *bool { return &b }(true)},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
					},
				},
			},
		},
	}
	status = jn.Filter(ctx, nil, job1Pod2, nodeInfo)
	assert.True(t, status.IsSuccess())

	// 3. Score behavior: Job1 replacement should get score 100 on node-1
	score, status := jn.Score(ctx, nil, job1Pod2, nodeInfo)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, int64(100), score)

	nodeInfo2 := framework.NewNodeInfo()
	nodeInfo2.SetNode(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}})

	// Score on other node should be 0
	score, status = jn.Score(ctx, nil, job1Pod2, nodeInfo2)
	assert.True(t, status.IsSuccess())
	assert.Equal(t, int64(0), score)

	// 4. Reserve behavior: Reserving should consume the nomination
	status = jn.Reserve(ctx, nil, job1Pod2, "node-1")
	assert.True(t, status.IsSuccess())

	jn.lock.RLock()
	assert.Equal(t, 0, len(jn.cache["job-1"]["node-1"]))
	jn.lock.RUnlock()

	// 5. Fallback behavior: after reserve, job2 asks for 8 CPU. Should fit now.
	status = jn.Filter(ctx, nil, job2Pod1, nodeInfo)
	assert.True(t, status.IsSuccess())
}
