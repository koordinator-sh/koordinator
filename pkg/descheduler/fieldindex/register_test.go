/*
Copyright 2026 The Koordinator Authors.

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
	"fmt"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sev1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/stretchr/testify/require"
)

// fakeCache implements cache.Cache with minimal behaviour for IndexField
type fakeCache struct {
	funcs            map[string]client.IndexerFunc
	returnErrOnField string
}

var _ cache.Cache = &fakeCache{}

func (f *fakeCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return nil
}

func (f *fakeCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return nil
}

func (f *fakeCache) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, nil
}

func (f *fakeCache) GetInformerForKind(ctx context.Context, gvk schema.GroupVersionKind, opts ...cache.InformerGetOption) (cache.Informer, error) {
	return nil, nil
}

func (f *fakeCache) RemoveInformer(ctx context.Context, obj client.Object) error {
	return nil
}

func (f *fakeCache) Start(ctx context.Context) error {
	return nil
}

func (f *fakeCache) WaitForCacheSync(ctx context.Context) bool {
	return true
}

// IndexField stores the extractor so tests can invoke it, or optionally
// return an injected error to simulate registration failure.
func (f *fakeCache) IndexField(ctx context.Context, obj client.Object, field string, extract client.IndexerFunc) error {
	if f.funcs == nil {
		f.funcs = make(map[string]client.IndexerFunc)
	}
	if f.returnErrOnField != "" && f.returnErrOnField == field {
		return fmt.Errorf("injected error for field %s", field)
	}
	f.funcs[field] = extract
	return nil
}

func TestRegisterFieldIndexes_Success(t *testing.T) {
	// reset package-level once so RegisterFieldIndexes runs fresh
	registerOnce = sync.Once{}

	fc := &fakeCache{}
	err := RegisterFieldIndexes(fc)
	require.NoError(t, err)

	// ensure indexers were registered
	require.NotNil(t, fc.funcs)
	// Pod by node name
	idx := fc.funcs[IndexPodByNodeName]
	require.NotNil(t, idx)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{}, Spec: corev1.PodSpec{NodeName: "node-a"}}
	keys := idx(pod)
	require.Equal(t, []string{"node-a"}, keys)

	// empty node name -> no keys
	pod2 := &corev1.Pod{Spec: corev1.PodSpec{NodeName: ""}}
	keys = idx(pod2)
	require.Equal(t, 0, len(keys))

	// Pod owner refs
	idxOwner := fc.funcs[IndexPodByOwnerRefUID]
	require.NotNil(t, idxOwner)
	pod3 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{UID: "u1"}, {UID: "u2"}}}}
	keys = idxOwner(pod3)
	require.ElementsMatch(t, []string{"u1", "u2"}, keys)

	// PodMigrationJob indexes
	idxJobUID := fc.funcs[IndexJobByPodUID]
	require.NotNil(t, idxJobUID)
	job := &sev1alpha1.PodMigrationJob{Spec: sev1alpha1.PodMigrationJobSpec{PodRef: &corev1.ObjectReference{UID: "pod-uid"}}}
	keys = idxJobUID(job)
	require.Equal(t, []string{"pod-uid"}, keys)

	idxJobNSName := fc.funcs[IndexJobPodNamespacedName]
	require.NotNil(t, idxJobNSName)
	job2 := &sev1alpha1.PodMigrationJob{Spec: sev1alpha1.PodMigrationJobSpec{PodRef: &corev1.ObjectReference{Namespace: "myns", Name: "mypod"}}}
	keys = idxJobNSName(job2)
	require.Equal(t, []string{"myns/mypod"}, keys)

	idxJobNS := fc.funcs[IndexJobByPodNamespace]
	require.NotNil(t, idxJobNS)
	keys = idxJobNS(job2)
	require.Equal(t, []string{"myns"}, keys)
}

func TestRegisterFieldIndexes_FailurePropagates(t *testing.T) {
	// reset once
	registerOnce = sync.Once{}

	fc := &fakeCache{returnErrOnField: IndexPodByNodeName}
	err := RegisterFieldIndexes(fc)
	require.Error(t, err)
}
