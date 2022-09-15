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

package elasticquota

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

// todo the eventHandler's operation should be a complete transaction in the future work.

func (g *Plugin) OnPodAdd(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}

	quotaName := g.getPodAssociateQuotaName(pod)
	g.groupQuotaManager.UpdatePodCache(quotaName, pod, true)
	g.groupQuotaManager.UpdatePodRequest(quotaName, nil, pod)
	// in case failOver, update pod isAssigned explicitly according to its phase and NodeName.
	if pod.Spec.NodeName != "" && !util.IsPodTerminated(pod) {
		g.groupQuotaManager.UpdatePodIsAssigned(quotaName, pod, true)
		g.groupQuotaManager.UpdatePodUsed(quotaName, nil, pod)
	}
	klog.V(5).Infof("OnPodAddFunc %v.%v add success, quotaName:%v", pod.Namespace, pod.Name, quotaName)
}

func (g *Plugin) OnPodUpdate(oldObj, newObj interface{}) {
	oldPod := oldObj.(*corev1.Pod)
	newPod := newObj.(*corev1.Pod)

	if oldPod.ResourceVersion == newPod.ResourceVersion {
		klog.Warningf("update pod warning, update version for the same:%v", newPod.Name)
		return
	}

	oldQuotaName := g.getPodAssociateQuotaName(oldPod)
	newQuotaName := g.getPodAssociateQuotaName(newPod)

	if oldQuotaName == newQuotaName {
		g.groupQuotaManager.UpdatePodRequest(newQuotaName, oldPod, newPod)
		g.groupQuotaManager.UpdatePodUsed(newQuotaName, oldPod, newPod)
	} else {
		isAssigned := g.groupQuotaManager.GetPodIsAssigned(oldQuotaName, oldPod)
		g.groupQuotaManager.UpdatePodRequest(oldQuotaName, oldPod, nil)
		g.groupQuotaManager.UpdatePodUsed(oldQuotaName, oldPod, nil)
		g.groupQuotaManager.UpdatePodCache(oldQuotaName, oldPod, false)

		g.groupQuotaManager.UpdatePodCache(newQuotaName, newPod, true)
		g.groupQuotaManager.UpdatePodIsAssigned(newQuotaName, newPod, isAssigned)
		g.groupQuotaManager.UpdatePodRequest(newQuotaName, nil, newPod)
		g.groupQuotaManager.UpdatePodUsed(newQuotaName, nil, newPod)
	}

	klog.V(5).Infof("OnPodUpdateFunc %v.%v update success, quotaName:%v", newPod.Namespace, newPod.Name, newQuotaName)
}

func (g *Plugin) OnPodDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}

	quotaName := g.getPodAssociateQuotaName(pod)
	g.groupQuotaManager.UpdatePodRequest(quotaName, pod, nil)
	g.groupQuotaManager.UpdatePodUsed(quotaName, pod, nil)
	g.groupQuotaManager.UpdatePodCache(quotaName, pod, false)
	klog.V(5).Infof("OnPodDeleteFunc %v.%v delete success", pod.Namespace, pod.Name)
}
