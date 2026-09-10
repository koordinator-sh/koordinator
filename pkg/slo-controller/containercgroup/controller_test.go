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

package containercgroup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func TestReconciler_SyncToNodeSLO(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))
	assert.NoError(t, slov1alpha1.AddToScheme(scheme))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
			UID:       types.UID("uid-1"),
		},
		Spec: corev1.PodSpec{NodeName: "node-a"},
	}
	cr := &slov1alpha1.ContainerCgroupOverride{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cap",
			Namespace:  "default",
			Finalizers: []string{extension.FinalizerContainerCgroupOverrideWriteback},
		},
		Spec: slov1alpha1.ContainerCgroupOverrideSpec{
			Target: slov1alpha1.ContainerCgroupTarget{
				PodName:       "app",
				ContainerName: "main",
			},
			Resources: extension.ContainerCgroupResources{
				Memory: &extension.MemoryCgroupOverride{Max: "512Mi"},
			},
		},
	}
	nodeSLO := &slov1alpha1.NodeSLO{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Spec:       slov1alpha1.NodeSLOSpec{},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr).WithObjects(pod, cr, nodeSLO).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "cap"},
	})
	assert.NoError(t, err)

	updated := &slov1alpha1.NodeSLO{}
	assert.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "node-a"}, updated))
	assert.NotNil(t, updated.Spec.Extensions)
	raw := updated.Spec.Extensions.Object[extension.ExtensionContainerCgroupOverrides]
	overrides, err := extension.ParseNodeCgroupOverrides(raw)
	assert.NoError(t, err)
	assert.Len(t, overrides.Items, 1)
	assert.Equal(t, "main", overrides.Items[0].ContainerName)
	assert.Equal(t, "512Mi", overrides.Items[0].Resources.MemoryMax())
	assert.Equal(t, "uid-1", overrides.Items[0].PodUID)
}
