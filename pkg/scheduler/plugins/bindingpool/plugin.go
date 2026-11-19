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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	kubefeatures "k8s.io/kubernetes/pkg/features"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/validation"
)

const Name = "BindingPool"

var _ fwktype.BindPlugin = &Plugin{}

// bindRequest is a Binding waiting for a worker to send to the apiserver.
type bindRequest struct {
	ctx     context.Context
	binding *corev1.Binding
	// result is buffered with capacity 1 so that a worker never blocks when the
	// submitter has already given up.
	result chan error
}

// Plugin replaces DefaultBinder in the bind extension point. It sends Bindings
// through a fixed number of workers, so the requests in flight are bounded by
// the worker count, and the binding cycles beyond that wait in submit.
type Plugin struct {
	client clientset.Interface
	queue  chan *bindRequest
}

func New(ctx context.Context, args runtime.Object, handle fwktype.Handle) (fwktype.Plugin, error) {
	// With SchedulerAsyncAPICalls the scheduler sends API calls through its
	// APIDispatcher, which runs each call in a new goroutine without a limit.
	// Binding through it would bypass the workers, and binding around it would
	// skip its merging of calls for the same pod, so refuse to run with the gate.
	// Check the gate rather than handle.APICacher(), which is set only after the
	// plugins are built.
	if k8sfeature.DefaultFeatureGate.Enabled(kubefeatures.SchedulerAsyncAPICalls) {
		return nil, fmt.Errorf("%s does not support the %s feature gate", Name, kubefeatures.SchedulerAsyncAPICalls)
	}
	pluginArgs, ok := args.(*config.BindingPoolArgs)
	if !ok {
		return nil, fmt.Errorf("want args to be of type BindingPoolArgs, got %T", args)
	}
	if err := validation.ValidateBindingPoolArgs(pluginArgs); err != nil {
		return nil, err
	}

	pl := &Plugin{
		client: handle.ClientSet(),
		queue:  make(chan *bindRequest),
	}
	for i := 0; i < int(pluginArgs.Workers); i++ {
		go pl.worker(ctx)
	}
	return pl, nil
}

func (pl *Plugin) Name() string {
	return Name
}

// Bind blocks until a worker has sent the Binding, so a failure is returned to
// the binding cycle, which forgets the assumed pod and requeues it.
func (pl *Plugin) Bind(ctx context.Context, _ fwktype.CycleState, pod *corev1.Pod, nodeName string) *fwktype.Status {
	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID},
		Target:     corev1.ObjectReference{Kind: "Node", Name: nodeName},
	}
	klog.FromContext(ctx).V(3).Info("Attempting to bind pod to node through the binding pool", "pod", klog.KObj(pod), "node", klog.KRef("", nodeName))
	if err := pl.submit(ctx, binding); err != nil {
		return fwktype.AsStatus(err)
	}
	return nil
}

func (pl *Plugin) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-pl.queue:
			req.result <- pl.client.CoreV1().Pods(req.binding.Namespace).Bind(req.ctx, req.binding, metav1.CreateOptions{})
		}
	}
}

// submit hands the Binding to a worker and waits for its result. It blocks
// while all workers are busy, which pushes back on the binding cycles.
func (pl *Plugin) submit(ctx context.Context, binding *corev1.Binding) error {
	req := &bindRequest{
		ctx:     ctx,
		binding: binding,
		result:  make(chan error, 1),
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case pl.queue <- req:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-req.result:
		return err
	}
}
