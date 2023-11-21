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

package helpers

import (
	corev1 "k8s.io/api/core/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

// CalculateFilterPodsUsed calculates the sum used of filtered pods and hostApps.
// It returns the sum used of the filtered pods, the sum used of the filtered hostApps and the system used.
// If hostApps not passed, the hostApps part will be counted into the system used.
//
// e.g. To calculate non-BE used,
//   - filterPodsUsed := sum(podUsed[i] if f_pod(pod[i]))
//   - filterHostAppUsed := sum(hostAppUsed[i] if f_hostApp(hostApp[i]))
//   - systemdUsed := max(nodeReserved, nodeUsed - sum(podUsed[i]) - sum(hostAppUsed[i]))
func CalculateFilterPodsUsed(nodeUsed float64, nodeReserved float64,
	podMetas []*statesinformer.PodMeta, podUsedMap map[string]float64,
	hostApps []slov1alpha1.HostApplicationSpec, hostAppMetrics map[string]float64,
	podFilterFn func(*corev1.Pod) bool,
	hostAppFilterFn func(*slov1alpha1.HostApplicationSpec) bool) (float64, float64, float64) {
	var podsAllUsed, podsFilterUsed, hostAppsAllUsed, hostAppsFilterUsed float64

	podMetaMap := map[string]*statesinformer.PodMeta{}
	for _, podMeta := range podMetas {
		podMetaMap[string(podMeta.Pod.UID)] = podMeta
	}

	for podUID, podUsed := range podUsedMap {
		podsAllUsed += podUsed

		podMeta, ok := podMetaMap[podUID]
		if !ok { // NOTE: consider podMeta-missing pods as filtered
			klog.V(4).Infof("pod metric not included in the podMetas, uid %v", podUID)
			podsFilterUsed += podUsed
		} else if podFilterFn(podMeta.Pod) {
			podsFilterUsed += podUsed
		}
	}

	for _, hostApp := range hostApps {
		hostAppUsed, exist := hostAppMetrics[hostApp.Name]
		if !exist {
			klog.V(4).Infof("host app metric not included in the hostAppMetrics, name %v", hostApp.Name)
			continue
		}

		hostAppsAllUsed += hostAppUsed
		if hostAppFilterFn(&hostApp) {
			hostAppsFilterUsed += hostAppUsed
		}
	}

	// systemUsed means the remain used excluding the filtered pods and hostApps
	systemUsed := nodeUsed - podsAllUsed - hostAppsAllUsed
	if systemUsed < 0 { // set systemUsed always no less than 0
		systemUsed = 0
	}
	// systemUsed = max(nodeUsed - podsAllUsed - hostAppsAllUsed, nodeAnnoReserved, nodeKubeletReserved)
	if systemUsed < nodeReserved {
		systemUsed = nodeReserved
	}

	return podsFilterUsed, hostAppsFilterUsed, systemUsed
}

func NotBatchOrFreePodFilter(pod *corev1.Pod) bool {
	priority := apiext.GetPodPriorityClassWithDefault(pod)
	return priority != apiext.PriorityBatch && priority != apiext.PriorityFree
}

func NonBEPodFilter(pod *corev1.Pod) bool {
	return apiext.GetPodQoSClassRaw(pod) != apiext.QoSBE && util.GetKubeQosClass(pod) != corev1.PodQOSBestEffort
}

func NonBEHostAppFilter(hostAppSpec *slov1alpha1.HostApplicationSpec) bool {
	// host app qos must be BE and also run under best-effort dir
	return hostAppSpec.QoS != apiext.QoSBE || hostAppSpec.CgroupPath == nil || hostAppSpec.CgroupPath.Base != slov1alpha1.CgroupBaseTypeKubeBesteffort
}

func NonePodHighPriority(_ *corev1.Pod) bool {
	return false
}

func NoneHostAppHighPriority(_ *slov1alpha1.HostApplicationSpec) bool {
	return false
}

func GetNodeResourceReserved(node *corev1.Node) corev1.ResourceList {
	// nodeReserved := max(nodeKubeletReserved, nodeAnnoReserved)
	nodeReserved := util.GetNodeReservationFromKubelet(node)
	if node.Annotations != nil {
		nodeAnnoReserved := util.GetNodeReservationFromAnnotation(node.Annotations)
		nodeReserved = quotav1.Max(nodeReserved, nodeAnnoReserved)
	}
	return nodeReserved
}
