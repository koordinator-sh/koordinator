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

package nodenumaresource

import (
	"context"
	"fmt"
	"testing"

	topologyv1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func TestPlugin_EventsToRegister(t *testing.T) {
	tests := []struct {
		name            string
		enableQueueHint bool
		expectHintFn    bool
	}{
		{"no hint functions when queue hint is disabled", false, false},
		{"hint functions are set when queue hint is enabled", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, nil)
			suit.nodeNUMAResourceArgs.EnableQueueHint = tt.enableQueueHint

			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			require.NoError(t, err)
			pl := p.(*Plugin)

			events, err := pl.EventsToRegister(context.TODO())
			assert.NoError(t, err)
			assert.Equal(t, 2, len(events))

			expectedGVK := fmt.Sprintf("noderesourcetopologies.%v.%v",
				topologyv1alpha1.SchemeGroupVersion.Version,
				topologyv1alpha1.SchemeGroupVersion.Group)

			var podEv, nrtEv *fwktype.ClusterEventWithHint
			for i := range events {
				switch events[i].Event.Resource {
				case fwktype.Pod:
					podEv = &events[i]
				case fwktype.EventResource(expectedGVK):
					nrtEv = &events[i]
				}
			}
			assert.NotNil(t, podEv)
			assert.NotNil(t, nrtEv)
			assert.Equal(t, fwktype.Delete, podEv.Event.ActionType)
			assert.Equal(t, fwktype.Add|fwktype.Update|fwktype.Delete, nrtEv.Event.ActionType)

			if tt.expectHintFn {
				assert.NotNil(t, podEv.QueueingHintFn)
				assert.NotNil(t, nrtEv.QueueingHintFn)
			} else {
				assert.Nil(t, podEv.QueueingHintFn)
				assert.Nil(t, nrtEv.QueueingHintFn)
			}
		})
	}
}

func makePodWithNUMAPolicy(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
			Annotations: map[string]string{
				apiext.AnnotationNUMATopologySpec: `{"numaTopologyPolicy":"SingleNUMANode"}`,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
				},
			}},
		},
	}
}

func makePlainPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				},
			}},
		},
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterPodDeletion(t *testing.T) {
	tests := []struct {
		name         string
		waiting      *corev1.Pod
		deletedObj   interface{}
		expectedHint fwktype.QueueingHint
	}{
		{
			name:         "oldObj is not a Pod, fall back to Queue",
			waiting:      makePodWithNUMAPolicy("w1"),
			deletedObj:   "not-a-pod",
			expectedHint: fwktype.Queue,
		},
		{
			name:         "nil deleted pod, fall back to Queue",
			waiting:      makePodWithNUMAPolicy("w1n"),
			deletedObj:   (*corev1.Pod)(nil),
			expectedHint: fwktype.Queue,
		},
		{
			name:         "waiting pod does not require NUMA, no need to wake it up",
			waiting:      makePlainPod("w2"),
			deletedObj:   makePodWithNUMAPolicy("del-numa"),
			expectedHint: fwktype.QueueSkip,
		},
		{
			name:         "waiting pod requires NUMA and deleted pod is also NUMA-pinned, requeue",
			waiting:      makePodWithNUMAPolicy("w3"),
			deletedObj:   makePodWithNUMAPolicy("del-numa"),
			expectedHint: fwktype.Queue,
		},
		{
			name:         "waiting pod requires NUMA but deleted pod is plain, no NUMA resources released",
			waiting:      makePodWithNUMAPolicy("w4"),
			deletedObj:   makePlainPod("del-plain"),
			expectedHint: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, nil)
			suit.nodeNUMAResourceArgs.EnableQueueHint = true
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			require.NoError(t, err)
			pl := p.(*Plugin)

			got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), tt.waiting, tt.deletedObj, nil)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterNRTChange(t *testing.T) {
	nrt := &topologyv1alpha1.NodeResourceTopology{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
	}

	tests := []struct {
		name         string
		waiting      *corev1.Pod
		oldObj       interface{}
		newObj       interface{}
		expectedHint fwktype.QueueingHint
	}{
		{"wrong type, fall back to Queue", makePodWithNUMAPolicy("w1"), nil, "not-nrt", fwktype.Queue},
		{"waiting pod needs no NUMA, skip all", makePlainPod("w2"), nil, nrt, fwktype.QueueSkip},
		{"Add NRT, requeue", makePodWithNUMAPolicy("w3"), nil, nrt, fwktype.Queue},
		{"Update NRT, requeue", makePodWithNUMAPolicy("w4"), nrt, nrt, fwktype.Queue},
		{"Delete NRT cannot unblock a waiter, skip", makePodWithNUMAPolicy("w5"), nrt, nil, fwktype.QueueSkip},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil, nil)
			suit.nodeNUMAResourceArgs.EnableQueueHint = true
			p, err := suit.proxyNew(context.TODO(), suit.nodeNUMAResourceArgs, suit.Handle)
			require.NoError(t, err)
			pl := p.(*Plugin)

			got, err := pl.isSchedulableAfterNRTChange(klog.Background(), tt.waiting, tt.oldObj, tt.newObj)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}
