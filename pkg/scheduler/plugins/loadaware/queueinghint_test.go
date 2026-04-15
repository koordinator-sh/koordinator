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

package loadaware

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	v1 "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

func newQueueingHintPlugin(t *testing.T, enableQueueHint bool) *Plugin {
	var v1args v1.LoadAwareSchedulingArgs
	v1.SetDefaults_LoadAwareSchedulingArgs(&v1args)
	var args config.LoadAwareSchedulingArgs
	err := v1.Convert_v1_LoadAwareSchedulingArgs_To_config_LoadAwareSchedulingArgs(&v1args, &args, nil)
	assert.NoError(t, err)
	args.EnableQueueHint = enableQueueHint

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, _ := frameworkext.NewFrameworkExtenderFactory(
		frameworkext.WithKoordinatorClientSet(koordClientSet),
		frameworkext.WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	proxyNew := frameworkext.PluginFactoryProxy(extenderFactory, New)

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newTestSharedLister(nil, nil)
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}
	fh, err := schedulertesting.NewFramework(context.TODO(), registeredPlugins, "koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
	)
	assert.NoError(t, err)

	p, err := proxyNew(context.TODO(), &args, fh)
	assert.NoError(t, err)
	return p.(*Plugin)
}

func TestPlugin_EventsToRegister(t *testing.T) {
	tests := []struct {
		name            string
		enableQueueHint bool
		expectHintFn    bool
	}{
		{"no hint functions when queue hint is disabled", false, false},
		{"hint functions are set when queue hint is enabled", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := newQueueingHintPlugin(t, tt.enableQueueHint)

			events, err := pl.EventsToRegister(context.TODO())
			assert.NoError(t, err)
			assert.Equal(t, 2, len(events))

			expectedGVK := fmt.Sprintf("nodemetrics.%v.%v",
				slov1alpha1.GroupVersion.Version,
				slov1alpha1.GroupVersion.Group)

			var podEv, metricEv *fwktype.ClusterEventWithHint
			for i := range events {
				switch events[i].Event.Resource {
				case fwktype.Pod:
					podEv = &events[i]
				case fwktype.EventResource(expectedGVK):
					metricEv = &events[i]
				}
			}
			assert.NotNil(t, podEv)
			assert.NotNil(t, metricEv)
			assert.Equal(t, fwktype.Delete, podEv.Event.ActionType)
			assert.Equal(t, fwktype.Add|fwktype.Update|fwktype.Delete, metricEv.Event.ActionType)

			if tt.expectHintFn {
				assert.NotNil(t, podEv.QueueingHintFn)
				assert.NotNil(t, metricEv.QueueingHintFn)
			} else {
				assert.Nil(t, podEv.QueueingHintFn)
				assert.Nil(t, metricEv.QueueingHintFn)
			}
		})
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterPodDeletion(t *testing.T) {
	unboundPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "unbound", UID: types.UID("unbound"), Namespace: "default"},
	}
	boundPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "bound", UID: types.UID("bound"), Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
	}
	waiting := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "waiting", UID: types.UID("waiting"), Namespace: "default"},
	}

	tests := []struct {
		name         string
		oldObj       interface{}
		expectedHint fwktype.QueueingHint
	}{
		{"oldObj has the wrong type, fall back to Queue", "not-a-pod", fwktype.Queue},
		{"deleted pod was never bound to a node, skip", unboundPod, fwktype.QueueSkip},
		{"deleted pod was bound to a node and may have freed load, Queue", boundPod, fwktype.Queue},
		{"nil deleted pod is ambiguous, re-queue conservatively", (*corev1.Pod)(nil), fwktype.Queue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := newQueueingHintPlugin(t, true)
			got, err := pl.isSchedulableAfterPodDeletion(klog.Background(), waiting, tt.oldObj, nil)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}

func TestPlugin_QueueingHint_IsSchedulableAfterNodeMetricChange(t *testing.T) {
	metric := &slov1alpha1.NodeMetric{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	waiting := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "waiting"}}

	tests := []struct {
		name         string
		oldObj       interface{}
		newObj       interface{}
		expectedHint fwktype.QueueingHint
	}{
		{"wrong type, fall back to Queue", nil, "not-a-metric", fwktype.Queue},
		{"Add metric, requeue", nil, metric, fwktype.Queue},
		{"Update metric, requeue", metric, metric, fwktype.Queue},
		{"Delete metric cannot lower load, skip", metric, nil, fwktype.QueueSkip},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := newQueueingHintPlugin(t, true)
			got, err := pl.isSchedulableAfterNodeMetricChange(klog.Background(), waiting, tt.oldObj, tt.newObj)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedHint, got)
		})
	}
}
