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
	"sync"

	corev1 "k8s.io/api/core/v1"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

type Nominator struct {
	sync.Mutex
	nominateMap map[string]map[schedulingv1alpha1.DeviceType]deviceResources
}

func NewNominator() *Nominator {
	return &Nominator{
		nominateMap: make(map[string]map[schedulingv1alpha1.DeviceType]deviceResources),
	}
}

func (nominator *Nominator) AddPod(pod *corev1.Pod, used map[schedulingv1alpha1.DeviceType]deviceResources) {
	nominator.Lock()
	defer nominator.Unlock()

	podNamespacedName := pod.Namespace + "/" + pod.Name
	nominator.nominateMap[podNamespacedName] = used
}

func (nominator *Nominator) RemovePod(pod *corev1.Pod) {
	nominator.Lock()
	defer nominator.Unlock()

	podNamespacedName := pod.Namespace + "/" + pod.Name
	delete(nominator.nominateMap, podNamespacedName)
}

func (nominator *Nominator) GetPodAllocated(pod *corev1.Pod) map[schedulingv1alpha1.DeviceType]deviceResources {
	nominator.Lock()
	defer nominator.Unlock()

	podNamespacedName := pod.Namespace + "/" + pod.Name
	return nominator.nominateMap[podNamespacedName]
}

func (nominator *Nominator) IsPodExist(pod *corev1.Pod) bool {
	nominator.Lock()
	defer nominator.Unlock()

	podNamespacedName := pod.Namespace + "/" + pod.Name
	_, exist := nominator.nominateMap[podNamespacedName]
	return exist
}
