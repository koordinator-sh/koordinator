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

package framework

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

func Test_EvictPodIfNotEvicted(t *testing.T) {
	testpod := testutil.MockTestPod(apiext.QoSBE, "test_be_pod")
	testnode := testutil.MockTestNode("80", "120G")

	type args struct {
		pod    *corev1.Pod
		node   *corev1.Node
		reason string
	}
	type wants struct {
		result           bool
		evictObjectError bool
		eventReason      string
	}

	tests := []struct {
		name   string
		pod    *corev1.Pod
		node   *corev1.Node
		args   args
		wanted wants
	}{
		{
			name: "evict ok",
			pod:  testpod,
			node: testnode,
			args: args{
				pod:    testpod,
				node:   testnode,
				reason: resourceexecutor.EvictPodByNodeMemoryUsage,
			},
			wanted: wants{
				result:           true,
				evictObjectError: false,
				eventReason:      helpers.EvictPodSuccess,
			},
		},
		{
			name: "evict not found",
			node: testnode,
			args: args{
				pod:    testpod,
				node:   testnode,
				reason: resourceexecutor.EvictPodByNodeMemoryUsage,
			},
			wanted: wants{
				result:           false,
				evictObjectError: true,
				eventReason:      helpers.EvictPodFail,
			},
		},
		// TODO add test for evict failed by forbidden
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// prepare
			ctl := gomock.NewController(t)
			defer ctl.Finish()
			fakeRecorder := &testutil.FakeRecorder{}
			client := clientsetfake.NewSimpleClientset()
			r := NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
			stop := make(chan struct{})
			err := r.podsEvicted.Run(stop)
			assert.NoError(t, err)
			defer func() { stop <- struct{}{} }()

			if tt.pod != nil {
				_, err = client.CoreV1().Pods(tt.pod.Namespace).Create(context.TODO(), tt.pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			got := r.EvictPodIfNotEvicted(tt.args.pod, tt.args.node, tt.args.reason, "")
			assert.Equal(t, tt.wanted.result, got)

			getEvictObject, err := client.Tracker().Get(testutil.PodsResource, tt.args.pod.Namespace, tt.args.pod.Name)
			if tt.wanted.evictObjectError {
				assert.Error(t, err)
				assert.Nil(t, getEvictObject)
				assert.Equal(t, tt.wanted.eventReason, fakeRecorder.EventReason, "expect evict failed event! but got %s", fakeRecorder.EventReason)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, getEvictObject)
				assert.Equal(t, tt.wanted.eventReason, fakeRecorder.EventReason, "expect evict success event! but got %s", fakeRecorder.EventReason)
			}
		})
	}
}

func Test_EvictPodsIfNotEvicted(t *testing.T) {
	// test data
	pod := testutil.MockTestPod(apiext.QoSBE, "test_be_pod")
	node := testutil.MockTestNode("80", "120G")
	// env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	fakeRecorder := &testutil.FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()
	r := NewEvictor(client, fakeRecorder, policyv1beta1.SchemeGroupVersion.Version)
	stop := make(chan struct{})
	err := r.podsEvicted.Run(stop)
	assert.NoError(t, err)
	defer func() { stop <- struct{}{} }()

	// create pod
	_, err = client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)

	// evict success
	r.EvictPodsIfNotEvicted([]*corev1.Pod{pod}, node, "evict pod first", "")
	getEvictObject, err := client.Tracker().Get(testutil.PodsResource, pod.Namespace, pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, getEvictObject, "evictPod Fail", err)
	assert.Equal(t, helpers.EvictPodSuccess, fakeRecorder.EventReason, "expect evict success event! but got %s", fakeRecorder.EventReason)

	_, found := r.podsEvicted.Get(string(pod.UID))
	assert.True(t, found, "check PodEvicted cached")

	// evict duplication
	fakeRecorder.EventReason = ""
	r.EvictPodsIfNotEvicted([]*corev1.Pod{pod}, node, "evict pod duplication", "")
	assert.Equal(t, "", fakeRecorder.EventReason, "check evict duplication, no event send!")
}

func setupFakeDiscoveryWithPolicyResource(fake *coretesting.Fake, groupVersion string) {
	fake.AddReactor("get", "group", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
		fake.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: groupVersion,
				APIResources: []metav1.APIResource{
					{
						Name: util.EvictionSubResourceName,
						Kind: util.EvictionKind,
					},
				},
			},
		}
		return true, nil, nil
	})
	fake.AddReactor("get", "resource", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
		fake.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "v1",
				APIResources: []metav1.APIResource{
					{
						Name:    util.EvictionSubResourceName,
						Kind:    util.EvictionKind,
						Group:   util.EvictionGroupName,
						Version: "v1",
					},
				},
			},
		}
		return true, nil, nil
	})
}

func Test_evictPod_policy_v1beta1(t *testing.T) {
	// test data
	pod := testutil.MockTestPod(apiext.QoSBE, "test_be_pod")
	node := testutil.MockTestNode("80", "120G")
	// env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
	mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas([]*corev1.Pod{pod})).AnyTimes()
	mockStatesInformer.EXPECT().GetNode().Return(node).AnyTimes()

	fakeRecorder := &testutil.FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()
	setupFakeDiscoveryWithPolicyResource(&client.Fake, policyv1beta1.SchemeGroupVersion.String())
	evictVersion, err := util.FindSupportedEvictVersion(client)
	assert.Nil(t, err)

	r := NewEvictor(client, fakeRecorder, evictVersion)

	// create pod
	_, err = client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)
	// check pod
	existPod, err := client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
	assert.NotNil(t, existPod, "pod exist in k8s!", err)

	// evict success
	r.evictPod(pod, "evict pod first", "")
	getEvictObject, err := client.Tracker().Get(testutil.PodsResource, pod.Namespace, pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, getEvictObject, "evictPod Fail", err)

	assert.Equal(t, helpers.EvictPodSuccess, fakeRecorder.EventReason, "expect evict success event! but got %s", fakeRecorder.EventReason)
}

func Test_evictPod_policy_v1(t *testing.T) {
	// test data
	pod := testutil.MockTestPod(apiext.QoSBE, "test_be_pod")
	node := testutil.MockTestNode("80", "120G")
	// env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
	mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas([]*corev1.Pod{pod})).AnyTimes()
	mockStatesInformer.EXPECT().GetNode().Return(node).AnyTimes()

	fakeRecorder := &testutil.FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()
	setupFakeDiscoveryWithPolicyResource(&client.Fake, policyv1.SchemeGroupVersion.String())
	evictVersion, err := util.FindSupportedEvictVersion(client)
	assert.Nil(t, err)

	r := NewEvictor(client, fakeRecorder, evictVersion)

	// create pod
	_, err = client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)
	// check pod
	existPod, err := client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
	assert.NotNil(t, existPod, "pod exist in k8s!", err)

	// evict success
	r.evictPod(pod, "evict pod first", "")
	getEvictObject, err := client.Tracker().Get(testutil.PodsResource, pod.Namespace, pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, getEvictObject, "evictPod Fail", err)

	assert.Equal(t, helpers.EvictPodSuccess, fakeRecorder.EventReason, "expect evict success event! but got %s", fakeRecorder.EventReason)
}

func Test_evictPod_policy_none(t *testing.T) {
	// test data
	pod := testutil.MockTestPod(apiext.QoSBE, "test_be_pod")
	node := testutil.MockTestNode("80", "120G")
	// env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
	mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas([]*corev1.Pod{pod})).AnyTimes()
	mockStatesInformer.EXPECT().GetNode().Return(node).AnyTimes()

	fakeRecorder := &testutil.FakeRecorder{}
	client := clientsetfake.NewSimpleClientset()

	r := NewEvictor(client, fakeRecorder, "")

	// create pod
	_, err := client.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	assert.NoError(t, err)
	// check pod
	existPod, err := client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
	assert.NotNil(t, existPod, "pod exist in k8s!", err)

	// evict success
	evicted := r.evictPod(pod, "evict pod first", "")
	assert.False(t, evicted, "pod evicted", err)
}
