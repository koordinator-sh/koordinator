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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name                  string
		args                  runtime.Object
		wantErr               bool
		wantEnablePrefixMatch bool
	}{
		{
			name:                  "nil args defaults to exact-match",
			args:                  nil,
			wantEnablePrefixMatch: false,
		},
		{
			name:                  "args with prefix match disabled",
			args:                  &config.ScheduleAdmissionArgs{EnablePrefixMatch: false},
			wantEnablePrefixMatch: false,
		},
		{
			name:                  "args with prefix match enabled",
			args:                  &config.ScheduleAdmissionArgs{EnablePrefixMatch: true},
			wantEnablePrefixMatch: true,
		},
		{
			name:    "wrong args type",
			args:    &config.SchedulingHintArgs{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, err := New(context.Background(), tt.args, nil)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, plugin)
			assert.Equal(t, Name, plugin.Name())
			assert.Equal(t, tt.wantEnablePrefixMatch, plugin.(*Plugin).enablePrefixMatch)
		})
	}
}

func TestPreEnqueue(t *testing.T) {
	tests := []struct {
		name              string
		enablePrefixMatch bool
		pod               *corev1.Pod
		wantStatus        fwktype.Code
	}{
		{
			name: "pod with no labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pod"},
			},
			wantStatus: fwktype.Success,
		},
		{
			name: "pod with unrelated labels",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-pod",
					Labels: map[string]string{"app": "test"},
				},
			},
			wantStatus: fwktype.Success,
		},
		{
			name: "exact-match mode: fixed label gates the pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-pod",
					Labels: map[string]string{extension.LabelScheduleAdmission: "true"},
				},
			},
			wantStatus: fwktype.UnschedulableAndUnresolvable,
		},
		{
			name: "exact-match mode: prefixed label does not gate the pod",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-pod",
					Labels: map[string]string{extension.LabelScheduleAdmissionPrefix + "quota-check": "true"},
				},
			},
			wantStatus: fwktype.Success,
		},
		{
			name:              "prefix mode: fixed label gates the pod",
			enablePrefixMatch: true,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-pod",
					Labels: map[string]string{extension.LabelScheduleAdmission: "true"},
				},
			},
			wantStatus: fwktype.UnschedulableAndUnresolvable,
		},
		{
			name:              "prefix mode: one prefixed label gates the pod",
			enablePrefixMatch: true,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-pod",
					Labels: map[string]string{extension.LabelScheduleAdmissionPrefix + "quota-check": "true"},
				},
			},
			wantStatus: fwktype.UnschedulableAndUnresolvable,
		},
		{
			name:              "prefix mode: multiple prefixed labels gate the pod",
			enablePrefixMatch: true,
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
			name:              "prefix mode: mixed labels including schedule-admission",
			enablePrefixMatch: true,
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
			pl := &Plugin{enablePrefixMatch: tt.enablePrefixMatch}
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
	logger := klog.Background()
	podUID := types.UID("test-uid")

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gated-pod",
			UID:  podUID,
		},
	}

	tests := []struct {
		name              string
		enablePrefixMatch bool
		oldObj            any
		newObj            any
		wantHint          fwktype.QueueingHint
		wantErr           bool
	}{
		{
			name:              "different pod updated",
			enablePrefixMatch: true,
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
			name: "exact-match mode: fixed label removed",
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "gated-pod",
					UID:    podUID,
					Labels: map[string]string{extension.LabelScheduleAdmission: "true"},
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
			name: "exact-match mode: prefixed label removed is ignored",
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "gated-pod",
					UID:    podUID,
					Labels: map[string]string{extension.LabelScheduleAdmissionPrefix + "quota-check": "true"},
				},
			},
			newObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
				},
			},
			wantHint: fwktype.QueueSkip,
		},
		{
			name:              "prefix mode: schedule-admission label removed",
			enablePrefixMatch: true,
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "gated-pod",
					UID:    podUID,
					Labels: map[string]string{extension.LabelScheduleAdmissionPrefix + "quota-check": "true"},
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
			name:              "prefix mode: unrelated label changed, schedule-admission unchanged",
			enablePrefixMatch: true,
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
			name:              "prefix mode: schedule-admission label added, not removed",
			enablePrefixMatch: true,
			oldObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gated-pod",
					UID:  podUID,
				},
			},
			newObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "gated-pod",
					UID:    podUID,
					Labels: map[string]string{extension.LabelScheduleAdmissionPrefix + "quota-check": "true"},
				},
			},
			wantHint: fwktype.QueueSkip,
		},
		{
			name:              "prefix mode: one of multiple gates removed, still gated",
			enablePrefixMatch: true,
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
					Name:   "gated-pod",
					UID:    podUID,
					Labels: map[string]string{extension.LabelScheduleAdmissionPrefix + "resource-ready": "true"},
				},
			},
			wantHint: fwktype.QueueSkip,
		},
		{
			name:              "prefix mode: all gates removed",
			enablePrefixMatch: true,
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
					Name:   "gated-pod",
					UID:    podUID,
					Labels: map[string]string{},
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
			pl := &Plugin{enablePrefixMatch: tt.enablePrefixMatch}
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
