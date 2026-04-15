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

	t.Run("default priority pod contributes a fragment", func(t *testing.T) {
		fragments, status := pl.SignPod(context.TODO(), mkPod("p", nil))
		assert.True(t, status == nil || status.IsSuccess())
		assert.Len(t, fragments, 1)
		assert.Equal(t, "koord.LoadAware.priorityClass", fragments[0].Key)
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
}
