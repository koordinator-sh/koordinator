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

package helper

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestSyncedEventHandler(t *testing.T) {
	var objects []runtime.Object
	for i := 0; i < 10; i++ {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				UID:             uuid.NewUUID(),
				Name:            fmt.Sprintf("node-%d", i),
				ResourceVersion: fmt.Sprintf("%d", i+1),
			},
		}
		objects = append(objects, node)
	}
	fakeClientSet := kubefake.NewSimpleClientset(objects...)
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	nodeInformer := sharedInformerFactory.Core().V1().Nodes()
	addTimes := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(10)

	// ForceSyncFromInformer only registers the handler; the informer must be
	// started explicitly via factory.Start().
	ForceSyncFromInformer(context.TODO().Done(), sharedInformerFactory, nodeInformer.Informer(), cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			node := obj.(*corev1.Node)
			mu.Lock()
			addTimes[node.Name]++
			mu.Unlock()
			wg.Done()
		},
	})

	// Start informers so that the registered handler receives initial list events.
	stopCh := make(chan struct{})
	defer close(stopCh)
	sharedInformerFactory.Start(stopCh)

	wg.Wait()

	mu.Lock()
	for _, v := range addTimes {
		if v > 1 {
			mu.Unlock()
			t.Errorf("unexpected add times, want 1 but got %d", v)
			return
		}
	}
	mu.Unlock()

	sharedInformerFactory.WaitForCacheSync(stopCh)
	node, err := nodeInformer.Lister().Get("node-0")
	assert.NoError(t, err)
	assert.NotNil(t, node)
	node = node.DeepCopy()
	node.ResourceVersion = "100"
	_, err = fakeClientSet.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	assert.NoError(t, err)
	err = wait.PollUntil(1*time.Second, func() (done bool, err error) {
		node, err := nodeInformer.Lister().Get("node-0")
		assert.NoError(t, err)
		assert.NotNil(t, node)
		return node.ResourceVersion == "100", nil
	}, wait.NeverStop)
	assert.NoError(t, err)
}

// TestForceSyncFromInformerWithReplace_NilReplaceHandler ensures that passing a
// nil replaceHandler is equivalent to the plain ForceSyncFromInformer path and
// does NOT register a startup hook.
func TestForceSyncFromInformerWithReplace_NilReplaceHandler(t *testing.T) {
	ResetRegistrations()
	defer ResetRegistrations()

	fakeClientSet := kubefake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	nodeInformer := sharedInformerFactory.Core().V1().Nodes()

	_, err := ForceSyncFromInformerWithReplace(
		context.TODO().Done(),
		sharedInformerFactory,
		nodeInformer.Informer(),
		cache.ResourceEventHandlerFuncs{},
		nil, // replaceHandler
	)
	assert.NoError(t, err)

	// Running the startup phase must be a no-op: no hook was registered.
	runCtx, runCancel := context.WithTimeout(context.Background(), time.Second)
	defer runCancel()
	assert.NoError(t, RunAfterPluginInformersSynced(runCtx))
}

// TestForceSyncFromInformerWithReplace_PropagatesError ensures a non-nil error
// returned from replaceHandler is surfaced verbatim through
// RunAfterPluginInformersSynced so the scheduler startup path can fail fast.
func TestForceSyncFromInformerWithReplace_PropagatesError(t *testing.T) {
	ResetRegistrations()
	defer ResetRegistrations()

	var objects []runtime.Object
	for i := 0; i < 3; i++ {
		objects = append(objects, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				UID:             uuid.NewUUID(),
				Name:            fmt.Sprintf("node-%d", i),
				ResourceVersion: fmt.Sprintf("%d", i+1),
			},
		})
	}
	fakeClientSet := kubefake.NewSimpleClientset(objects...)
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	nodeInformer := sharedInformerFactory.Core().V1().Nodes()

	sentinel := errors.New("replace-failed")
	_, err := ForceSyncFromInformerWithReplace(
		context.TODO().Done(),
		sharedInformerFactory,
		nodeInformer.Informer(),
		cache.ResourceEventHandlerFuncs{},
		func(objs []interface{}) error { return sentinel },
	)
	assert.NoError(t, err)

	stopCh := make(chan struct{})
	defer close(stopCh)
	sharedInformerFactory.Start(stopCh)
	sharedInformerFactory.WaitForCacheSync(stopCh)

	runCtx, runCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer runCancel()
	err = RunAfterPluginInformersSynced(runCtx)
	assert.ErrorIs(t, err, sentinel, "replaceHandler error must propagate via the AfterPluginInformersSynced hook")
}

// TestForceSyncFromInformerWithReplace_CtxCanceledBeforeSync ensures that when
// the startup ctx is canceled before the handler registration finishes syncing,
// the hook surfaces ctx.Err() instead of silently succeeding.
func TestForceSyncFromInformerWithReplace_CtxCanceledBeforeSync(t *testing.T) {
	ResetRegistrations()
	defer ResetRegistrations()

	fakeClientSet := kubefake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	nodeInformer := sharedInformerFactory.Core().V1().Nodes()

	replaceCalled := false
	_, err := ForceSyncFromInformerWithReplace(
		context.TODO().Done(),
		sharedInformerFactory,
		nodeInformer.Informer(),
		cache.ResourceEventHandlerFuncs{},
		func(objs []interface{}) error {
			replaceCalled = true
			return nil
		},
	)
	assert.NoError(t, err)

	// Do NOT start the informer factory, so reg.HasSynced() never becomes true.
	// Use an already-canceled ctx so cache.WaitForCacheSync returns immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = RunAfterPluginInformersSynced(ctx)
	assert.ErrorIs(t, err, context.Canceled, "hook must surface ctx.Err() when cache.WaitForCacheSync fails due to cancellation")
	assert.False(t, replaceCalled, "replaceHandler must not run when the underlying registration never synced")
}

func TestSyncedEventHandlerWithReplace(t *testing.T) {
	ResetRegistrations()

	var objects []runtime.Object
	for i := 0; i < 10; i++ {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				UID:             uuid.NewUUID(),
				Name:            fmt.Sprintf("node-%d", i),
				ResourceVersion: fmt.Sprintf("%d", i+1),
			},
		}
		objects = append(objects, node)
	}
	fakeClientSet := kubefake.NewSimpleClientset(objects...)
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClientSet, 0)
	nodeInformer := sharedInformerFactory.Core().V1().Nodes()
	addTimes := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(10)

	// replaceHandlerCalled is closed once replaceHandler has run.
	replaceHandlerCalled := make(chan struct{})
	var replacedObjs []interface{}
	var replaceMu sync.Mutex

	// ForceSyncFromInformerWithReplace registers handler via the regular path and
	// stashes replaceHandler as an AfterPluginInformersSynced startup hook. The
	// hook is dispatched by RunAfterPluginInformersSynced below, mirroring the
	// production startup sequence in cmd/koord-scheduler/app/server.go.
	ForceSyncFromInformerWithReplace(context.TODO().Done(), sharedInformerFactory, nodeInformer.Informer(),
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				node := obj.(*corev1.Node)
				mu.Lock()
				addTimes[node.Name]++
				mu.Unlock()
				wg.Done()
			},
		},
		func(objs []interface{}) error {
			replaceMu.Lock()
			replacedObjs = objs
			replaceMu.Unlock()
			close(replaceHandlerCalled)
			return nil
		},
	)

	// Start informers so that the registered handler receives initial list events.
	stopCh := make(chan struct{})
	defer close(stopCh)
	sharedInformerFactory.Start(stopCh)

	wg.Wait()

	mu.Lock()
	for _, v := range addTimes {
		if v > 1 {
			mu.Unlock()
			t.Errorf("unexpected add times, want 1 but got %d", v)
			return
		}
	}
	mu.Unlock()

	// Dispatch startup hooks: this is where replaceHandler now runs, in the same
	// phase the scheduler would trigger it in production.
	runCtx, runCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer runCancel()
	if err := RunAfterPluginInformersSynced(runCtx); err != nil {
		t.Fatalf("RunAfterPluginInformersSynced failed: %v", err)
	}

	// replaceHandler must have been called by the time the hook run returns.
	select {
	case <-replaceHandlerCalled:
	default:
		t.Fatal("replaceHandler was not called by RunAfterPluginInformersSynced")
	}

	replaceMu.Lock()
	assert.Equal(t, 10, len(replacedObjs), "replaceHandler should receive all 10 nodes")
	replaceMu.Unlock()

	// WaitForHandlersSync should return quickly now that the initial list has been
	// fully delivered (registrations no longer gate on replaceHandler completion).
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer syncCancel()
	if err := WaitForHandlersSync(syncCtx); err != nil {
		t.Fatalf("WaitForHandlersSync timed out: %v", err)
	}

	sharedInformerFactory.WaitForCacheSync(stopCh)
	node, err := nodeInformer.Lister().Get("node-0")
	assert.NoError(t, err)
	assert.NotNil(t, node)
	node = node.DeepCopy()
	node.ResourceVersion = "100"
	_, err = fakeClientSet.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	assert.NoError(t, err)
	err = wait.PollUntil(1*time.Second, func() (done bool, err error) {
		node, err := nodeInformer.Lister().Get("node-0")
		assert.NoError(t, err)
		assert.NotNil(t, node)
		return node.ResourceVersion == "100", nil
	}, wait.NeverStop)
	assert.NoError(t, err)
}
