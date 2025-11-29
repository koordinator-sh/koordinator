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

package util

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientsetfake "k8s.io/client-go/kubernetes/fake"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/helpers"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
)

func Test_EvictPodIfNotEvicted(t *testing.T) {
	testpod := testutil.MockTestPod(apiext.QoSBE, "test_be_pod")

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
			args: args{
				pod:    testpod,
				reason: resourceexecutor.EvictBEPodByNodeMemoryUsage,
			},
			wanted: wants{
				result:           true,
				evictObjectError: false,
				eventReason:      helpers.EvictPodSuccess,
			},
		},
		{
			name: "evict not found",
			args: args{
				pod:    testpod,
				reason: resourceexecutor.EvictBEPodByNodeMemoryUsage,
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

			got := r.EvictPodIfNotEvicted(tt.args.pod, tt.args.reason, "")
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
	r.EvictPodsIfNotEvicted([]*corev1.Pod{pod}, "evict pod first", "")
	getEvictObject, err := client.Tracker().Get(testutil.PodsResource, pod.Namespace, pod.Name)
	assert.NoError(t, err)
	assert.NotNil(t, getEvictObject, "evictPod Fail", err)
	assert.Equal(t, helpers.EvictPodSuccess, fakeRecorder.EventReason, "expect evict success event! but got %s", fakeRecorder.EventReason)

	_, found := r.podsEvicted.Get(string(pod.UID))
	assert.True(t, found, "check PodEvicted cached")

	// evict duplication
	fakeRecorder.EventReason = ""
	r.EvictPodsIfNotEvicted([]*corev1.Pod{pod}, "evict pod duplication", "")
	assert.Equal(t, "", fakeRecorder.EventReason, "check evict duplication, no event send!")
}
