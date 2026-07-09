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
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

// Test_frameworkExtenderImpl_RunPreFilterPlugins_Tracing verifies that RunPreFilterPlugins
// emits an OpenTelemetry span for the PreFilter transformer phase, carrying the scheduled
// pod's identity as span attributes, when a tracer provider is configured.
func Test_frameworkExtenderImpl_RunPreFilterPlugins_Tracing(t *testing.T) {
	// Install an in-memory tracer provider to capture emitted spans, and restore the
	// previous global provider afterwards so other tests are unaffected.
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(previous)

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)
	extenderFactory, err := NewFrameworkExtenderFactory(
		WithServicesEngine(services.NewEngine(gin.New())),
		WithKoordinatorClientSet(koordClientSet),
		WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	assert.NoError(t, err)

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterPreFilterPlugin("T1", PluginFactoryProxy(extenderFactory, func(_ context.Context, _ runtime.Object, _ fwktype.Handle) (fwktype.Plugin, error) {
			return &TestTransformer{name: "T1", index: 1}, nil
		})),
	}
	fakeClient := kubefake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{nodeInfoLister: nodeInfoLister{}}),
		frameworkruntime.WithClientSet(fakeClient),
		frameworkruntime.WithInformerFactory(sharedInformerFactory),
	)
	assert.NoError(t, err)
	frameworkExtender := extenderFactory.NewFrameworkExtender(fh)
	frameworkExtender.SetConfiguredPlugins(fh.ListPlugins())

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "test-pod"}}
	_, status, _ := frameworkExtender.RunPreFilterPlugins(context.TODO(), framework.NewCycleState(), pod)
	assert.Nil(t, status)

	// A span for the PreFilter transformer phase should have been recorded, carrying
	// the pod namespace and name as attributes.
	var found sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.Name() == "RunPreFilterPluginTransformers" {
			found = s
			break
		}
	}
	if assert.NotNil(t, found, "expected a RunPreFilterPluginTransformers span to be recorded") {
		attrs := map[string]string{}
		for _, kv := range found.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		assert.Equal(t, "test-ns", attrs["pod.namespace"])
		assert.Equal(t, "test-pod", attrs["pod.name"])
	}
}
