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

package scaledownbinpack

import (
	"context"
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	frameworkruntime "github.com/koordinator-sh/koordinator/pkg/descheduler/framework/runtime"
)

func TestPatchDeletionCosts(t *testing.T) {
	targetPod := newTestPod("default", "target-1", "node1", "100m", "100Mi", nil)
	skippedPod := newTestPod("default", "skipped-1", "node1", "100m", "100Mi", nil)

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {targetPod, skippedPod},
	}}
	fh, _ := makeTestHandle(t, lister)

	pl := &ScaleDownBinPack{
		handle: fh,
		args:   &deschedulerconfig.ScaleDownBinPackArgs{},
	}

	ranked := []RankedPod{
		{Pod: targetPod, Rank: 0},
	}

	status := pl.patchDeletionCosts(context.Background(), ranked, []*corev1.Pod{skippedPod})
	assert.Nil(t, status)

	// Verify patches
	client := fh.ClientSet()
	updatedTarget, err := client.CoreV1().Pods(targetPod.Namespace).Get(context.Background(), targetPod.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, "0", updatedTarget.Annotations[podDeletionCostAnnotation])

	updatedSkipped, err := client.CoreV1().Pods(skippedPod.Namespace).Get(context.Background(), skippedPod.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, strconv.Itoa(math.MaxInt32), updatedSkipped.Annotations[podDeletionCostAnnotation])
}

func TestEvictRankedPods(t *testing.T) {
	pod1 := newTestPod("default", "p1", "node1", "100m", "100Mi", nil)
	pod2 := newTestPod("default", "p2", "node1", "100m", "100Mi", nil)
	pod3 := newTestPod("default", "p3", "node1", "100m", "100Mi", nil)

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {pod1, pod2, pod3},
	}}
	fh, evictor := makeTestHandle(t, lister)

	maxPods := int32(2)
	pl := &ScaleDownBinPack{
		handle: fh,
		args: &deschedulerconfig.ScaleDownBinPackArgs{
			MaxPodsToEvict: &maxPods,
		},
	}

	ranked := []RankedPod{
		{Pod: pod1, Rank: 0},
		{Pod: pod2, Rank: 1},
		{Pod: pod3, Rank: 2},
	}

	status := pl.evictRankedPods(context.Background(), ranked)
	assert.Nil(t, status)

	// Verify eviction count
	assert.Equal(t, 2, len(evictor.evicted))
	assert.Equal(t, "p1", evictor.evicted[0].Name)
	assert.Equal(t, "p2", evictor.evicted[1].Name)
}

func TestPatchDeletionCosts_DryRun(t *testing.T) {
	targetPod := newTestPod("default", "target-1", "node1", "100m", "100Mi", nil)
	skippedPod := newTestPod("default", "skipped-1", "node1", "100m", "100Mi", nil)

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {targetPod, skippedPod},
	}}
	fh, _ := makeTestHandle(t, lister, frameworkruntime.WithDryRun(true))

	pl := &ScaleDownBinPack{
		handle: fh,
		args:   &deschedulerconfig.ScaleDownBinPackArgs{},
	}

	ranked := []RankedPod{
		{Pod: targetPod, Rank: 0},
	}

	status := pl.patchDeletionCosts(context.Background(), ranked, []*corev1.Pod{skippedPod})
	assert.Nil(t, status)

	// Verify no patches were applied.
	client := fh.ClientSet()
	updatedTarget, err := client.CoreV1().Pods(targetPod.Namespace).Get(context.Background(), targetPod.Name, metav1.GetOptions{})
	assert.NoError(t, err)
	_, hasAnnotation := updatedTarget.Annotations[podDeletionCostAnnotation]
	assert.False(t, hasAnnotation, "dry-run must not patch annotations")
}

func TestEvictRankedPods_DryRun(t *testing.T) {
	pod1 := newTestPod("default", "p1", "node1", "100m", "100Mi", nil)

	lister := &podsLister{data: map[string][]*corev1.Pod{
		"node1": {pod1},
	}}
	fh, evictor := makeTestHandle(t, lister, frameworkruntime.WithDryRun(true))

	pl := &ScaleDownBinPack{
		handle: fh,
		args:   &deschedulerconfig.ScaleDownBinPackArgs{},
	}

	ranked := []RankedPod{
		{Pod: pod1, Rank: 0},
	}

	status := pl.evictRankedPods(context.Background(), ranked)
	assert.Nil(t, status)
	assert.Empty(t, evictor.evicted, "dry-run must not evict any pods")
}
