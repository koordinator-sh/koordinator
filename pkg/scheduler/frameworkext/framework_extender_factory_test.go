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
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	frameworkfake "k8s.io/kubernetes/pkg/scheduler/framework/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"

	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/services"
)

func TestExtenderFactory(t *testing.T) {
	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinformers.NewSharedInformerFactory(koordClientSet, 0)
	factory, err := NewFrameworkExtenderFactory(
		WithServicesEngine(services.NewEngine(gin.New())),
		WithKoordinatorClientSet(koordClientSet),
		WithKoordinatorSharedInformerFactory(koordSharedInformerFactory),
	)
	assert.NoError(t, err)
	assert.NotNil(t, factory)
	assert.Equal(t, koordClientSet, factory.KoordinatorClientSet())
	assert.Equal(t, koordSharedInformerFactory, factory.KoordinatorSharedInformerFactory())

	proxyNew := PluginFactoryProxy(factory, func(args runtime.Object, f framework.Handle) (framework.Plugin, error) {
		return &TestTransformer{index: 1}, nil
	})
	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
	}
	fakeClient := kubefake.NewSimpleClientset()
	sharedInformerFactory := informers.NewSharedInformerFactory(fakeClient, 0)
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		frameworkruntime.WithSnapshotSharedLister(fakeNodeInfoLister{NodeInfoLister: frameworkfake.NodeInfoLister{}}),
		frameworkruntime.WithClientSet(fakeClient),
		frameworkruntime.WithInformerFactory(sharedInformerFactory),
	)
	assert.NoError(t, err)
	pl, err := proxyNew(nil, fh)
	assert.NoError(t, err)
	assert.NotNil(t, pl)
	assert.Equal(t, "TestTransformer", pl.Name())

	extender := factory.GetExtender("koord-scheduler")
	assert.NotNil(t, extender)
	impl := extender.(*frameworkExtenderImpl)
	assert.Len(t, impl.preFilterTransformers, 1)
	assert.Len(t, impl.filterTransformers, 1)
	assert.Len(t, impl.scoreTransformers, 1)
}

func TestCopyQueueInfoToPod(t *testing.T) {
	tests := []struct {
		name             string
		podHasQueueInfo  *corev1.Pod
		podNeedQueueInfo *corev1.Pod
		wantOriginal     *corev1.Pod
		want             *corev1.Pod
	}{
		{
			name: "normal flow",
			podHasQueueInfo: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					ManagedFields: []metav1.ManagedFieldsEntry{
						{Manager: initialTimestampManager, Time: &metav1.Time{}},
						{Manager: attemptsManager, Subresource: strconv.Itoa(1)},
					},
				},
			},
			podNeedQueueInfo: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					ManagedFields: []metav1.ManagedFieldsEntry{
						{Manager: "original manager"},
					},
				},
			},
			wantOriginal: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					ManagedFields: []metav1.ManagedFieldsEntry{
						{Manager: "original manager"},
					},
				},
			},
			want: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					ManagedFields: []metav1.ManagedFieldsEntry{
						{Manager: initialTimestampManager, Time: &metav1.Time{}},
						{Manager: attemptsManager, Subresource: strconv.Itoa(1)},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CopyQueueInfoToPod(tt.podHasQueueInfo, tt.podNeedQueueInfo)
			assert.Equal(t, tt.wantOriginal, tt.podNeedQueueInfo)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRecordPodQueueInfoToPod(t *testing.T) {
	initialTimestamp := time.Now()
	tests := []struct {
		name            string
		originalPod     *corev1.Pod
		podInfo         *framework.QueuedPodInfo
		wantPod         *corev1.Pod
		wantOriginalPod *corev1.Pod
	}{
		{
			name:        "normal flow",
			originalPod: &corev1.Pod{},
			podInfo: &framework.QueuedPodInfo{
				PodInfo:                 &framework.PodInfo{},
				Attempts:                1,
				InitialAttemptTimestamp: &initialTimestamp,
			},
			wantPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					ManagedFields: []metav1.ManagedFieldsEntry{
						{Manager: initialTimestampManager, Time: &metav1.Time{Time: initialTimestamp}},
						{Manager: attemptsManager, Subresource: strconv.Itoa(1)},
					},
				},
			},
			wantOriginalPod: &corev1.Pod{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.podInfo.Pod = tt.originalPod
			RecordPodQueueInfoToPod(tt.podInfo)
			assert.Equal(t, tt.wantOriginalPod, tt.originalPod)
			assert.Equal(t, tt.wantPod, tt.podInfo.PodInfo.Pod)
		})
	}
}

func Test_makePodInfoFromPod(t *testing.T) {
	initialTimestamp := time.Now()
	tests := []struct {
		name    string
		pod     *corev1.Pod
		want    *framework.QueuedPodInfo
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "normal flow",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					ManagedFields: []metav1.ManagedFieldsEntry{
						{Manager: initialTimestampManager, Time: &metav1.Time{Time: initialTimestamp}},
						{Manager: attemptsManager, Subresource: strconv.Itoa(1)},
					},
				},
			},
			want: &framework.QueuedPodInfo{
				PodInfo: &framework.PodInfo{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							ManagedFields: []metav1.ManagedFieldsEntry{
								{Manager: initialTimestampManager, Time: &metav1.Time{Time: initialTimestamp}},
								{Manager: attemptsManager, Subresource: strconv.Itoa(1)},
							},
						},
					},
				},
				InitialAttemptTimestamp: &initialTimestamp,
				Attempts:                1,
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := makePodInfoFromPod(tt.pod)
			if !tt.wantErr(t, err, fmt.Sprintf("makePodInfoFromPod(%v)", tt.pod)) {
				return
			}
			assert.Equalf(t, tt.want, got, "makePodInfoFromPod(%v)", tt.pod)
		})
	}
}
