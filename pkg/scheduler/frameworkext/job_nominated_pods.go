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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	corev1helper "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
)

const (
	nominatedPodsOfTheSameJob = extension.SchedulingDomainPrefix + "/nominated-pods-to-ignore"
)

type NominatedPodsOfTheSameJob struct {
	UIDs sets.Set[string]
}

func (s *NominatedPodsOfTheSameJob) Clone() fwktype.StateData {
	return s
}

func MakeNominatedPodsOfTheSameJob(cycleState fwktype.CycleState, uids []string) {
	cycleState.Write(nominatedPodsOfTheSameJob, &NominatedPodsOfTheSameJob{
		UIDs: sets.New[string](uids...),
	})
}

func getNominatedPodsOfTheSameJob(cycleState fwktype.CycleState) sets.Set[string] {
	s, err := cycleState.Read(nominatedPodsOfTheSameJob)
	if err != nil || s == nil {
		return nil
	}
	return s.(*NominatedPodsOfTheSameJob).UIDs
}

// addNominatedPods adds pods with equal or greater priority which are nominated
// to run on the node. It returns 1) whether any pod was added, 2) augmented cycleState,
// 3) augmented nodeInfo.
func addNominatedPods(ctx context.Context, fh fwktype.Handle, pod *corev1.Pod, state fwktype.CycleState, nodeInfo fwktype.NodeInfo, podsOfSameJob sets.Set[string]) (bool, fwktype.CycleState, fwktype.NodeInfo, error, []string) {
	if fh == nil {
		// This may happen only in tests.
		return false, state, nodeInfo, nil, nil
	}
	nominatedPodInfos := fh.NominatedPodsForNode(nodeInfo.Node().Name)
	if len(nominatedPodInfos) == 0 {
		return false, state, nodeInfo, nil, nil
	}

	nodeInfoOut := nodeInfo.Snapshot()
	stateOut := state.Clone()
	podsAdded := false
	var addedPods []string

	for _, pi := range nominatedPodInfos {
		piPod := pi.GetPod()
		if corev1helper.PodPriority(piPod) >= corev1helper.PodPriority(pod) &&
			piPod.UID != pod.UID &&
			(podsOfSameJob == nil || !podsOfSameJob.Has(string(piPod.UID))) {
			nodeInfoOut.AddPodInfo(pi)
			if klog.V(5).Enabled() || pod.Status.NominatedNodeName == nodeInfo.Node().Name {
				addedPods = append(addedPods, framework.GetNamespacedName(piPod.Namespace, piPod.Name))
			}
			status := fh.RunPreFilterExtensionAddPod(ctx, stateOut, pod, pi, nodeInfoOut)
			if !status.IsSuccess() {
				return false, state, nodeInfo, status.AsError(), nil
			}
			podsAdded = true
		}
	}

	if podsAdded {
		klog.V(5).Infof("Added %v pods with equal or higher priority to the node %q when schedule pod %s/%s", addedPods, nodeInfo.Node().Name, pod.Namespace, pod.Name)
		return true, stateOut, nodeInfoOut, nil, addedPods
	}

	return false, state, nodeInfo, nil, nil
}

// addMergedNominatedPods adds both native and cross-scheduler nominated pods to a cloned nodeInfo.
// For native nominated pods: uses priority >= (consistent with k8s native behavior).
// For cross-scheduler nominated pods: uses priority > (strictly greater than, to avoid same-priority deadlock).
func addMergedNominatedPods(
	ctx context.Context,
	ext *frameworkExtenderImpl,
	pod *corev1.Pod,
	state fwktype.CycleState,
	nodeInfo fwktype.NodeInfo,
	podsOfSameJob sets.Set[string],
) (bool, fwktype.CycleState, fwktype.NodeInfo, error) {
	nodeName := nodeInfo.Node().Name
	podPriority := corev1helper.PodPriority(pod)

	// Get native nominated pods via the embedded framework handle.
	nativeNominated := ext.Framework.NominatedPodsForNode(nodeName)
	// Get cross-scheduler nominated pods.
	var crossNominated []fwktype.PodInfo
	if ext.crossSchedulerNominator != nil {
		crossNominated = ext.crossSchedulerNominator.NominatedPodsForNode(nodeName)
	}

	if len(nativeNominated) == 0 && len(crossNominated) == 0 {
		return false, state, nodeInfo, nil
	}

	nodeInfoOut := nodeInfo.Snapshot()
	stateOut := state.Clone()
	podsAdded := false

	// Add responsible nominated pods not in the same job (priority >= current pod, consistent with k8s native behavior).
	for _, pi := range nativeNominated {
		piPod := pi.GetPod()
		if corev1helper.PodPriority(piPod) >= podPriority &&
			piPod.UID != pod.UID &&
			(podsOfSameJob == nil || !podsOfSameJob.Has(string(piPod.UID))) {
			nodeInfoOut.AddPodInfo(pi)
			status := ext.RunPreFilterExtensionAddPod(ctx, stateOut, pod, pi, nodeInfoOut)
			if !status.IsSuccess() {
				return false, state, nodeInfo, status.AsError()
			}
			podsAdded = true
		}
	}

	// Add cross-scheduler nominated pods not in the same job (priority > current pod, strictly greater to avoid same-priority deadlock).
	for _, pi := range crossNominated {
		piPod := pi.GetPod()
		if corev1helper.PodPriority(piPod) > podPriority &&
			piPod.UID != pod.UID &&
			(podsOfSameJob == nil || !podsOfSameJob.Has(string(piPod.UID))) {
			nodeInfoOut.AddPodInfo(pi)
			status := ext.RunPreFilterExtensionAddPod(ctx, stateOut, pod, pi, nodeInfoOut)
			if !status.IsSuccess() {
				return false, state, nodeInfo, status.AsError()
			}
			podsAdded = true
		}
	}

	if podsAdded {
		klog.V(5).Infof("Added %v responsible and cross-scheduler nominated pods to the node %q when schedule pod %s/%s", podsAdded, nodeName, pod.Namespace, pod.Name)
		return true, stateOut, nodeInfoOut, nil
	}

	return false, state, nodeInfo, nil
}

// runFilterPluginsWithNominatedPods is the unified implementation for running filter plugins
// with nominated pods. It handles all feature gate combinations:
//   - CrossSchedulerNomination: controls whether cross-scheduler nominated pods are included.
//     When enabled, uses addMergedNominatedPods (native >= + cross-scheduler >).
//     When disabled, uses addNominatedPods (native >= only).
//   - SkipFilterWithNominatedPods: controls the number of filter passes.
//     When enabled, runs a single pass with nominated pods (skipping the second pass without
//     nominated pods that handles the pod affinity corner case).
//     When disabled, runs two passes (with and without nominated pods) for conservative scheduling.
//
// It also excludes same-job nominated pods and includes NominatedNodeName diagnostic logging,
// compatible with the previous runFilterPluginsWithNominatedPodsIgnoreSameJob behavior.
func (ext *frameworkExtenderImpl) runFilterPluginsWithNominatedPods(
	ctx context.Context,
	state fwktype.CycleState,
	pod *corev1.Pod,
	info fwktype.NodeInfo,
) *fwktype.Status {
	podsOfSameJob := getNominatedPodsOfTheSameJob(state)
	singlePass := k8sfeature.DefaultFeatureGate.Enabled(features.SkipFilterWithNominatedPods)

	var status *fwktype.Status
	podsAdded := false

	for i := 0; i < 2; i++ {
		stateToUse := state
		nodeInfoToUse := info

		if i == 0 {
			// Pass 1: add nominated pods to nodeInfo overlay.
			var err error
			if k8sfeature.DefaultFeatureGate.Enabled(features.CrossSchedulerNomination) && ext.crossSchedulerNominator != nil {
				podsAdded, stateToUse, nodeInfoToUse, err = addMergedNominatedPods(ctx, ext, pod, state, info, podsOfSameJob)
			} else {
				podsAdded, stateToUse, nodeInfoToUse, err, _ = addNominatedPods(ctx, ext, pod, state, info, podsOfSameJob)
			}
			if err != nil {
				return fwktype.AsStatus(err)
			}
			if !podsAdded {
				stateToUse = state
				nodeInfoToUse = info
			}
		} else if !podsAdded || !status.IsSuccess() {
			// Pass 2: only needed if pass 1 added pods AND pass 1 succeeded.
			break
		}

		status = ext.RunFilterPlugins(ctx, stateToUse, pod, nodeInfoToUse)
		if !status.IsSuccess() {
			if debugFilterFailure {
				klog.Infof("Failed to filter for Pod %q on Node %q (pass %d), failedPlugin: %s, reason: %s",
					klog.KObj(pod), klog.KObj(info.Node()), i, status.Plugin(), status.Message())
			}
			// NominatedNodeName diagnostic logging.
			if pod.Status.NominatedNodeName == info.Node().Name && klog.V(4).Enabled() {
				existingPods := make([]string, 0, len(nodeInfoToUse.GetPods()))
				for _, podInfo := range nodeInfoToUse.GetPods() {
					piPod := podInfo.GetPod()
					if len(piPod.OwnerReferences) != 0 && piPod.OwnerReferences[0].Kind == "DaemonSet" {
						continue
					}
					existingPods = append(existingPods, framework.GetNamespacedName(piPod.Namespace, piPod.Name))
				}
				klog.V(4).Infof("Pod %s/%s is nominated to run on node %q, but failed to scheduling (pass %d), existingPods: %+v, status: %+v",
					pod.Namespace, pod.Name, info.Node().Name, i, existingPods, status)
			}
		}
		if !status.IsSuccess() && !status.IsRejected() {
			return status
		}

		// In single-pass mode, skip the second pass.
		if singlePass {
			break
		}
	}

	return status
}
