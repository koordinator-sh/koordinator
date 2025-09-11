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
	"context"
	"errors"
	"time"

	"k8s.io/kubernetes/pkg/scheduler/framework"
)

var (
	// JobPreemptionRejectPlugin If the waitingPod is rejected by the Job preemption plug-in, the assumedNodeName is the nominatedNodeName.
	JobPreemptionRejectPlugin = "JobPreemptionRejectPlugin"
	// JobRejectPlugin If the waitingPod is only recycled by the JobRejectPlugin, the NominatedNodeName should not be modified.
	JobRejectPlugin = "JobRejectPlugin"
)

func TakeoverNominatingInfo(
	ctx context.Context,
	fwk framework.Framework,
	podInfo *framework.QueuedPodInfo,
	status *framework.Status,
	nominatingInfo *framework.NominatingInfo,
	start time.Time) bool {
	if status == nil || status.Code() != framework.Unschedulable {
		return false
	}
	rejecterPlugin := getRejecterPlugin(status)
	if rejecterPlugin == JobPreemptionRejectPlugin {
		nominatingInfo.NominatingMode = framework.ModeOverride
		nominatingInfo.NominatedNodeName = podInfo.Pod.Spec.NodeName
	} else if status.FailedPlugin() == JobRejectPlugin {
		nominatingInfo.NominatingMode = framework.ModeNoop
		nominatingInfo.NominatedNodeName = ""
	}
	return false
}

func getRejecterPlugin(status *framework.Status) string {
	if status == nil || status.Code() != framework.Unschedulable {
		return ""
	}
	err := status.AsError()
	var fitError *framework.FitError
	if errors.As(err, &fitError) && fitError.Diagnosis.UnschedulablePlugins.Len() == 1 {
		// 1.33 framework.NewStatus(framework.Unschedulable, msg).WithPlugin(pluginName):
		//		fitErr := &framework.FitError{
		//			NumAllNodes: 1,
		//			Pod:         assumedPodInfo.Pod,
		//			Diagnosis: framework.Diagnosis{
		//				NodeToStatus:         framework.NewDefaultNodeToStatus(),
		//				UnschedulablePlugins: sets.New(status.Plugin()),
		//			},
		//		}
		//		framework.NewStatus(status.Code()).WithError(fitErr)
		return fitError.Diagnosis.UnschedulablePlugins.UnsortedList()[0]
	}
	// 1.28 framework.NewStatus(framework.Unschedulable, msg).WithFailedPlugin(pluginName):
	return status.FailedPlugin()
}
