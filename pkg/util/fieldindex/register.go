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

package fieldindex

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/koordinator-sh/koordinator/apis/extension"
	apiv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

var registerOnce sync.Once

type fieldIndexDescriptor struct {
	description string
	obj         client.Object
	field       string
	indexerFunc client.IndexerFunc
}

// NOTE: add field index here if needed
var indexDescriptors = []fieldIndexDescriptor{
	{
		description: "index pod by spec.NodeName",
		obj:         &corev1.Pod{},
		field:       "spec.nodeName",
		indexerFunc: func(obj client.Object) []string {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return []string{}
			}
			if len(pod.Spec.NodeName) == 0 {
				return []string{}
			}
			return []string{pod.Spec.NodeName}
		},
	},
	{
		description: "index pod by label.QuotaName",
		obj:         &corev1.Pod{},
		field:       "label.quotaName",
		indexerFunc: func(obj client.Object) []string {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return []string{}
			}
			if len(pod.Labels) == 0 || pod.Labels[extension.LabelQuotaName] == "" {
				return []string{}
			}
			return []string{pod.Labels[extension.LabelQuotaName]}
		},
	},
	{
		description: "index elastic quota by annotation.namespaces",
		obj:         &apiv1alpha1.ElasticQuota{},
		field:       "annotation.namespaces",
		indexerFunc: func(obj client.Object) []string {
			eq, ok := obj.(*apiv1alpha1.ElasticQuota)
			if !ok {
				return []string{}
			}
			if len(eq.Annotations) == 0 || eq.Annotations[extension.AnnotationQuotaNamespaces] == "" {
				return []string{}
			}
			return extension.GetAnnotationQuotaNamespaces(eq)
		},
	},
	{
		description: "index elastic quota by name",
		obj:         &apiv1alpha1.ElasticQuota{},
		field:       "metadata.name",
		indexerFunc: func(obj client.Object) []string {
			eq, ok := obj.(*apiv1alpha1.ElasticQuota)
			if !ok {
				return []string{}
			}
			return []string{eq.Name}
		},
	},
}

func RegisterFieldIndexes(c cache.Cache) error {
	var err error
	registerOnce.Do(func() {
		for _, descriptor := range indexDescriptors {
			err = c.IndexField(context.Background(), descriptor.obj, descriptor.field, descriptor.indexerFunc)
			if err != nil {
				klog.ErrorS(err, "Failed to register field index", "description", descriptor.description, "field", descriptor.field)
				return
			}
		}
	})
	return err
}
