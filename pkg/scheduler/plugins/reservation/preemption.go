/*
Copyright 2022 The Koordinator Authors.
Copyright 2020 The Kubernetes Authors.

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
	"sort"

	corev1 "k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	corelisters "k8s.io/client-go/listers/core/v1"
	policylisters "k8s.io/client-go/listers/policy/v1"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/features"
	schedulerconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultpreemption"
	plfeature "k8s.io/kubernetes/pkg/scheduler/framework/plugins/feature"
	"k8s.io/kubernetes/pkg/scheduler/framework/preemption"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	listerschedulingv1alpha1 "github.com/koordinator-sh/koordinator/pkg/client/listers/scheduling/v1alpha1"
	koordfeature "github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
)

var (
	_ framework.PostFilterPlugin = &PreemptionMgr{}
	_ preemption.Interface       = &PreemptionMgr{}
)

// PreemptionMgr is a wrapper of defaultpreemption.DefaultPreemption, supporting the following preemption behaviors:
// 1. a pod preempt a pod.
// 2. a reservation preempt a pod.
// TODO: support the reservation being preempted
type PreemptionMgr struct {
	*defaultpreemption.DefaultPreemption
	fh                frameworkext.ExtendedHandle
	podLister         corelisters.PodLister
	pdbLister         policylisters.PodDisruptionBudgetLister
	reservationLister listerschedulingv1alpha1.ReservationLister
}

func newPreemptionMgr(pluginArgs *config.ReservationArgs, extendedHandle frameworkext.ExtendedHandle,
	podLister corelisters.PodLister, rLister listerschedulingv1alpha1.ReservationLister) (*PreemptionMgr, error) {
	preemptionArgs, err := getPreemptionArgs(pluginArgs)
	if err != nil {
		return nil, err
	}

	fts := plfeature.Features{
		EnablePodDisruptionConditions: k8sfeature.DefaultFeatureGate.Enabled(features.PodDisruptionConditions),
	}

	pl, err := defaultpreemption.New(preemptionArgs, extendedHandle, fts)
	if err != nil {
		return nil, err
	}

	preemptionPl := pl.(*defaultpreemption.DefaultPreemption)
	var pdbLister policylisters.PodDisruptionBudgetLister
	if k8sfeature.DefaultFeatureGate.Enabled(koordfeature.PodDisruptionBudget) {
		pdbLister = extendedHandle.SharedInformerFactory().Policy().V1().PodDisruptionBudgets().Lister()
	}

	return &PreemptionMgr{
		DefaultPreemption: preemptionPl,
		fh:                extendedHandle,
		podLister:         podLister,
		pdbLister:         pdbLister,
		reservationLister: rLister,
	}, nil
}

func (pm *PreemptionMgr) Name() string {
	return Name
}

func (pm *PreemptionMgr) PostFilter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, m framework.NodeToStatusMap) (*framework.PostFilterResult, *framework.Status) {
	defer func() {
		metrics.PreemptionAttempts.Inc()
	}()

	pe := preemption.Evaluator{
		PluginName: Name,
		Handler:    pm.fh,
		PodLister:  newDelegatingPodLister(pm.podLister, pm.reservationLister, pod),
		PdbLister:  pm.pdbLister,
		State:      state,
		Interface:  pm,
	}
	klog.V(4).InfoS("Attempt to do reservation preemption in the PostFilter", "pod", klog.KObj(pod))

	result, status := pe.Preempt(ctx, pod, m)
	if status.Message() != "" {
		return result, framework.NewStatus(status.Code(), "preemption: "+status.Message())
	}
	return result, status
}

// SelectVictimsOnNode finds minimum set of pods on the given node that should be preempted in order to make enough room
// for "pod" to be scheduled.
// Note that both `state` and `nodeInfo` are deep-copied.
// We delegate the function to extend the preemption rules:
// If a pod is marked as non-preemptible, it will not be selected as the victim.
func (pm *PreemptionMgr) SelectVictimsOnNode(
	ctx context.Context,
	state *framework.CycleState,
	pod *corev1.Pod,
	nodeInfo *framework.NodeInfo,
	pdbs []*policy.PodDisruptionBudget) ([]*corev1.Pod, int, *framework.Status) {
	logger := klog.FromContext(ctx)
	var potentialVictims []*framework.PodInfo
	removePod := func(rpi *framework.PodInfo) error {
		if err := nodeInfo.RemovePod(rpi.Pod); err != nil {
			return err
		}
		status := pm.fh.RunPreFilterExtensionRemovePod(ctx, state, pod, rpi, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}
	addPod := func(api *framework.PodInfo) error {
		nodeInfo.AddPodInfo(api)
		status := pm.fh.RunPreFilterExtensionAddPod(ctx, state, pod, api, nodeInfo)
		if !status.IsSuccess() {
			return status.AsError()
		}
		return nil
	}
	// As the first step, remove all the lower priority pods from the node and
	// check if the given pod can be scheduled.
	podPriority := corev1helpers.PodPriority(pod)
	for _, pi := range nodeInfo.Pods {
		// NOTE: Ignore the non-preemptible pod.
		if !extension.IsPodPreemptible(pi.Pod) ||
			corev1helpers.PodPriority(pi.Pod) >= podPriority {
			continue
		}

		potentialVictims = append(potentialVictims, pi)
		if err := removePod(pi); err != nil {
			return nil, 0, framework.AsStatus(err)
		}
	}

	// No potential victims are found, and so we don't need to evaluate the node again since its state didn't change.
	if len(potentialVictims) == 0 {
		message := fmt.Sprintf("No preemption victims found for incoming pod")
		return nil, 0, framework.NewStatus(framework.UnschedulableAndUnresolvable, message)
	}

	// If the new pod does not fit after removing all the lower priority pods,
	// we are almost done and this node is not suitable for preemption. The only
	// condition that we could check is if the "pod" is failing to schedule due to
	// inter-pod affinity to one or more victims, but we have decided not to
	// support this case for performance reasons. Having affinity to lower
	// priority pods is not a recommended configuration anyway.
	if status := pm.fh.RunFilterPluginsWithNominatedPods(ctx, state, pod, nodeInfo); !status.IsSuccess() {
		return nil, 0, status
	}
	var victims []*corev1.Pod
	numViolatingVictim := 0
	sort.Slice(potentialVictims, func(i, j int) bool { return util.MoreImportantPod(potentialVictims[i].Pod, potentialVictims[j].Pod) })
	// Try to reprieve as many pods as possible. We first try to reprieve the PDB
	// violating victims and then other non-violating ones. In both cases, we start
	// from the highest priority victims.
	violatingVictims, nonViolatingVictims := filterPodsWithPDBViolation(potentialVictims, pdbs)
	reprievePod := func(pi *framework.PodInfo) (bool, error) {
		if err := addPod(pi); err != nil {
			return false, err
		}
		status := pm.fh.RunFilterPluginsWithNominatedPods(ctx, state, pod, nodeInfo)
		fits := status.IsSuccess()
		if !fits {
			if err := removePod(pi); err != nil {
				return false, err
			}
			rpi := pi.Pod
			victims = append(victims, rpi)
			logger.V(5).Info("Pod is a potential preemption victim on node", "pod", klog.KObj(rpi), "node", klog.KObj(nodeInfo.Node()))
		}
		return fits, nil
	}
	for _, p := range violatingVictims {
		if fits, err := reprievePod(p); err != nil {
			return nil, 0, framework.AsStatus(err)
		} else if !fits {
			numViolatingVictim++
		}
	}
	// Now we try to reprieve non-violating victims.
	for _, p := range nonViolatingVictims {
		if _, err := reprievePod(p); err != nil {
			return nil, 0, framework.AsStatus(err)
		}
	}
	return victims, numViolatingVictim, framework.NewStatus(framework.Success)
}

var _ corelisters.PodLister = &delegatingPodLister{}

// delegatingPodLister delegates the PodLister interface to get a Pod or a Reservation from the reserve pod.
type delegatingPodLister struct {
	corelisters.PodLister
	reservationLister listerschedulingv1alpha1.ReservationLister
	cachedPod         *corev1.Pod
}

func newDelegatingPodLister(podLister corelisters.PodLister,
	reservationLister listerschedulingv1alpha1.ReservationLister,
	pod *corev1.Pod) corelisters.PodLister {
	if !reservationutil.IsReservePod(pod) {
		return podLister
	}

	return &delegatingPodLister{
		PodLister:         podLister,
		reservationLister: reservationLister,
		cachedPod:         pod,
	}
}

func (dp *delegatingPodLister) Pods(namespace string) corelisters.PodNamespaceLister {
	// only delegate the default namespace since reserve pod is forced to the namespace
	if namespace != "" && namespace != corev1.NamespaceDefault ||
		dp.cachedPod == nil || namespace != dp.cachedPod.Namespace {
		return dp.PodLister.Pods(namespace)
	}

	return &delegatingPodNamespaceLister{
		PodNamespaceLister: dp.PodLister.Pods(namespace),
		reservationLister:  dp.reservationLister,
		cachedPod:          dp.cachedPod,
	}
}

var _ corelisters.PodNamespaceLister = &delegatingPodNamespaceLister{}

type delegatingPodNamespaceLister struct {
	corelisters.PodNamespaceLister
	reservationLister listerschedulingv1alpha1.ReservationLister
	cachedPod         *corev1.Pod
}

func (dpn *delegatingPodNamespaceLister) Get(name string) (*corev1.Pod, error) {
	pod, err := dpn.PodNamespaceLister.Get(name)
	if err == nil {
		return pod, nil
	}
	if !errors.IsNotFound(err) {
		return pod, err
	}

	// if the cached pod not found from the informer, try to get a corresponding reservation
	if dpn.cachedPod == nil || dpn.cachedPod.Name != name {
		return pod, err
	}
	reservationName := reservationutil.GetReservationNameFromReservePod(dpn.cachedPod)
	if len(reservationName) <= 0 {
		klog.ErrorS(fmt.Errorf("missing a reservationName"), "Failed to get reservation for cachedPod",
			"pod", name, "cachedPod", klog.KObj(dpn.cachedPod))
		return pod, err
	}

	reservation, err1 := dpn.reservationLister.Get(reservationName)
	if err1 != nil {
		return pod, utilerrors.NewAggregate([]error{err, err1})
	}
	// Is it necessary to regenerate the reserve pod?
	reservePod := reservationutil.NewReservePod(reservation)
	return reservePod, nil
}

// filterPodsWithPDBViolation groups the given "pods" into two groups of "violatingPods"
// and "nonViolatingPods" based on whether their PDBs will be violated if they are
// preempted.
// This function is stable and does not change the order of received pods. So, if it
// receives a sorted list, grouping will preserve the order of the input list.
func filterPodsWithPDBViolation(podInfos []*framework.PodInfo, pdbs []*policy.PodDisruptionBudget) (violatingPodInfos, nonViolatingPodInfos []*framework.PodInfo) {
	pdbsAllowed := make([]int32, len(pdbs))
	for i, pdb := range pdbs {
		pdbsAllowed[i] = pdb.Status.DisruptionsAllowed
	}

	for _, podInfo := range podInfos {
		pod := podInfo.Pod
		pdbForPodIsViolated := false
		// A pod with no labels will not match any PDB. So, no need to check.
		if len(pod.Labels) != 0 {
			for i, pdb := range pdbs {
				if pdb.Namespace != pod.Namespace {
					continue
				}
				selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
				if err != nil {
					// This object has an invalid selector, it does not match the pod
					continue
				}
				// A PDB with a nil or empty selector matches nothing.
				if selector.Empty() || !selector.Matches(labels.Set(pod.Labels)) {
					continue
				}

				// Existing in DisruptedPods means it has been processed in API server,
				// we don't treat it as a violating case.
				if _, exist := pdb.Status.DisruptedPods[pod.Name]; exist {
					continue
				}
				// Only decrement the matched pdb when it's not in its <DisruptedPods>;
				// otherwise we may over-decrement the budget number.
				pdbsAllowed[i]--
				// We have found a matching PDB.
				if pdbsAllowed[i] < 0 {
					pdbForPodIsViolated = true
				}
			}
		}
		if pdbForPodIsViolated {
			violatingPodInfos = append(violatingPodInfos, podInfo)
		} else {
			nonViolatingPodInfos = append(nonViolatingPodInfos, podInfo)
		}
	}
	return violatingPodInfos, nonViolatingPodInfos
}

func getPreemptionArgs(pluginArgs *config.ReservationArgs) (*schedulerconfig.DefaultPreemptionArgs, error) {
	preemptionArgs := &schedulerconfig.DefaultPreemptionArgs{
		MinCandidateNodesPercentage: pluginArgs.MinCandidateNodesPercentage,
		MinCandidateNodesAbsolute:   pluginArgs.MinCandidateNodesAbsolute,
	}
	return preemptionArgs, nil
}
