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

package frameworkext

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

// orderRecorder collects a shared, ordered log of dispatch calls across multiple caches so
// tests can assert the unified dispatcher fans out in registration order.
type orderRecorder struct {
	mu  sync.Mutex
	seq []string
}

func (r *orderRecorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq = append(r.seq, s)
}

func (r *orderRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seq...)
}

func (r *orderRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq = nil
}

// recordingCache is a mock SharedPluginCache that records how many times Start was called
// and appends "<key>:<event>" to a shared orderRecorder for every dispatched event.
type recordingCache struct {
	key string
	rec *orderRecorder

	mu         sync.Mutex
	startCalls int
}

var _ SharedPluginCache = &recordingCache{}

func newRecordingCache(key string, rec *orderRecorder) *recordingCache {
	return &recordingCache{key: key, rec: rec}
}

func (c *recordingCache) starts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startCalls
}

func (c *recordingCache) Start(ctx context.Context) {
	c.mu.Lock()
	c.startCalls++
	c.mu.Unlock()
	c.rec.add(c.key + ":Start")
}

func (c *recordingCache) OnPodAdd(pod *corev1.Pod) { c.rec.add(c.key + ":OnPodAdd:" + pod.Name) }
func (c *recordingCache) OnPodUpdate(oldPod, newPod *corev1.Pod) {
	c.rec.add(c.key + ":OnPodUpdate:" + newPod.Name)
}
func (c *recordingCache) OnPodDelete(pod *corev1.Pod) { c.rec.add(c.key + ":OnPodDelete:" + pod.Name) }
func (c *recordingCache) OnNodeAdd(node *corev1.Node) { c.rec.add(c.key + ":OnNodeAdd:" + node.Name) }
func (c *recordingCache) OnNodeUpdate(oldNode, newNode *corev1.Node) {
	c.rec.add(c.key + ":OnNodeUpdate:" + newNode.Name)
}
func (c *recordingCache) OnNodeDelete(node *corev1.Node) {
	c.rec.add(c.key + ":OnNodeDelete:" + node.Name)
}

func newTestFactory(t *testing.T) *FrameworkExtenderFactory {
	factory, err := NewFrameworkExtenderFactory()
	assert.NoError(t, err)
	return factory
}

func newTestInformerFactory() informers.SharedInformerFactory {
	return informers.NewSharedInformerFactory(kubefake.NewSimpleClientset(), 0)
}

func Test_getOrRegisterSharedCache_SameKeyReturnsSameInstance(t *testing.T) {
	factory := newTestFactory(t)
	rec := &orderRecorder{}

	createCalls := 0
	create := func(ExtendedHandle) SharedPluginCache {
		createCalls++
		return newRecordingCache("deviceshare", rec)
	}

	first := factory.getOrRegisterSharedCache("deviceshare", nil, create)
	second := factory.getOrRegisterSharedCache("deviceshare", nil, create)

	assert.Same(t, first, second, "same key must return the same instance across profiles")
	assert.Equal(t, 1, createCalls, "create must be invoked exactly once for a given key")
}

func Test_getOrRegisterSharedCache_DifferentKeysReturnDistinctInstances(t *testing.T) {
	factory := newTestFactory(t)
	rec := &orderRecorder{}

	a := factory.getOrRegisterSharedCache("a", nil, func(ExtendedHandle) SharedPluginCache {
		return newRecordingCache("a", rec)
	})
	b := factory.getOrRegisterSharedCache("b", nil, func(ExtendedHandle) SharedPluginCache {
		return newRecordingCache("b", rec)
	})

	assert.NotSame(t, a, b, "different keys must return distinct instances")
}

func Test_StartSharedCaches_StartsEachCacheExactlyOnce(t *testing.T) {
	factory := newTestFactory(t)
	rec := &orderRecorder{}
	a := newRecordingCache("a", rec)
	b := newRecordingCache("b", rec)
	factory.getOrRegisterSharedCache("a", nil, func(ExtendedHandle) SharedPluginCache { return a })
	factory.getOrRegisterSharedCache("b", nil, func(ExtendedHandle) SharedPluginCache { return b })

	informerFactory := newTestInformerFactory()
	// Called twice to prove idempotency: the second call is a no-op guarded by
	// sharedCachesStarted, so Start still runs exactly once per cache.
	assert.NoError(t, factory.StartSharedCaches(context.TODO(), informerFactory))
	assert.NoError(t, factory.StartSharedCaches(context.TODO(), informerFactory))

	assert.Equal(t, 1, a.starts())
	assert.Equal(t, 1, b.starts())
}

func Test_StartSharedCaches_NoCachesIsNoOp(t *testing.T) {
	factory := newTestFactory(t)
	// No registered caches — must not panic and must remain a no-op.
	assert.NotPanics(t, func() {
		assert.NoError(t, factory.StartSharedCaches(context.TODO(), newTestInformerFactory()))
	})
}

func Test_dispatch_FanOutInRegistrationOrder(t *testing.T) {
	factory := newTestFactory(t)
	rec := &orderRecorder{}
	// Register in the order a, b, c; dispatch must fan out in that exact order.
	for _, key := range []string{"a", "b", "c"} {
		k := key
		factory.getOrRegisterSharedCache(k, nil, func(ExtendedHandle) SharedPluginCache {
			return newRecordingCache(k, rec)
		})
	}
	// The dispatcher reads the snapshot published by StartSharedCaches; reset the recorder
	// afterwards to drop the Start() entries so we assert only the dispatched events.
	assert.NoError(t, factory.StartSharedCaches(context.TODO(), newTestInformerFactory()))
	rec.reset()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1"}}
	newPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2"}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	newNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n2"}}

	factory.dispatchPodAdd(pod)
	factory.dispatchPodUpdate(pod, newPod)
	factory.dispatchPodDelete(pod)
	factory.dispatchNodeAdd(node)
	factory.dispatchNodeUpdate(node, newNode)
	factory.dispatchNodeDelete(node)

	assert.Equal(t, []string{
		"a:OnPodAdd:p1", "b:OnPodAdd:p1", "c:OnPodAdd:p1",
		"a:OnPodUpdate:p2", "b:OnPodUpdate:p2", "c:OnPodUpdate:p2",
		"a:OnPodDelete:p1", "b:OnPodDelete:p1", "c:OnPodDelete:p1",
		"a:OnNodeAdd:n1", "b:OnNodeAdd:n1", "c:OnNodeAdd:n1",
		"a:OnNodeUpdate:n2", "b:OnNodeUpdate:n2", "c:OnNodeUpdate:n2",
		"a:OnNodeDelete:n1", "b:OnNodeDelete:n1", "c:OnNodeDelete:n1",
	}, rec.snapshot())
}

func Test_dispatch_IgnoresUnexpectedTypesAndUnwrapsTombstones(t *testing.T) {
	factory := newTestFactory(t)
	rec := &orderRecorder{}
	c := newRecordingCache("a", rec)
	factory.getOrRegisterSharedCache("a", nil, func(ExtendedHandle) SharedPluginCache { return c })
	assert.NoError(t, factory.StartSharedCaches(context.TODO(), newTestInformerFactory()))
	rec.reset()

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1"}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}

	// Non-pod / non-node objects are ignored, not dispatched.
	factory.dispatchPodAdd("not-a-pod")
	factory.dispatchNodeAdd(42)
	assert.Empty(t, rec.snapshot(), "unexpected object types must be ignored")

	// DeletedFinalStateUnknown tombstones are unwrapped to the underlying object.
	factory.dispatchPodDelete(cache.DeletedFinalStateUnknown{Key: "default/p1", Obj: pod})
	factory.dispatchNodeDelete(cache.DeletedFinalStateUnknown{Key: "n1", Obj: node})
	assert.Equal(t, []string{"a:OnPodDelete:p1", "a:OnNodeDelete:n1"}, rec.snapshot())
}
