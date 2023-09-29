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

package transformer

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

type transformerDescriptor struct {
	object      client.Object
	gvr         schema.GroupVersionResource
	transformer toolscache.TransformFunc
}

var transformers = []transformerDescriptor{
	{
		object:      &corev1.Node{},
		gvr:         corev1.SchemeGroupVersion.WithResource("nodes"),
		transformer: TransformNode,
	},
	{
		object:      &corev1.Pod{},
		gvr:         corev1.SchemeGroupVersion.WithResource("pods"),
		transformer: TransformPod,
	},
	{
		object:      &schedulingv1alpha1.Device{},
		gvr:         schedulingv1alpha1.SchemeGroupVersion.WithResource("devices"),
		transformer: TransformDevice,
	},
}

func SetupTransformers(informerFactory informers.SharedInformerFactory, koordInformerFactory koordinformers.SharedInformerFactory) {
	for _, descriptor := range transformers {
		informer, err := informerFactory.ForResource(descriptor.gvr)
		if err != nil {
			informer, err = koordInformerFactory.ForResource(descriptor.gvr)
			if err != nil {
				klog.Fatalf("Failed to create informer for resource %v, err: %v", descriptor.gvr.String(), err)
			}
		}
		if err := informer.Informer().SetTransform(descriptor.transformer); err != nil {
			klog.Fatalf("Failed to SetTransform in informer, resource: %v, err: %v", descriptor.gvr, err)
		}
	}
}

func GetTransformByObject() cache.TransformByObject {
	m := cache.TransformByObject{}
	for _, descriptor := range transformers {
		m[descriptor.object] = descriptor.transformer
	}
	return m
}
