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
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// noopSharedCache is a zero-work SharedPluginCache used to isolate the dispatcher's own cost
// (atomic snapshot load + fan-out) from any per-cache handler work.
type noopSharedCache struct{}

var _ SharedPluginCache = noopSharedCache{}

func (noopSharedCache) Start(context.Context)          {}
func (noopSharedCache) OnPodAdd(*corev1.Pod)           {}
func (noopSharedCache) OnPodUpdate(_, _ *corev1.Pod)   {}
func (noopSharedCache) OnPodDelete(*corev1.Pod)        {}
func (noopSharedCache) OnNodeAdd(*corev1.Node)         {}
func (noopSharedCache) OnNodeUpdate(_, _ *corev1.Node) {}
func (noopSharedCache) OnNodeDelete(*corev1.Node)      {}

func benchDispatchFactory(b *testing.B, numCaches int) *FrameworkExtenderFactory {
	factory, err := NewFrameworkExtenderFactory()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < numCaches; i++ {
		factory.getOrRegisterSharedCache(fmt.Sprintf("c%d", i), nil,
			func(ExtendedHandle) SharedPluginCache { return noopSharedCache{} })
	}
	if err := factory.StartSharedCaches(context.TODO(), newTestInformerFactory()); err != nil {
		b.Fatal(err)
	}
	return factory
}

// BenchmarkDispatchPodAdd measures the per-event dispatch cost (lock-free snapshot load +
// fan-out to every registered cache) that replaced the per-event mutex + slice allocation.
func BenchmarkDispatchPodAdd(b *testing.B) {
	factory := benchDispatchFactory(b, 3)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		factory.dispatchPodAdd(pod)
	}
}

// BenchmarkDispatchPodAddParallel confirms the dispatch read path scales under concurrency:
// dispatchCaches() is an atomic load with no lock, so concurrent events do not contend.
func BenchmarkDispatchPodAddParallel(b *testing.B) {
	factory := benchDispatchFactory(b, 3)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1"}}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			factory.dispatchPodAdd(pod)
		}
	})
}
