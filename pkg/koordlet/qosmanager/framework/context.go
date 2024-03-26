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

package framework

import (
	"context"
	"fmt"

	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers"
	"github.com/koordinator-sh/koordinator/pkg/util"
	expireCache "github.com/koordinator-sh/koordinator/pkg/util/cache"
)

type Context struct {
	Evictor    *Evictor
	Strategies map[string]QOSStrategy
}

type Evictor struct {
	eventRecorder record.EventRecorder
	kubeClient    clientset.Interface
	podsEvicted   *expireCache.Cache
	evictVersion  string
	started       atomic.Bool
}

func NewEvictor(kubeClient clientset.Interface, eventRecorder record.EventRecorder, evictVersion string) *Evictor {
	return &Evictor{
		eventRecorder: eventRecorder,
		kubeClient:    kubeClient,
		podsEvicted:   expireCache.NewCacheDefault(),
		evictVersion:  evictVersion,
	}
}

func (r *Evictor) Start(stopCh <-chan struct{}) error {
	return r.podsEvicted.Run(stopCh)
}

func (r *Evictor) EvictPodsIfNotEvicted(evictPods []*corev1.Pod, node *corev1.Node, reason string, message string) {
	for _, evictPod := range evictPods {
		r.EvictPodIfNotEvicted(evictPod, node, reason, message)
	}
}

func (r *Evictor) EvictPodIfNotEvicted(evictPod *corev1.Pod, node *corev1.Node, reason string, message string) bool {
	_, evicted := r.podsEvicted.Get(string(evictPod.UID))
	if evicted {
		klog.V(5).Infof("Pod has been evicted! podID: %v, evict reason: %s", evictPod.UID, reason)
		return true
	}
	success := r.evictPod(evictPod, reason, message)
	if success {
		_ = r.podsEvicted.SetDefault(string(evictPod.UID), evictPod.UID)
	}

	return success
}

func (r *Evictor) evictPod(evictPod *corev1.Pod, reason string, message string) bool {
	podEvictMessage := fmt.Sprintf("evict Pod:%s/%s, reason: %s, message: %v", evictPod.Namespace, evictPod.Name, reason, message)
	_ = audit.V(0).Pod(evictPod.Namespace, evictPod.Name).Reason(reason).Message(message).Do()

	if err := util.EvictPodByVersion(context.TODO(), r.kubeClient, evictPod.Namespace, evictPod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: nil,
		Preconditions:      metav1.NewUIDPreconditions(string(evictPod.UID))}, r.evictVersion); err == nil {
		r.eventRecorder.Eventf(evictPod, corev1.EventTypeWarning, helpers.EvictPodSuccess, podEvictMessage)
		metrics.RecordPodEviction(evictPod.Namespace, evictPod.Name, reason)
		klog.Infof("evict pod %v/%v success, reason: %v", evictPod.Namespace, evictPod.Name, reason)
		return true
	} else {
		errorMsg := fmt.Sprintf("%v, error %v", podEvictMessage, err)
		r.eventRecorder.Eventf(evictPod, corev1.EventTypeWarning, helpers.EvictPodFail, errorMsg)
		klog.Errorf("evict pod %v/%v failed, reason: %v, error: %v", evictPod.Namespace, evictPod.Name, reason, err)
		return false
	}
}
