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
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func TestQuotaHandler(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	topology := NewQuotaTopology(client)

	parentQuota := MakeQuota("parentQuota").Namespace("kube-system").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(120).Mem(1048576).Obj()).IsParent(true).Obj()

	topology.OnQuotaAdd(parentQuota)

	// check parent quota info
	quotaInfo := topology.getQuotaInfo(parentQuota.Name, parentQuota.Namespace)
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, parentQuota.Name, quotaInfo.Name)
	assert.Equal(t, extension.RootQuotaName, quotaInfo.ParentName)

	childQuota := MakeQuota("childQuota").Namespace("kube-system").Max(MakeResourceList().CPU(120).Mem(1048576).Obj()).
		Min(MakeResourceList().CPU(120).Mem(1048576).Obj()).IsParent(false).ParentName(parentQuota.Name).Annotations(
		map[string]string{extension.AnnotationQuotaNamespaces: `["namespace1"]`},
	).Obj()
	topology.OnQuotaAdd(childQuota)

	// check child quota info
	quotaInfo = topology.getQuotaInfo(childQuota.Name, childQuota.Namespace)
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, childQuota.Name, quotaInfo.Name)
	assert.Equal(t, parentQuota.Name, quotaInfo.ParentName)
	// get quota by namespace
	quotaInfo = topology.getQuotaInfo("", "namespace1")
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, childQuota.Name, quotaInfo.Name)

	// update quota info
	newChildQuota := childQuota.DeepCopy()
	newChildQuota.Annotations[extension.AnnotationQuotaNamespaces] = `["namespace2"]`
	topology.OnQuotaUpdate(childQuota, newChildQuota)
	quotaInfo = topology.getQuotaInfo("", "namespace1")
	assert.Nil(t, quotaInfo)
	quotaInfo = topology.getQuotaInfo("", "namespace2")
	assert.NotNil(t, quotaInfo)
	assert.Equal(t, childQuota.Name, quotaInfo.Name)

	// delete quota
	topology.OnQuotaDelete(newChildQuota)
	quotaInfo = topology.getQuotaInfo(childQuota.Name, childQuota.Namespace)
	assert.Nil(t, quotaInfo)
	quotaInfo = topology.getQuotaInfo("", "namespace2")
	assert.Nil(t, quotaInfo)
}
