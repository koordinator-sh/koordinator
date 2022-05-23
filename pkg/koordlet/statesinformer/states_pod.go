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

package statesinformer

import (
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

func (s *statesInformer) syncKubelet() error {
	podList, err := s.kubelet.GetAllPods()
	if err != nil {
		klog.Warningf("get pods from kubelet failed, err: %v", err)
		return err
	}
	newPodMap := make(map[string]*PodMeta, len(podList.Items))
	for _, pod := range podList.Items {
		newPodMap[string(pod.UID)] = &PodMeta{
			Pod:       pod.DeepCopy(),
			CgroupDir: genPodCgroupParentDir(&pod),
		}
	}
	s.podMap = newPodMap
	s.podHasSynced.Store(true)
	s.podUpdatedTime = time.Now()
	klog.Infof("get pods from kubelet success, len %d", len(s.podMap))
	return nil
}

func (s *statesInformer) syncKubeletLoop(duration time.Duration, stopCh <-chan struct{}) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	// TODO add a config to setup the values
	rateLimiter := rate.NewLimiter(5, 10)
	for {
		select {
		case <-s.podCreated:
			if rateLimiter.Allow() {
				// sync kubelet triggered immediately when the Pod is created
				s.syncKubelet()
				// reset timer to
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(duration)
			}
		case <-timer.C:
			timer.Reset(duration)
			s.syncKubelet()
		case <-stopCh:
			klog.Infof("sync kubelet loop is exited")
			return
		}
	}
}

func genPodCgroupParentDir(pod *corev1.Pod) string {
	// todo use cri interface to get pod cgroup dir
	// e.g. kubepods-burstable.slice/kubepods-burstable-pod9dba1d9e_67ba_4db6_8a73_fb3ea297c363.slice/
	return util.GetPodKubeRelativePath(pod)
}
