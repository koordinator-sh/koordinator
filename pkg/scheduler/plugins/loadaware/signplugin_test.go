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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	fwktype "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing/framework"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	v1 "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
)

var _ fwktype.SignPlugin = &Plugin{}

func newSignPluginForTest(t *testing.T) *Plugin {
	var v1args v1.LoadAwareSchedulingArgs
	v1.SetDefaults_LoadAwareSchedulingArgs(&v1args)
	var args config.LoadAwareSchedulingArgs
	err := v1.Convert_v1_LoadAwareSchedulingArgs_To_config_LoadAwareSchedulingArgs(&v1args, &args, nil)
	require.NoError(t, err)

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
	registered := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}
	fh, err := schedulertesting.NewFramework(context.TODO(), registered, "koord-scheduler",
		frameworkruntime.WithClientSet(cs),
		frameworkruntime.WithInformerFactory(informerFactory),
		frameworkruntime.WithSnapshotSharedLister(snapshot),
	)
	require.NoError(t, err)

	p, err := proxyNew(context.TODO(), &args, fh)
	require.NoError(t, err)
	return p.(*Plugin)
}

func TestPlugin_SignPod(t *testing.T) {
	pl := newSignPluginForTest(t)

	mkPod := func(name string, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name),
				Labels: labels,
			},
		}
	}

	findFragment := func(fragments []fwktype.SignFragment, key string) any {
		for _, f := range fragments {
			if f.Key == key {
				return f.Value
			}
		}
		return nil
	}

	t.Run("default priority pod contributes priority + daemonset fragments", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", nil))
		assert.True(t, status == nil || status.IsSuccess())
		assert.NotNil(t, findFragment(fragments, "koord.LoadAware.priorityClass"))
		assert.Equal(t, false, findFragment(fragments, "koord.LoadAware.daemonSetOwned"))
	})

	mkPodWithLimits := func(name, cpuReq, memReq, cpuLim, memLim string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name)},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpuReq),
						corev1.ResourceMemory: resource.MustParse(memReq),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpuLim),
						corev1.ResourceMemory: resource.MustParse(memLim),
					},
				},
			}}},
		}
	}

	t.Run("different limits yield different signatures (EstimatePod input)", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPodWithLimits("small", "1", "1Gi", "2", "2Gi"))
		fb, _ := pl.SignPod(context.TODO(), mkPodWithLimits("large", "1", "1Gi", "8", "16Gi"))
		assert.NotEqual(t, fa, fb,
			"DefaultEstimator.EstimatePod uses resourceapi.PodLimits when limit>request; upstream fit does not sign limits so this plugin must")
	})

	t.Run("identical limits share the limits fragment", func(t *testing.T) {
		fa, _ := pl.SignPod(context.TODO(), mkPodWithLimits("a", "2", "4Gi", "4", "8Gi"))
		fb, _ := pl.SignPod(context.TODO(), mkPodWithLimits("b", "2", "4Gi", "4", "8Gi"))
		assert.Equal(t, fa, fb)
	})

	t.Run("same-limits pods with different requests share this plugin's signature", func(t *testing.T) {
		// Upstream noderesources/fit.SignPod already signs requests, so this
		// plugin must not double-sign; two pods with identical limits but
		// different requests should appear identical through koord.LoadAware
		// alone (fit will still differentiate them in the overall signature).
		fa, _ := pl.SignPod(context.TODO(), mkPodWithLimits("r1", "1", "1Gi", "4", "8Gi"))
		fb, _ := pl.SignPod(context.TODO(), mkPodWithLimits("r2", "2", "2Gi", "4", "8Gi"))
		assert.Equal(t, fa, fb)
	})

	t.Run("custom estimated scaling factors annotation produces a different signature", func(t *testing.T) {
		plain := mkPod("plain", nil)
		withFactors := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "with-factors", Namespace: "default", UID: "with-factors",
				Annotations: map[string]string{
					apiext.AnnotationCustomEstimatedScalingFactors: `{"cpu":80}`,
				},
			},
		}
		fa, _ := pl.SignPod(context.TODO(), plain)
		fb, _ := pl.SignPod(context.TODO(), withFactors)
		assert.NotEqual(t, fa, fb,
			"custom scaling factors override EstimatePod output; different factors must not share a signature")
	})

	t.Run("prod-priority pod differs from default-priority pod", func(t *testing.T) {
		prod := mkPod("prod", map[string]string{apiext.LabelPodPriorityClass: string(apiext.PriorityProd)})
		plain := mkPod("plain", nil)
		fa, _ := pl.SignPod(context.TODO(), prod)
		fb, _ := pl.SignPod(context.TODO(), plain)
		assert.NotEqual(t, fa, fb)
	})

	t.Run("two prod pods match", func(t *testing.T) {
		labels := map[string]string{apiext.LabelPodPriorityClass: string(apiext.PriorityProd)}
		fa, _ := pl.SignPod(context.TODO(), mkPod("a", labels))
		fb, _ := pl.SignPod(context.TODO(), mkPod("b", labels))
		assert.Equal(t, fa, fb)
	})

	t.Run("DaemonSet ownership produces a different signature", func(t *testing.T) {
		plain := mkPod("plain", nil)
		ds := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ds", Namespace: "default", UID: "ds",
				OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "logs", APIVersion: "apps/v1"}},
			},
		}
		fa, _ := pl.SignPod(context.TODO(), plain)
		fb, _ := pl.SignPod(context.TODO(), ds)
		assert.NotEqual(t, fa, fb, "Filter short-circuits on DaemonSet pods, so the signature must differ")
	})

	t.Run("two DaemonSet pods at the same priority share a signature", func(t *testing.T) {
		mkDS := func(name string) *corev1.Pod {
			return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "default", UID: types.UID(name),
				OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "logs", APIVersion: "apps/v1"}},
			}}
		}
		fa, _ := pl.SignPod(context.TODO(), mkDS("ds-a"))
		fb, _ := pl.SignPod(context.TODO(), mkDS("ds-b"))
		assert.Equal(t, fa, fb)
	})
}
