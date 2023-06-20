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

package impl

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	pvcInformerName PluginName = "pvcInformer"
)

type pvcInformer struct {
	pvcInformer cache.SharedIndexInformer
	pvcRWMutex  sync.RWMutex
	// The key of the map is the name of the PVC, in the format of PVC namespace/PVC name.
	// The value of the map is the name of the PV bound to the PVC.
	volumeNameMap map[string]string
}

func NewPVCInformer() *pvcInformer {
	return &pvcInformer{
		volumeNameMap: map[string]string{},
	}
}

func (s *pvcInformer) GetVolumeName(pvcNamespace, pvcName string) string {
	s.pvcRWMutex.RLock()
	defer s.pvcRWMutex.RUnlock()
	return s.volumeNameMap[util.GetNamespacedName(pvcNamespace, pvcName)]
}

func (s *pvcInformer) Setup(ctx *PluginOption, state *PluginState) {
	s.pvcInformer = newPVCInformer(ctx.KubeClient)
	s.pvcInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pvc, ok := obj.(*corev1.PersistentVolumeClaim)
			if ok {
				s.updateVolumeNameMap(pvc)
				klog.Infof("add PVC %s", util.GetNamespacedName(pvc.Namespace, pvc.Name))
			} else {
				klog.Errorf("pvc informer add func parse PVC failed")
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			pvc, ok := newObj.(*corev1.PersistentVolumeClaim)
			if ok {
				s.updateVolumeNameMap(pvc)
				klog.Infof("update PVC %s", util.GetNamespacedName(pvc.Namespace, pvc.Name))
			} else {
				klog.Errorf("pvc informer update func parse PVC failed")
			}
		},
	})
}

func (s *pvcInformer) Start(stopCh <-chan struct{}) {
	if !features.DefaultKoordletFeatureGate.Enabled(features.BlkIOReconcile) {
		klog.V(4).Infof("feature %v is not enabled, skip running pvc informer", features.BlkIOReconcile)
		return
	}
	klog.V(2).Infof("starting node pvc informer")
	go s.pvcInformer.Run(stopCh)
	klog.V(2).Infof("node pvc informer started")
}

func (s *pvcInformer) HasSynced() bool {
	// TODO add interface to check whether a plugin is enabled
	if !features.DefaultKoordletFeatureGate.Enabled(features.BlkIOReconcile) {
		klog.V(5).Infof("feature %v is not enabled, has sync return true", features.BlkIOReconcile)
		return true
	}
	if s.pvcInformer == nil {
		return false
	}
	synced := s.pvcInformer.HasSynced()
	klog.V(5).Infof("node pvc informer has synced %v", synced)
	return synced
}

func (s *pvcInformer) updateVolumeNameMap(pvc *corev1.PersistentVolumeClaim) {
	s.pvcRWMutex.Lock()
	defer s.pvcRWMutex.Unlock()

	if pvc == nil {
		return
	}

	s.volumeNameMap[util.GetNamespacedName(pvc.Namespace, pvc.Name)] = pvc.Spec.VolumeName
}

func newPVCInformer(client clientset.Interface) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (apiruntime.Object, error) {
				return client.CoreV1().PersistentVolumeClaims(metav1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return client.CoreV1().PersistentVolumeClaims(metav1.NamespaceAll).Watch(context.TODO(), options)
			},
		},
		&corev1.PersistentVolumeClaim{},
		time.Hour*12,
		cache.Indexers{},
	)
}
