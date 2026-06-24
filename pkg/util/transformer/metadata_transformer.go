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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

var metadataTransformers = []func(meta *metav1.PartialObjectMetadata){
	TransformPartialMetadataRemoveResources,
}

func InstallMetadataTransformer(informer cache.SharedIndexInformer) {
	if err := informer.SetTransform(TransformPod); err != nil {
		klog.Fatalf("Failed to SetTransform with metadata, err: %v", err)
	}
}

func TransformMeta(obj interface{}) (interface{}, error) {
	var meta *metav1.PartialObjectMetadata
	switch t := obj.(type) {
	case *metav1.PartialObjectMetadata:
		meta = t
	case cache.DeletedFinalStateUnknown:
		meta, _ = t.Obj.(*metav1.PartialObjectMetadata)
	}
	if meta == nil {
		return obj, nil
	}

	for _, fn := range metadataTransformers {
		fn(meta)
	}

	if unknown, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		unknown.Obj = meta
		return unknown, nil
	}
	return meta, nil
}

func TransformPartialMetadataRemoveResources(partialMeta *metav1.PartialObjectMetadata) {
	partialMeta.ManagedFields = nil
}
