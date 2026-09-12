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
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

func newTestScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))
	assert.NoError(t, slov1alpha1.AddToScheme(scheme))
	return scheme
}

func newCR(namespace, name string) *slov1alpha1.ContainerCgroupOverride {
	return &slov1alpha1.ContainerCgroupOverride{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
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
}

func TestReconciler_AddFinalizer(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("default", "cap")
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr).WithObjects(cr).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "cap"},
	})
	assert.NoError(t, err)

	updated := &slov1alpha1.ContainerCgroupOverride{}
	assert.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "cap"}, updated))
	assert.True(t, controllerutil.ContainsFinalizer(updated, extension.FinalizerContainerCgroupOverrideWriteback))
}

func TestReconciler_EmptyResources(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("default", "cap")
	cr.Spec.Resources = extension.ContainerCgroupResources{}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr).WithObjects(cr).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "cap"},
	})
	assert.NoError(t, err)

	updated := &slov1alpha1.ContainerCgroupOverride{}
	assert.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "cap"}, updated))
	assert.Equal(t, extension.CgroupOverridePhaseFailed, updated.Status.Phase)
}

func TestReconciler_PodNotFound(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("default", "cap")
	controllerutil.AddFinalizer(cr, extension.FinalizerContainerCgroupOverrideWriteback)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr).WithObjects(cr).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "cap"},
	})
	assert.NoError(t, err)

	updated := &slov1alpha1.ContainerCgroupOverride{}
	assert.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "cap"}, updated))
	assert.Equal(t, extension.CgroupOverridePhaseFailed, updated.Status.Phase)
	assert.Equal(t, "target pod not found", updated.Status.Message)
}

func TestReconciler_PodNotScheduled(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: types.UID("uid-1")},
	}
	cr := newCR("default", "cap")
	controllerutil.AddFinalizer(cr, extension.FinalizerContainerCgroupOverrideWriteback)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr).WithObjects(cr, pod).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "cap"},
	})
	assert.NoError(t, err)
	assert.Equal(t, 5*time.Second, res.RequeueAfter)

	updated := &slov1alpha1.ContainerCgroupOverride{}
	assert.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "cap"}, updated))
	assert.Equal(t, extension.CgroupOverridePhasePending, updated.Status.Phase)
}

func TestReconciler_PodUIDMismatch(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: types.UID("uid-1")},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
	}
	cr := newCR("default", "cap")
	cr.Spec.Target.PodUID = "uid-999"
	controllerutil.AddFinalizer(cr, extension.FinalizerContainerCgroupOverrideWriteback)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr).WithObjects(cr, pod).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "cap"},
	})
	assert.NoError(t, err)

	updated := &slov1alpha1.ContainerCgroupOverride{}
	assert.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "cap"}, updated))
	assert.Equal(t, extension.CgroupOverridePhaseFailed, updated.Status.Phase)
	assert.Equal(t, "pod UID mismatch", updated.Status.Message)
}

func TestReconciler_DeletionReleasedRemovesFinalizer(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: types.UID("uid-1")},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
	}
	now := metav1.Now()
	cr := newCR("default", "cap")
	cr.DeletionTimestamp = &now
	cr.Status.Phase = extension.CgroupOverridePhaseReleased
	controllerutil.AddFinalizer(cr, extension.FinalizerContainerCgroupOverrideWriteback)

	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr).WithObjects(cr, pod).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "cap"},
	})
	assert.NoError(t, err)

	// with DeletionTimestamp + Released, last finalizer is removed and the CR is GC'd
	updated := &slov1alpha1.ContainerCgroupOverride{}
	err = c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "cap"}, updated)
	assert.True(t, errors.IsNotFound(err))
}

func TestReconciler_MapPodToOverrides(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: types.UID("uid-1")}}
	cr := newCR("default", "cap")
	cr2 := newCR("default", "other")
	cr2.Spec.Target.PodName = "diff-pod"
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr, cr2, pod).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	reqs := r.mapPodToOverrides(context.Background(), pod)
	assert.Len(t, reqs, 1)
	assert.Equal(t, "cap", reqs[0].Name)

	// non-pod object -> nil
	assert.Nil(t, r.mapPodToOverrides(context.Background(), cr))

	// pod not namespace-scoped to CR list -> nil requests
	otherPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "other"}}
	assert.Empty(t, r.mapPodToOverrides(context.Background(), otherPod))
}

func TestReconciler_MapNodeSLOToOverrides(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("default", "cap")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	reqs := r.mapNodeSLOToOverrides(context.Background(), &slov1alpha1.NodeSLO{})
	assert.Len(t, reqs, 1)
	assert.Equal(t, "cap", reqs[0].Name)

	// no CRs -> nil
	c2 := fake.NewClientBuilder().WithScheme(scheme).Build()
	r2 := &Reconciler{Client: c2, Scheme: scheme}
	assert.Nil(t, r2.mapNodeSLOToOverrides(context.Background(), &slov1alpha1.NodeSLO{}))
}

func TestSyncAllNodesMissingNodeSLO(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: types.UID("uid-1")},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
	}
	cr := newCR("default", "cap")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr, pod).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	res, err := r.syncAllNodes(context.Background())
	assert.NoError(t, err)
	// no NodeSLO for node-a -> requeue
	assert.Equal(t, 10*time.Second, res.RequeueAfter)
}

func TestToItem(t *testing.T) {
	scheme := newTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default", UID: types.UID("uid-1")},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
	}
	// empty resources -> ok false
	crEmpty := newCR("default", "cap")
	crEmpty.Spec.Resources = extension.ContainerCgroupResources{}
	// pod not found -> ok false
	crNoPod := newCR("default", "nopod")
	crNoPod.Spec.Target.PodName = "missing"
	// pod not scheduled -> ok false
	crUnsched := newCR("default", "unsched")
	crUnsched.Spec.Target.PodName = "unsched-pod"
	unschedPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "unsched-pod", Namespace: "default"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(crNoPod, crUnsched, pod, unschedPod).Build()
	r := &Reconciler{Client: c, Scheme: scheme}

	_, _, ok := r.toItem(context.Background(), crEmpty)
	assert.False(t, ok)
	_, _, ok = r.toItem(context.Background(), crNoPod)
	assert.False(t, ok)
	_, _, ok = r.toItem(context.Background(), crUnsched)
	assert.False(t, ok)

	// happy path
	cr := newCR("default", "cap")
	cr.Status.Baseline = &extension.ContainerCgroupResources{Memory: &extension.MemoryCgroupOverride{Max: "1Gi"}}
	item, node, ok := r.toItem(context.Background(), cr)
	assert.True(t, ok)
	assert.Equal(t, "node-a", node)
	assert.Equal(t, "main", item.ContainerName)
	assert.Equal(t, "uid-1", item.PodUID)
	assert.NotEmpty(t, item.DesiredHash)
	assert.True(t, item.WritebackOnDelete)
	assert.False(t, item.PendingDelete)
}

func TestPatchNodeSLO(t *testing.T) {
	scheme := newTestScheme(t)
	item := extension.NodeCgroupOverrideItem{
		Namespace:     "default",
		Name:          "cap",
		PodNamespace:  "default",
		PodName:       "app",
		PodUID:        "uid-1",
		ContainerName: "main",
		Resources: extension.ContainerCgroupResources{
			Memory: &extension.MemoryCgroupOverride{Max: "512Mi"},
		},
		DesiredHash: "abc123",
	}

	nodeA := &slov1alpha1.NodeSLO{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
	nodeB := &slov1alpha1.NodeSLO{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}}
	nodeB.Spec.Extensions = &slov1alpha1.ExtensionsMap{Object: map[string]interface{}{
		extension.ExtensionContainerCgroupOverrides: &extension.NodeCgroupOverrides{Items: []extension.NodeCgroupOverrideItem{item}},
	}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodeA, nodeB).Build()
	r := &Reconciler{Client: c, Scheme: scheme}
	ctx := context.Background()

	// initial patch on node-a
	nodeA = &slov1alpha1.NodeSLO{}
	assert.NoError(t, c.Get(ctx, types.NamespacedName{Name: "node-a"}, nodeA))
	assert.NoError(t, r.patchNodeSLO(ctx, nodeA, []extension.NodeCgroupOverrideItem{item}))
	assert.NotNil(t, nodeA.Spec.Extensions)
	raw := nodeA.Spec.Extensions.Object[extension.ExtensionContainerCgroupOverrides]
	parsed, err := extension.ParseNodeCgroupOverrides(raw)
	assert.NoError(t, err)
	assert.Len(t, parsed.Items, 1)

	// no-op when identical (re-fetch current state)
	nodeA = &slov1alpha1.NodeSLO{}
	assert.NoError(t, c.Get(ctx, types.NamespacedName{Name: "node-a"}, nodeA))
	err = r.patchNodeSLO(ctx, nodeA, []extension.NodeCgroupOverrideItem{item})
	assert.NoError(t, err)

	// changed item -> patch applied
	nodeA = &slov1alpha1.NodeSLO{}
	assert.NoError(t, c.Get(ctx, types.NamespacedName{Name: "node-a"}, nodeA))
	changed := item
	changed.DesiredHash = "zzz"
	assert.NoError(t, r.patchNodeSLO(ctx, nodeA, []extension.NodeCgroupOverrideItem{changed}))
	raw = nodeA.Spec.Extensions.Object[extension.ExtensionContainerCgroupOverrides]
	parsed, err = extension.ParseNodeCgroupOverrides(raw)
	assert.NoError(t, err)
	assert.Equal(t, "zzz", parsed.Items[0].DesiredHash)

	// empty items -> key removed on node-b
	nodeB = &slov1alpha1.NodeSLO{}
	assert.NoError(t, c.Get(ctx, types.NamespacedName{Name: "node-b"}, nodeB))
	assert.NoError(t, r.patchNodeSLO(ctx, nodeB, nil))
	_, exists := nodeB.Spec.Extensions.Object[extension.ExtensionContainerCgroupOverrides]
	assert.False(t, exists)
}
