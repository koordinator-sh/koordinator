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
	nrtinformers "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

var transformers = map[schema.GroupVersionResource]cache.TransformFunc{
	corev1.SchemeGroupVersion.WithResource("nodes"):               TransformNode,
	schedulingv1alpha1.SchemeGroupVersion.WithResource("devices"): TransformDevice,
}

type TransformFactory func() cache.TransformFunc

var transformerFactories = map[schema.GroupVersionResource]TransformFactory{
	corev1.SchemeGroupVersion.WithResource("pods"): TransformPodFactory,
}

func SetupTransformers(informerFactory informers.SharedInformerFactory, koordInformerFactory koordinformers.SharedInformerFactory, nodeResourceTopologyInformerFactory nrtinformers.SharedInformerFactory) {
	for resource, transformFn := range transformers {
		informer, err := informerFactory.ForResource(resource)
		if err != nil {
			informer, err = koordInformerFactory.ForResource(resource)
			if err != nil {
				informer, err = nodeResourceTopologyInformerFactory.ForResource(resource)
				if err != nil {
					klog.Fatalf("Failed to create informer for resource %v, err: %v", resource.String(), err)
				}
			}
		}
		if err := informer.Informer().SetTransform(transformFn); err != nil {
			klog.Fatalf("Failed to SetTransform in informer, resource: %v, err: %v", resource, err)
		}

	}
	for resource, transformFactory := range transformerFactories {
		informer, err := informerFactory.ForResource(resource)
		if err != nil {
			informer, err = koordInformerFactory.ForResource(resource)
			if err != nil {
				klog.Fatalf("Failed to create informer for resource %v, err: %v", resource.String(), err)
			}
		}
		// new transformFunc after initialization
		transformFn := transformFactory()
		if err := informer.Informer().SetTransform(transformFn); err != nil {
			klog.Fatalf("Failed to SetTransform in informer, resource: %v, err: %v", resource, err)
		}
	}

}
