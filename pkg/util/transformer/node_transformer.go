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
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

var nodeTransformers = []func(node *corev1.Node){
	TransformNodeWithNodeReservation,
	TransformNodeDeprecatedBatchResources,
	TransformNodeDeprecatedDeviceResources,
}

func InstallNodeTransformer(informer cache.SharedIndexInformer) {
	if err := informer.SetTransform(TransformNode); err != nil {
		klog.Fatalf("Failed to SetTransform with node, err: %v", err)
	}
}

func TransformNode(obj interface{}) (interface{}, error) {
	var node *corev1.Node
	switch t := obj.(type) {
	case *corev1.Node:
		node = t
	case cache.DeletedFinalStateUnknown:
		node, _ = t.Obj.(*corev1.Node)
	}
	if node == nil {
		return obj, nil
	}

	node = node.DeepCopy()
	for _, fn := range nodeTransformers {
		fn(node)
	}

	if unknown, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		unknown.Obj = node
		return unknown, nil
	}
	return node, nil
}

func TransformNodeWithNodeReservation(node *corev1.Node) {
	node.Status.Allocatable, _ = util.TrimNodeAllocatableByNodeReservation(node)
}

func TransformNodeDeprecatedBatchResources(node *corev1.Node) {
	replaceAndEraseWithResourcesMapper(node.Status.Allocatable, apiext.DeprecatedBatchResourcesMapper)
	replaceAndEraseWithResourcesMapper(node.Status.Capacity, apiext.DeprecatedBatchResourcesMapper)
}

func TransformNodeDeprecatedDeviceResources(node *corev1.Node) {
	replaceAndEraseWithResourcesMapper(node.Status.Allocatable, apiext.DeprecatedDeviceResourcesMapper)
	replaceAndEraseWithResourcesMapper(node.Status.Capacity, apiext.DeprecatedDeviceResourcesMapper)
}
