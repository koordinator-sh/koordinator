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

package reservation

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

// TestPlugin_EventsToRegister 檢查 EnableQueueHint 打開與關掉時，事件註冊的形狀。
// 關掉時要維持現有行為（兩個事件、沒有 hint 函式）；打開時要兩個事件都帶 hint。
func TestPlugin_EventsToRegister(t *testing.T) {
	tests := []struct {
		name            string
		enableQueueHint bool
		expectHintFn    bool
	}{
		{
			name:            "queue hint 關掉時，兩個事件都不帶 hint 函式",
			enableQueueHint: false,
			expectHintFn:    false,
		},
		{
			name:            "queue hint 打開時，兩個事件都要帶 hint 函式",
			enableQueueHint: true,
			expectHintFn:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
				args.EnableQueueHint = tt.enableQueueHint
			})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)

			events, err := pl.EventsToRegister(context.TODO())
			assert.NoError(t, err)
			assert.Equal(t, 2, len(events), "應該剛好註冊 Pod 和 Reservation 兩種事件")

			expectedGVK := fmt.Sprintf("reservations.%v.%v",
				schedulingv1alpha1.GroupVersion.Version,
				schedulingv1alpha1.GroupVersion.Group)

			var podEvent, reservationEvent *fwktype.ClusterEventWithHint
			for i := range events {
				switch events[i].Event.Resource {
				case fwktype.Pod:
					podEvent = &events[i]
				case fwktype.EventResource(expectedGVK):
					reservationEvent = &events[i]
				}
			}
			assert.NotNil(t, podEvent, "Pod Delete 事件應該存在")
			assert.NotNil(t, reservationEvent, "Reservation Add|Update|Delete 事件應該存在")

			// ActionType 不論開關都不收窄，維持既有語意
			assert.Equal(t, fwktype.Delete, podEvent.Event.ActionType)
			assert.Equal(t, fwktype.Add|fwktype.Update|fwktype.Delete, reservationEvent.Event.ActionType)

			if tt.expectHintFn {
				assert.NotNil(t, podEvent.QueueingHintFn, "打開 queue hint 後 Pod 事件要帶 hint")
				assert.NotNil(t, reservationEvent.QueueingHintFn, "打開 queue hint 後 Reservation 事件要帶 hint")
			} else {
				assert.Nil(t, podEvent.QueueingHintFn)
				assert.Nil(t, reservationEvent.QueueingHintFn)
			}
		})
	}
}

// makeWaitingPodUsingReservation 造一個「有 reservation affinity」的等待中 pod。
func makeWaitingPodUsingReservation(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
			Annotations: map[string]string{
				apiext.AnnotationReservationAffinity: `{"reservationSelector":{"app":"demo"}}`,
			},
		},
	}
}

// makeWaitingPodNoReservation 造一個完全不碰 reservation 的 pod。
func makeWaitingPodNoReservation(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
		},
	}
}

// makeReservePod 造一個 reserve pod，IsReservePod 靠 annotation 判別。
func makeReservePod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
			Annotations: map[string]string{
				reservationutil.AnnotationReservePod: "true",
			},
		},
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterPodDeletion(t *testing.T) {
	type args struct {
		waitingPod *corev1.Pod
		oldObj     interface{}
	}
	tests := []struct {
		name         string
		args         args
		expectedHint fwktype.QueueingHint
	}{
		{
			name: "oldObj 不是 pod，保守 re-queue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w1"),
				oldObj:     "not-a-pod",
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "等待中的 pod 跟 reservation 無關，直接 skip",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w2"),
				oldObj:     makeReservePod("deleted-reserve"),
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "等待中的 pod 需要 reservation，且被刪的是 reserve pod，值得喚醒",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w3"),
				oldObj:     makeReservePod("deleted-reserve"),
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "等待中的 pod 需要 reservation，但被刪的 pod 跟 reservation 毫無關係，skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w4"),
				oldObj:     makeWaitingPodNoReservation("deleted-normal"),
			},
			expectedHint: fwktype.QueueSkip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
				args.EnableQueueHint = true
			})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)

			got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), tt.args.waitingPod, tt.args.oldObj, nil)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterReservationChange(t *testing.T) {
	// IsReservationActive 要求 Status.NodeName 不空、Phase 是 Available 或 Waiting
	activeReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-active", UID: "r-active"},
		Status: schedulingv1alpha1.ReservationStatus{
			Phase:    schedulingv1alpha1.ReservationAvailable,
			NodeName: "node-1",
		},
	}
	pendingReservation := &schedulingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "r-pending", UID: "r-pending"},
		Status:     schedulingv1alpha1.ReservationStatus{Phase: schedulingv1alpha1.ReservationPending},
	}

	type args struct {
		waitingPod *corev1.Pod
		oldObj     interface{}
		newObj     interface{}
	}
	tests := []struct {
		name         string
		args         args
		expectedHint fwktype.QueueingHint
	}{
		{
			name: "不是 Reservation 型別，保守 re-queue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w1"),
				oldObj:     nil,
				newObj:     "not-a-reservation",
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "等待中的 pod 跟 reservation 無關，所有變化都 skip",
			args: args{
				waitingPod: makeWaitingPodNoReservation("w2"),
				oldObj:     nil,
				newObj:     activeReservation,
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "Add 一個 Available reservation，值得 re-queue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w3"),
				oldObj:     nil,
				newObj:     activeReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Add 一個還沒 ready 的 reservation，先 skip",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w4"),
				oldObj:     nil,
				newObj:     pendingReservation,
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "Update：從 pending 變 available，值得 re-queue",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w5"),
				oldObj:     pendingReservation,
				newObj:     activeReservation,
			},
			expectedHint: fwktype.Queue,
		},
		{
			name: "Update：兩邊都 Available、沒實質變化，skip 避免 noise",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w6"),
				oldObj:     activeReservation,
				newObj:     activeReservation,
			},
			expectedHint: fwktype.QueueSkip,
		},
		{
			name: "Delete：既然有用 reservation 的 pod 在等，給它一次重新評估機會",
			args: args{
				waitingPod: makeWaitingPodUsingReservation("w7"),
				oldObj:     activeReservation,
				newObj:     nil,
			},
			expectedHint: fwktype.Queue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuitWith(t, nil, nil, func(args *config.ReservationArgs) {
				args.EnableQueueHint = true
			})
			p, err := suit.pluginFactory()
			assert.NoError(t, err)
			pl := p.(*Plugin)

			got, err := pl.isSchedulableAfterReservationChange(klog.Background(), tt.args.waitingPod, tt.args.oldObj, tt.args.newObj)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}
