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
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/informers/externalversions"
)

func SetupElasticQuotaTransformers(factory externalversions.SharedInformerFactory) {
	resource := v1alpha1.SchemeGroupVersion.WithResource("elasticquotas")
	informer, err := factory.ForResource(resource)
	if err != nil {
		klog.Fatalf("Failed to create informer for resource %v, err: %v", resource.String(), err)
	}
	if err = informer.Informer().SetTransform(TransformElasticQuota); err != nil {
		klog.Fatalf("Failed to SetTransform in informer, resource: %v, err: %v", resource, err)
	}
}

var elasticQuotaTransformers = []func(elasticQuota *v1alpha1.ElasticQuota){
	TransformElasticQuotaWithDeprecatedBatchResources,
}

func TransformElasticQuota(obj interface{}) (interface{}, error) {
	var eq *v1alpha1.ElasticQuota
	switch t := obj.(type) {
	case *v1alpha1.ElasticQuota:
		eq = t
	case cache.DeletedFinalStateUnknown:
		eq, _ = t.Obj.(*v1alpha1.ElasticQuota)
	}
	if eq == nil {
		return obj, nil
	}
	for _, fn := range elasticQuotaTransformers {
		fn(eq)
	}

	if unknown, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		unknown.Obj = eq
		return unknown, nil
	}
	return eq, nil
}

func TransformElasticQuotaWithDeprecatedBatchResources(eq *v1alpha1.ElasticQuota) {
	replaceAndEraseWithResourcesMapper(eq.Spec.Max, apiext.DeprecatedBatchResourcesMapper)
	replaceAndEraseWithResourcesMapper(eq.Spec.Min, apiext.DeprecatedBatchResourcesMapper)
}
