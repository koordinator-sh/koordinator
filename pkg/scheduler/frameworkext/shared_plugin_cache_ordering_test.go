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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

// Test_StartSharedCaches_NoEventBeforeStartReturns pins the four-phase initialization order:
// StartSharedCaches registers the unified dispatcher and invokes Start(ctx) BEFORE the shared
// informer factory is started, so no pod/node event can be observed by a cache before its
// Start() has returned. Pre-existing objects are created before startup and delivered only
// once the informer starts, letting us assert Start is the first recorded call.
func Test_StartSharedCaches_NoEventBeforeStartReturns(t *testing.T) {
	factory := newTestFactory(t)
	rec := &orderRecorder{}
	c := newRecordingCache("a", rec)
	factory.getOrRegisterSharedCache("a", nil, func(ExtendedHandle) SharedPluginCache { return c })

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "p1", UID: "p1"}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	client := kubefake.NewSimpleClientset(pod, node)
	informerFactory := informers.NewSharedInformerFactory(client, 0)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	// Phase 2: register dispatcher + Start() (records "a:Start"). Informer not started yet.
	assert.NoError(t, factory.StartSharedCaches(ctx, informerFactory))
	// Phase 3: start the informer so the pre-existing pod/node are delivered as Add events.
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	// Wait until at least one pod/node event has been dispatched to the cache.
	assert.Eventually(t, func() bool {
		for _, e := range rec.snapshot() {
			if e == "a:OnPodAdd:p1" || e == "a:OnNodeAdd:n1" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "expected pod/node events to be dispatched")

	seq := rec.snapshot()
	assert.Equal(t, "a:Start", seq[0], "Start must be the first recorded call")

	startIdx := -1
	for i, e := range seq {
		if e == "a:Start" {
			startIdx = i
			break
		}
	}
	for i, e := range seq {
		if strings.HasPrefix(e, "a:OnPod") || strings.HasPrefix(e, "a:OnNode") {
			assert.Greater(t, i, startIdx, "event %q must be observed after Start() returned", e)
		}
	}
}
