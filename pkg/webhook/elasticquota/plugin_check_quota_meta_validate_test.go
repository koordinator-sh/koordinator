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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestQuotaMetaChecker(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	sche := client.Scheme()
	sche.AddKnownTypes(schema.GroupVersion{
		Group:   "scheduling.sigs.k8s.io",
		Version: "v1alpha1",
	}, &v1alpha1.ElasticQuota{}, &v1alpha1.ElasticQuotaList{})
	decoder := admission.NewDecoder(sche)

	plugin := NewPlugin(decoder, client)

	request := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Resource: metav1.GroupVersionResource{
				Group:    "scheduling.sigs.k8s.io",
				Version:  "v1alpha1",
				Resource: "elasticquotas",
			},
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{},
		},
	}

	parentQuota := MakeQuota("parentQuota").Namespace("kube-system").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(120).Mem(1048576).Obj()).IsParent(true).Obj()

	// validate quota
	err := plugin.ValidateQuota(context.TODO(), request, parentQuota)
	assert.Nil(t, err)

	// get quota info
	quotaInfo := plugin.GetQuotaInfo(parentQuota.Name, parentQuota.Namespace)
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, parentQuota.Name, quotaInfo.Name)
	assert.Equal(t, extension.RootQuotaName, quotaInfo.ParentName)
}
