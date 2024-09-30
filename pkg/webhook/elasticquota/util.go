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

package elasticquota

import (
	"encoding/json"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testing2 "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
)

type PodWrapper struct{ *v1.Pod }

func MakePod(namespace, name string) *PodWrapper {
	pod := testing2.MakePod().Namespace(namespace).Name(name).Obj()

	return &PodWrapper{pod}
}

func (p *PodWrapper) Label(string1, string2 string) *PodWrapper {
	if p.Labels == nil {
		p.Labels = make(map[string]string)
	}
	p.Labels[string1] = string2
	return p
}

func (p *PodWrapper) Container(request v1.ResourceList) *PodWrapper {
	p.Pod.Spec.Containers = append(p.Pod.Spec.Containers, v1.Container{
		Name:  fmt.Sprintf("con%d", len(p.Pod.Spec.Containers)),
		Image: "image",
		Resources: v1.ResourceRequirements{
			Requests: request,
		},
	})
	return p
}

func (p *PodWrapper) Obj() *v1.Pod {
	return p.Pod
}

type QuotaWrapper struct {
	*v1alpha1.ElasticQuota
}

func MakeQuota(name string) *QuotaWrapper {
	eq := &v1alpha1.ElasticQuota{
		TypeMeta: metav1.TypeMeta{Kind: "ElasticQuota", APIVersion: "scheduling.sigs.k8s.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
		},
	}
	return &QuotaWrapper{eq}
}

func (q *QuotaWrapper) Namespace(ns string) *QuotaWrapper {
	q.ElasticQuota.Namespace = ns
	return q
}

func (q *QuotaWrapper) Min(min v1.ResourceList) *QuotaWrapper {
	q.ElasticQuota.Spec.Min = min
	return q
}

func (q *QuotaWrapper) Max(max v1.ResourceList) *QuotaWrapper {
	q.ElasticQuota.Spec.Max = max
	return q
}

func (q *QuotaWrapper) Used(used v1.ResourceList) *QuotaWrapper {
	q.ElasticQuota.Status.Used = used
	return q
}

func (q *QuotaWrapper) ChildRequest(request v1.ResourceList) *QuotaWrapper {
	raw, err := json.Marshal(request)
	if err == nil {
		q.ElasticQuota.Annotations[extension.AnnotationChildRequest] = string(raw)
	}
	return q
}

func (q *QuotaWrapper) Admission(request v1.ResourceList) *QuotaWrapper {
	raw, err := json.Marshal(request)
	if err == nil {
		q.ElasticQuota.Annotations[extension.AnnotationAdmission] = string(raw)
	}
	return q
}

func (q *QuotaWrapper) TreeID(tree string) *QuotaWrapper {
	q.ElasticQuota.Labels[extension.LabelQuotaTreeID] = tree
	return q
}

func (q *QuotaWrapper) Guaranteed(guaranteed v1.ResourceList) *QuotaWrapper {
	raw, err := json.Marshal(guaranteed)
	if err == nil {
		q.ElasticQuota.Annotations[extension.AnnotationGuaranteed] = string(raw)
	}
	return q
}

func (q *QuotaWrapper) IsRoot(isRoot bool) *QuotaWrapper {
	if isRoot {
		q.Labels[extension.LabelQuotaIsRoot] = "true"
	}
	return q
}

func (q *QuotaWrapper) sharedWeight(sharedWeight v1.ResourceList) *QuotaWrapper {
	sharedWeightBytes, _ := json.Marshal(sharedWeight)
	q.ElasticQuota.Annotations[extension.AnnotationSharedWeight] = string(sharedWeightBytes)
	return q
}

func (q *QuotaWrapper) IsParent(isParent bool) *QuotaWrapper {
	if isParent {
		q.Labels[extension.LabelQuotaIsParent] = "true"
	} else {
		q.Labels[extension.LabelQuotaIsParent] = "false"
	}
	return q
}

func (q *QuotaWrapper) ParentName(parentName string) *QuotaWrapper {
	q.Labels[extension.LabelQuotaParent] = parentName
	return q
}

func (q *QuotaWrapper) Annotations(annotations map[string]string) *QuotaWrapper {
	for k, v := range annotations {
		q.ElasticQuota.Annotations[k] = v
	}
	return q
}

func (q *QuotaWrapper) Obj() *v1alpha1.ElasticQuota {
	return q.ElasticQuota
}

type resourceWrapper struct{ v1.ResourceList }

func MakeResourceList() *resourceWrapper {
	return &resourceWrapper{v1.ResourceList{}}
}

func (r *resourceWrapper) CPU(val int64) *resourceWrapper {
	r.ResourceList[v1.ResourceCPU] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) Mem(val int64) *resourceWrapper {
	r.ResourceList[v1.ResourceMemory] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) GPU(val int64) *resourceWrapper {
	r.ResourceList["nvidia.com/gpu"] = *resource.NewQuantity(val, resource.DecimalSI)
	return r
}

func (r *resourceWrapper) Obj() v1.ResourceList {
	return r.ResourceList
}
