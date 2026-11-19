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

package bindingpool

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
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	clienttesting "k8s.io/client-go/testing"
	fwktype "k8s.io/kube-scheduler/framework"
	kubefeatures "k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	utilfeature "github.com/koordinator-sh/koordinator/pkg/util/feature"
)

func newTestHandle(t *testing.T, cs clientset.Interface) fwktype.Handle {
	t.Helper()
	fh, err := schedulertesting.NewFramework(
		context.Background(),
		[]schedulertesting.RegisterPluginFunc{
			schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
			schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		},
		"koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informers.NewSharedInformerFactory(cs, 0)),
	)
	if err != nil {
		t.Fatalf("failed to build the test framework: %v", err)
	}
	return fh
}

func newTestPlugin(t *testing.T, cs clientset.Interface, workers int32) *Plugin {
	t.Helper()
	p, err := New(t.Context(), &config.BindingPoolArgs{Workers: workers}, newTestHandle(t, cs))
	if err != nil {
		t.Fatalf("failed to build the plugin: %v", err)
	}
	return p.(*Plugin)
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		args    runtime.Object
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid args",
			args: &config.BindingPoolArgs{Workers: 8},
		},
		{
			name:    "wrong args type",
			args:    &config.DefaultPreBindArgs{},
			wantErr: true,
			errMsg:  "want args to be of type BindingPoolArgs",
		},
		{
			name:    "invalid args - workers is zero",
			args:    &config.BindingPoolArgs{Workers: 0},
			wantErr: true,
			errMsg:  "workers",
		},
		{
			name:    "invalid args - workers is negative",
			args:    &config.BindingPoolArgs{Workers: -1},
			wantErr: true,
			errMsg:  "workers",
		},
	}
	fh := newTestHandle(t, kubefake.NewSimpleClientset())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(t.Context(), tt.args, fh)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, p)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, p)
			assert.Equal(t, Name, p.Name())
		})
	}
}

func TestNewRejectsSchedulerAsyncAPICalls(t *testing.T) {
	defer utilfeature.SetFeatureGateDuringTest(t, k8sfeature.DefaultMutableFeatureGate, kubefeatures.SchedulerAsyncAPICalls, true)()

	// The gate is checked before the handle is used, so no framework is needed.
	p, err := New(context.Background(), &config.BindingPoolArgs{Workers: 8}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), string(kubefeatures.SchedulerAsyncAPICalls))
	assert.Nil(t, p)
}

func TestBind(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "test-pod", UID: "test-uid"},
	}

	t.Run("sends the Binding", func(t *testing.T) {
		// The fake tracker resolves a Binding against the pod it names, so the pod
		// has to exist. Adding it up front keeps it out of the recorded actions.
		cs := kubefake.NewSimpleClientset(pod.DeepCopy())
		p := newTestPlugin(t, cs, 1)

		status := p.Bind(context.Background(), nil, pod, "test-node")
		assert.Nil(t, status)

		actions := cs.Actions()
		assert.Len(t, actions, 1)
		action := actions[0]
		assert.Equal(t, "create", action.GetVerb())
		assert.Equal(t, "pods", action.GetResource().Resource)
		assert.Equal(t, "binding", action.GetSubresource())

		binding := action.(clienttesting.CreateAction).GetObject().(*corev1.Binding)
		assert.Equal(t, pod.Namespace, binding.Namespace)
		assert.Equal(t, pod.Name, binding.Name)
		assert.Equal(t, pod.UID, binding.UID)
		assert.Equal(t, corev1.ObjectReference{Kind: "Node", Name: "test-node"}, binding.Target)
	})

	t.Run("returns the failure to the binding cycle", func(t *testing.T) {
		bindErr := errors.New("apiserver rejected the binding")
		cs := kubefake.NewSimpleClientset(pod.DeepCopy())
		cs.PrependReactor("create", "pods", func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "binding" {
				return false, nil, nil
			}
			return true, nil, bindErr
		})
		p := newTestPlugin(t, cs, 1)

		status := p.Bind(context.Background(), nil, pod, "test-node")
		assert.NotNil(t, status)
		assert.ErrorIs(t, status.AsError(), bindErr)
	})
}

func TestBindHonorsCanceledContext(t *testing.T) {
	cs := kubefake.NewSimpleClientset()
	p := newTestPlugin(t, cs, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	status := p.Bind(ctx, nil, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "test-pod"}}, "test-node")
	assert.NotNil(t, status)
	assert.ErrorIs(t, status.AsError(), context.Canceled)
}

// TestBindLimitsBindingsInFlight checks the property the plugin exists for: the
// Binding requests in flight never exceed the worker count, however many binding
// cycles are waiting.
func TestBindLimitsBindingsInFlight(t *testing.T) {
	const (
		workers = 3
		callers = 10
	)

	pods := &blockingPods{release: make(chan struct{})}
	p := newTestPlugin(t, &blockingClientset{Interface: kubefake.NewSimpleClientset(), pods: pods}, workers)

	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: fmt.Sprintf("test-pod-%d", i)}}
			status := p.Bind(context.Background(), nil, pod, "test-node")
			assert.Nil(t, status)
		}(i)
	}

	assert.Eventually(t, func() bool {
		return pods.maxInFlight() == workers
	}, 10*time.Second, 10*time.Millisecond, "the workers should all be busy")

	// Give the remaining callers a chance to leak past the workers.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, workers, pods.maxInFlight())

	close(pods.release)
	wg.Wait()
}

// blockingPods blocks every Bind until release is closed and records how many
// run at once. The fake clientset cannot measure this: its Invokes holds a lock
// for the whole reactor call, so reactors never run concurrently.
type blockingPods struct {
	release chan struct{}

	mu       sync.Mutex
	inFlight int
	max      int
}

func (b *blockingPods) bind(ctx context.Context) error {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.max {
		b.max = b.inFlight
	}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.inFlight--
		b.mu.Unlock()
	}()

	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *blockingPods) maxInFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.max
}

// blockingClientset replaces Bind on top of a real clientset so the rest of the
// client behaviour stays intact.
type blockingClientset struct {
	clientset.Interface
	pods *blockingPods
}

func (c *blockingClientset) CoreV1() typedcorev1.CoreV1Interface {
	return &blockingCoreV1{CoreV1Interface: c.Interface.CoreV1(), pods: c.pods}
}

type blockingCoreV1 struct {
	typedcorev1.CoreV1Interface
	pods *blockingPods
}

func (c *blockingCoreV1) Pods(namespace string) typedcorev1.PodInterface {
	return &blockingPodsInterface{PodInterface: c.CoreV1Interface.Pods(namespace), pods: c.pods}
}

type blockingPodsInterface struct {
	typedcorev1.PodInterface
	pods *blockingPods
}

func (p *blockingPodsInterface) Bind(ctx context.Context, _ *corev1.Binding, _ metav1.CreateOptions) error {
	return p.pods.bind(ctx)
}
