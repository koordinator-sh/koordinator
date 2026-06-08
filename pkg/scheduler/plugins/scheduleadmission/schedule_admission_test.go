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

package scheduleadmission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestNew(t *testing.T) {
	plugin, err := New(context.Background(), nil, nil)
	assert.NoError(t, err)
	assert.NotNil(t, plugin)
	assert.Equal(t, Name, plugin.Name())
}

func TestPreEnqueue(t *testing.T) {
	pl := &Plugin{}

	tests := []struct {
		name       string
		pod        *corev1.Pod
		wantStatus fwktype.Code
	}{
		{
			name: "pod with no labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
				},
			},
			wantStatus: fwktype.Success,
		},
		{
			name: "pod with unrelated labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						"app": "test",
					},
				},
			},
			wantStatus: fwktype.Success,
		},
		{
			name: "pod with one schedule-admission label",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "quota-check": "true",
					},
				},
			},
			wantStatus: fwktype.UnschedulableAndUnresolvable,
		},
		{
			name: "pod with multiple schedule-admission labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "quota-check":    "true",
						extension.LabelScheduleAdmissionPrefix + "resource-ready": "true",
					},
				},
			},
			wantStatus: fwktype.UnschedulableAndUnresolvable,
		},
		{
			name: "pod with mixed labels including schedule-admission",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						"app": "test",
						extension.LabelScheduleAdmissionPrefix + "quota-check": "true",
					},
				},
			},
			wantStatus: fwktype.UnschedulableAndUnresolvable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := pl.PreEnqueue(context.Background(), tt.pod)
			assert.Equal(t, tt.wantStatus, status.Code())
		})
	}
}

func TestEventsToRegister(t *testing.T) {
	pl := &Plugin{}
	events, err := pl.EventsToRegister(context.Background())
	assert.NoError(t, err)
	assert.Len(t, events, 1)
	assert.Equal(t, fwktype.Pod, events[0].Event.Resource)
	assert.Equal(t, fwktype.UpdatePodLabel, events[0].Event.ActionType)
	assert.NotNil(t, events[0].QueueingHintFn)
}

func TestIsScheduleAdmissionLabelRemoved(t *testing.T) {
	pl := &Plugin{}
	logger := klog.Background()
	podUID := types.UID("test-uid")

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gated-pod",
			UID:  podUID,
		},
	}

	tests := []struct {
		name     string
		oldObj   any
		newObj   any
		wantHint fwktype.QueueingHint
		wantErr  bool
	}{
		{
			name: "different pod updated",
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-pod",
					UID:  types.UID("other-uid"),
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "quota-check": "true",
					},
				},
			},
			newObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "other-pod",
					UID:  types.UID("other-uid"),
				},
			},
			wantHint: fwktype.QueueSkip,
		},
		{
			name: "schedule-admission label removed",
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "quota-check": "true",
					},
				},
			},
			newObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
				},
			},
			wantHint: fwktype.Queue,
		},
		{
			name: "unrelated label changed, schedule-admission unchanged",
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "quota-check": "true",
						"app": "v1",
					},
				},
			},
			newObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "quota-check": "true",
						"app": "v2",
					},
				},
			},
			wantHint: fwktype.QueueSkip,
		},
		{
			name: "schedule-admission label added, not removed",
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
				},
			},
			newObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "quota-check": "true",
					},
				},
			},
			wantHint: fwktype.QueueSkip,
		},
		{
			name: "one of multiple gates removed",
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "quota-check":    "true",
						extension.LabelScheduleAdmissionPrefix + "resource-ready": "true",
					},
				},
			},
			newObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
					Labels: map[string]string{
						extension.LabelScheduleAdmissionPrefix + "resource-ready": "true",
					},
				},
			},
			wantHint: fwktype.Queue,
		},
		{
			name:     "invalid old object type",
			oldObj:   "not-a-pod",
			newObj:   &corev1.Pod{},
			wantHint: fwktype.Queue,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint, err := pl.isScheduleAdmissionLabelRemoved(logger, pod, tt.oldObj, tt.newObj)
			assert.Equal(t, tt.wantHint, hint)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
