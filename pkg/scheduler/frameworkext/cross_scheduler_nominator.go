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
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// CrossSchedulerPodNominator tracks nominated pods from other schedulers
// to enable cross-scheduler resource accounting during filter phase.
type CrossSchedulerPodNominator struct {
	mu sync.RWMutex
	// nominatedPods indexes nominated pods by node name
	nominatedPods map[string][]fwktype.PodInfo
	// nominatedPodToNode maps pod UID to nominated node name for quick lookup
	nominatedPodToNode map[types.UID]string
	// localProfileNames contains the scheduler profile names of the current scheduler instance.
	// Pods from these profiles are excluded (they're handled by the native PodNominator).
	localProfileNames sets.Set[string]
}

// NewCrossSchedulerPodNominator creates a new CrossSchedulerPodNominator with an empty local profile set.
// Profile names are registered lazily via AddLocalProfileName after each framework profile is built.
func NewCrossSchedulerPodNominator() *CrossSchedulerPodNominator {
	return &CrossSchedulerPodNominator{
		nominatedPods:      make(map[string][]fwktype.PodInfo),
		nominatedPodToNode: make(map[types.UID]string),
		localProfileNames:  sets.New[string](),
	}
}

// AddLocalProfileName adds a profile name to the local profiles set.
// Pods from local profiles are excluded from cross-scheduler nomination tracking
// because they are handled by the native PodNominator.
// This method is called when a new framework profile is created.
func (n *CrossSchedulerPodNominator) AddLocalProfileName(profileName string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.localProfileNames.Insert(profileName)
}

// NominatedPodsForNode returns the cross-scheduler nominated pods for the given node.
// The caller should not modify the returned slice.
func (n *CrossSchedulerPodNominator) NominatedPodsForNode(nodeName string) []fwktype.PodInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()
	// Make a copy of the nominated Pods so the caller can mutate safely.
	pods := make([]fwktype.PodInfo, 0, len(n.nominatedPods[nodeName]))
	for i := range n.nominatedPods[nodeName] {
		podInfoCopy, err := framework.NewPodInfo(n.nominatedPods[nodeName][i].GetPod().DeepCopy())
		if err == nil {
			pods = append(pods, podInfoCopy)
		}
	}
	return pods
}

// addNominatedPod adds or updates the nominated pod record.
// It must be called with the write lock held (or in a context where no lock is needed).
func (n *CrossSchedulerPodNominator) addNominatedPod(pod *corev1.Pod) {
	if pod.Status.NominatedNodeName == "" {
		return
	}
	// Remove the old entry first to handle updates (node name may have changed).
	n.deleteNominatedPod(pod)

	nodeName := pod.Status.NominatedNodeName
	podInfo, err := framework.NewPodInfo(pod)
	if err != nil {
		klog.ErrorS(err, "Failed to create PodInfo for cross-scheduler nominated pod", "pod", klog.KObj(pod))
		return
	}
	n.nominatedPods[nodeName] = append(n.nominatedPods[nodeName], podInfo)
	n.nominatedPodToNode[pod.UID] = nodeName
}

// deleteNominatedPod removes the nominated pod record. Must be called with write lock held.
func (n *CrossSchedulerPodNominator) deleteNominatedPod(pod *corev1.Pod) {
	nodeName, ok := n.nominatedPodToNode[pod.UID]
	if !ok {
		return
	}
	pods := n.nominatedPods[nodeName]
	for i, pi := range pods {
		if pi.GetPod().UID == pod.UID {
			n.nominatedPods[nodeName] = append(pods[:i], pods[i+1:]...)
			break
		}
	}
	if len(n.nominatedPods[nodeName]) == 0 {
		delete(n.nominatedPods, nodeName)
	}
	delete(n.nominatedPodToNode, pod.UID)
}

func (n *CrossSchedulerPodNominator) ShouldHandle(obj interface{}) bool {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		// Handle tombstone objects from the informer cache.
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return false
		}
		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			return false
		}
	}
	if pod.Status.NominatedNodeName == "" || pod.Spec.NodeName != "" {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return !n.localProfileNames.Has(pod.Spec.SchedulerName)
}

func (n *CrossSchedulerPodNominator) OnAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addNominatedPod(pod)
}

func (n *CrossSchedulerPodNominator) OnUpdate(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		return
	}
	newPod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	// We update irrespective of the nominatedNodeName changed or not, to ensure that pod pointer is updated.
	n.deleteNominatedPod(oldPod)
	n.addNominatedPod(newPod)
}

func (n *CrossSchedulerPodNominator) OnDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		// Handle tombstone objects from the informer cache.
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	n.deleteNominatedPod(pod)
}
