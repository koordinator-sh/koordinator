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
	corev1helper "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	nominatedPodsOfTheSameJob = extension.SchedulingDomainPrefix + "/nominated-pods-to-ignore"
)

type NominatedPodsOfTheSameJob struct {
	UIDs sets.Set[string]
}

func (s *NominatedPodsOfTheSameJob) Clone() framework.StateData {
	return s
}

func MakeNominatedPodsOfTheSameJob(cycleState *framework.CycleState, uids []string) {
	cycleState.Write(nominatedPodsOfTheSameJob, &NominatedPodsOfTheSameJob{
		UIDs: sets.New[string](uids...),
	})
}

func getNominatedPodsOfTheSameJob(cycleState *framework.CycleState) sets.Set[string] {
	s, err := cycleState.Read(nominatedPodsOfTheSameJob)
	if err != nil || s == nil {
		return nil
	}
	return s.(*NominatedPodsOfTheSameJob).UIDs

}

func (ext *frameworkExtenderImpl) runFilterPluginsWithNominatedPodsIgnoreSameJob(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, info *framework.NodeInfo, podsOfSameJob sets.Set[string]) *framework.Status {
	var status *framework.Status

	podsAdded := false
	// We run filters twice in some cases. If the node has greater or equal priority
	// nominated pods, we run them when those pods are added to PreFilter state and nodeInfo.
	// If all filters succeed in this pass, we run them again when these
	// nominated pods are not added. This second pass is necessary because some
	// filters such as inter-pod affinity may not pass without the nominated pods.
	// If there are no nominated pods for the node or if the first run of the
	// filters fail, we don't run the second pass.
	// We consider only equal or higher priority pods in the first pass, because
	// those are the current "pod" must yield to them and not take a space opened
	// for running them. It is ok if the current "pod" take resources freed for
	// lower priority pods.
	// Requiring that the new pod is schedulable in both circumstances ensures that
	// we are making a conservative decision: filters like resources and inter-pod
	// anti-affinity are more likely to fail when the nominated pods are treated
	// as running, while filters like pod affinity are more likely to fail when
	// the nominated pods are treated as not running. We can't just assume the
	// nominated pods are running because they are not running right now and in fact,
	// they may end up getting scheduled to a different node.
	logger := klog.FromContext(ctx)
	logger = klog.LoggerWithName(logger, "FilterWithNominatedPods")
	ctx = klog.NewContext(ctx, logger)
	for i := 0; i < 2; i++ {
		stateToUse := state
		nodeInfoToUse := info
		var addedPods []string
		if i == 0 {
			var err error
			podsAdded, stateToUse, nodeInfoToUse, err, addedPods = addNominatedPods(ctx, ext, pod, state, info, podsOfSameJob)
			if err != nil {
				return framework.AsStatus(err)
			}
		} else if !podsAdded || !status.IsSuccess() {
			break
		}

		status = ext.RunFilterPlugins(ctx, stateToUse, pod, nodeInfoToUse)
		if !status.IsSuccess() && pod.Status.NominatedNodeName == nodeInfoToUse.Node().Name && klog.V(4).Enabled() {
			existingPods := make([]string, 0, len(nodeInfoToUse.Pods))
			for _, podInfo := range nodeInfoToUse.Pods {
				if len(podInfo.Pod.OwnerReferences) != 0 && podInfo.Pod.OwnerReferences[0].Kind == "DaemonSet" {
					// Normally, the daemonset on the node does not occupy resources, so when collecting the existing Pods, the daemonset Pods are ignored to reduce the number of logs.
					continue
				}
				existingPods = append(existingPods, framework.GetNamespacedName(podInfo.Pod.Namespace, podInfo.Pod.Name))
			}
			klog.V(4).Infof("Pod %s/%s is nominated to run on node %q, but failed to scheduling cause some existingPods: %+v, addedPods: %+v, status: %+v", pod.Namespace, pod.Name, nodeInfoToUse.Node().Name, existingPods, addedPods, status)
		}
		if !status.IsSuccess() && !status.IsUnschedulable() {
			return status
		}

	}

	return status
}

// addNominatedPods adds pods with equal or greater priority which are nominated
// to run on the node. It returns 1) whether any pod was added, 2) augmented cycleState,
// 3) augmented nodeInfo.
func addNominatedPods(ctx context.Context, fh framework.Handle, pod *corev1.Pod, state *framework.CycleState, nodeInfo *framework.NodeInfo, podsOfSameJob sets.Set[string]) (bool, *framework.CycleState, *framework.NodeInfo, error, []string) {
	if fh == nil {
		// This may happen only in tests.
		return false, state, nodeInfo, nil, nil
	}
	nominatedPodInfos := fh.NominatedPodsForNode(nodeInfo.Node().Name)
	if len(nominatedPodInfos) == 0 {
		return false, state, nodeInfo, nil, nil
	}
	nodeInfoOut := nodeInfo.Clone()
	stateOut := state.Clone()
	podsAdded := false
	var addedPods []string

	for _, pi := range nominatedPodInfos {
		if corev1helper.PodPriority(pi.Pod) >= corev1helper.PodPriority(pod) && pi.Pod.UID != pod.UID && !podsOfSameJob.Has(string(pi.Pod.UID)) {
			nodeInfoOut.AddPodInfo(pi)
			if klog.V(5).Enabled() || pod.Status.NominatedNodeName == nodeInfo.Node().Name {
				addedPods = append(addedPods, framework.GetNamespacedName(pi.Pod.Namespace, pi.Pod.Name))
			}
			status := fh.RunPreFilterExtensionAddPod(ctx, stateOut, pod, pi, nodeInfoOut)
			if !status.IsSuccess() {
				return false, state, nodeInfo, status.AsError(), nil
			}
			podsAdded = true
		}
	}
	if len(addedPods) > 0 {
		klog.V(5).Infof("Added %v pods with equal or higher priority to the node %q when schedule pod %s/%s", addedPods, nodeInfo.Node().Name, pod.Namespace, pod.Name)
	}
	return podsAdded, stateOut, nodeInfoOut, nil, addedPods
}
