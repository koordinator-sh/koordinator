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
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	frameworkexthelper "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
)

func registerGPUSharedResourceTemplatesConfigMapEventHandler(gpuSharedResourceTemplatesCache *gpuSharedResourceTemplatesCache,
	configMapNamespace, configMapName string, sharedInformerFactory informers.SharedInformerFactory) {
	configMapInformer := sharedInformerFactory.Core().V1().ConfigMaps().Informer()
	eventHandler := cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			switch cm := obj.(type) {
			case *corev1.ConfigMap:
				return cm.Namespace == configMapNamespace && cm.Name == configMapName
			default:
				return false
			}
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				if err := gpuSharedResourceTemplatesCache.setTemplatesInfosFromConfigMap(obj.(*corev1.ConfigMap)); err != nil {
					klog.Error(err)
				}
			},
			UpdateFunc: func(old, new interface{}) {
				if err := gpuSharedResourceTemplatesCache.setTemplatesInfosFromConfigMap(new.(*corev1.ConfigMap)); err != nil {
					klog.Error(err)
				}
			},
			DeleteFunc: func(obj interface{}) {
				gpuSharedResourceTemplatesCache.setTemplatesInfos(nil)
			},
		},
	}
	// make sure ConfigMaps are loaded before scheduler starts working
	frameworkexthelper.ForceSyncFromInformer(context.TODO().Done(), sharedInformerFactory, configMapInformer, eventHandler)
}
