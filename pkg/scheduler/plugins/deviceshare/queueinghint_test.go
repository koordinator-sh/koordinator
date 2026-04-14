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

package deviceshare

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
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
			suit := newPluginTestSuit(t, nil)
			args := getDefaultArgs()
			args.EnableQueueHint = tt.enableQueueHint

			p, err := suit.proxyNew(context.TODO(), args, suit.Framework)
			assert.NoError(t, err)
			pl := p.(*Plugin)

			events, err := pl.EventsToRegister(context.TODO())
			assert.NoError(t, err)
			assert.Equal(t, 2, len(events))

			expectedGVK := fmt.Sprintf("devices.%v.%v",
				schedulingv1alpha1.GroupVersion.Version,
				schedulingv1alpha1.GroupVersion.Group)

			var podEv, devEv *fwktype.ClusterEventWithHint
			for i := range events {
				switch events[i].Event.Resource {
				case fwktype.Pod:
					podEv = &events[i]
				case fwktype.EventResource(expectedGVK):
					devEv = &events[i]
				}
			}
			assert.NotNil(t, podEv)
			assert.NotNil(t, devEv)
			assert.Equal(t, fwktype.Delete, podEv.Event.ActionType)
			assert.Equal(t, fwktype.Add|fwktype.Update|fwktype.Delete, devEv.Event.ActionType)

			if tt.expectHintFn {
				assert.NotNil(t, podEv.QueueingHintFn)
				assert.NotNil(t, devEv.QueueingHintFn)
			} else {
				assert.Nil(t, podEv.QueueingHintFn)
				assert.Nil(t, devEv.QueueingHintFn)
			}
		})
	}
}

func makePodRequestingGPU(name, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
		},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{
				{
					Name: "c",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							apiext.ResourceGPU: resource.MustParse("100"),
						},
					},
				},
			},
		},
	}
}

func makePodNoDevice(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "c",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
			},
		},
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterPodDeletion(t *testing.T) {
	tests := []struct {
		name         string
		waitingPod   *corev1.Pod
		deletedObj   interface{}
		expectedHint fwktype.QueueingHint
	}{
		{
			name:         "oldObj has the wrong type, fall back to Queue",
			waitingPod:   makePodRequestingGPU("w1", ""),
			deletedObj:   "not-a-pod",
			expectedHint: fwktype.Queue,
		},
		{
			name:         "nil deleted pod, fall back to Queue",
			waitingPod:   makePodRequestingGPU("w1n", ""),
			deletedObj:   (*corev1.Pod)(nil),
			expectedHint: fwktype.Queue,
		},
		{
			name:         "waiting pod does not request devices, no need to wake it up",
			waitingPod:   makePodNoDevice("w2"),
			deletedObj:   makePodRequestingGPU("deleted-gpu", "n1"),
			expectedHint: fwktype.QueueSkip,
		},
		{
			name:         "waiting pod requests GPU and the deleted pod also held GPU, requeue",
			waitingPod:   makePodRequestingGPU("w3", ""),
			deletedObj:   makePodRequestingGPU("deleted-gpu", "n1"),
			expectedHint: fwktype.Queue,
		},
		{
			name:         "waiting pod requests GPU but the deleted pod held no device resources",
			waitingPod:   makePodRequestingGPU("w4", ""),
			deletedObj:   makePodNoDevice("deleted-norm"),
			expectedHint: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			args := getDefaultArgs()
			args.EnableQueueHint = true
			p, err := suit.proxyNew(context.TODO(), args, suit.Framework)
			assert.NoError(t, err)
			pl := p.(*Plugin)

			got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), tt.waitingPod, tt.deletedObj, nil)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterDeviceChange(t *testing.T) {
	dev := &schedulingv1alpha1.Device{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec: schedulingv1alpha1.DeviceSpec{
			Devices: []schedulingv1alpha1.DeviceInfo{
				{Type: schedulingv1alpha1.GPU, Minor: func() *int32 { i := int32(0); return &i }(), Health: true},
			},
		},
	}

	tests := []struct {
		name         string
		waitingPod   *corev1.Pod
		oldObj       interface{}
		newObj       interface{}
		expectedHint fwktype.QueueingHint
	}{
		{
			name:         "wrong type, fall back to Queue",
			waitingPod:   makePodRequestingGPU("w1", ""),
			oldObj:       nil,
			newObj:       "not-a-device",
			expectedHint: fwktype.Queue,
		},
		{
			name:         "waiting pod needs no device, skip any change",
			waitingPod:   makePodNoDevice("w2"),
			oldObj:       nil,
			newObj:       dev,
			expectedHint: fwktype.QueueSkip,
		},
		{
			name:         "Add a device, requeue",
			waitingPod:   makePodRequestingGPU("w3", ""),
			oldObj:       nil,
			newObj:       dev,
			expectedHint: fwktype.Queue,
		},
		{
			name:         "Update a device (capacity may have shifted), requeue",
			waitingPod:   makePodRequestingGPU("w4", ""),
			oldObj:       dev,
			newObj:       dev,
			expectedHint: fwktype.Queue,
		},
		{
			name:         "Delete device cannot unblock a waiting consumer, skip",
			waitingPod:   makePodRequestingGPU("w5", ""),
			oldObj:       dev,
			newObj:       nil,
			expectedHint: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			args := getDefaultArgs()
			args.EnableQueueHint = true
			p, err := suit.proxyNew(context.TODO(), args, suit.Framework)
			assert.NoError(t, err)
			pl := p.(*Plugin)

			got, err := pl.isSchedulableAfterDeviceChange(klog.Background(), tt.waitingPod, tt.oldObj, tt.newObj)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}
