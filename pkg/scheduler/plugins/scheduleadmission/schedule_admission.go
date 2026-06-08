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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	Name = "ScheduleAdmission"
)

var (
	_ fwktype.PreEnqueuePlugin = &Plugin{}
	_ fwktype.EnqueueExtensions = &Plugin{}
)

type Plugin struct{}

func New(_ context.Context, _ runtime.Object, _ fwktype.Handle) (fwktype.Plugin, error) {
	return &Plugin{}, nil
}

func (pl *Plugin) Name() string {
	return Name
}

func (pl *Plugin) PreEnqueue(ctx context.Context, pod *corev1.Pod) *fwktype.Status {
	if extension.HasScheduleAdmissionLabels(pod) {
		gates := extension.GetScheduleAdmissionGates(pod)
		return fwktype.NewStatus(fwktype.UnschedulableAndUnresolvable,
			fmt.Sprintf("pod has schedule-admission gates: %v", gates))
	}
	return fwktype.NewStatus(fwktype.Success, "")
}

func (pl *Plugin) EventsToRegister(_ context.Context) ([]fwktype.ClusterEventWithHint, error) {
	return []fwktype.ClusterEventWithHint{
		{
			Event: fwktype.ClusterEvent{
				Resource:   fwktype.Pod,
				ActionType: fwktype.UpdatePodLabel,
			},
			QueueingHintFn: pl.isScheduleAdmissionLabelRemoved,
		},
	}, nil
}

func (pl *Plugin) isScheduleAdmissionLabelRemoved(logger klog.Logger, pod *corev1.Pod, oldObj, newObj any) (fwktype.QueueingHint, error) {
	oldPod, newPod, err := schedutil.As[*corev1.Pod](oldObj, newObj)
	if err != nil {
		return fwktype.Queue, err
	}

	// Only care about label changes on the pod itself.
	if pod.UID != newPod.UID {
		return fwktype.QueueSkip, nil
	}

	// Check if any schedule-admission label was removed.
	for key := range oldPod.Labels {
		if strings.HasPrefix(key, extension.LabelScheduleAdmissionPrefix) {
			if _, exists := newPod.Labels[key]; !exists {
				logger.V(5).Info("Schedule-admission label removed, re-enqueuing pod",
					"pod", klog.KObj(pod), "removedLabel", key)
				return fwktype.Queue, nil
			}
		}
	}
	return fwktype.QueueSkip, nil
}
