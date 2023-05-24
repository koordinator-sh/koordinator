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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func TestErrorHandlerDispatcher(t *testing.T) {
	dispatcher := newErrorHandlerDispatcher()
	enterDefaultHandler := false
	dispatcher.setDefaultHandler(func(info *framework.QueuedPodInfo, err error) {
		enterDefaultHandler = true
	})

	handler1Processed := false
	dispatcher.RegisterErrorHandler(func(info *framework.QueuedPodInfo, err error) bool {
		if info.Pod.Name == "handler1" {
			handler1Processed = true
			return true
		}
		return false
	})

	handler2Processed := false
	dispatcher.RegisterErrorHandler(func(info *framework.QueuedPodInfo, err error) bool {
		if info.Pod.Name == "handler2" {
			handler2Processed = true
			return true
		}
		return false
	})
	dispatcher.Error(&framework.QueuedPodInfo{
		PodInfo: &framework.PodInfo{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "none",
				},
			},
		},
	}, nil)
	assert.True(t, enterDefaultHandler)
	enterDefaultHandler = false

	dispatcher.Error(&framework.QueuedPodInfo{
		PodInfo: &framework.PodInfo{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "handler2",
				},
			},
		},
	}, nil)
	assert.False(t, handler1Processed)
	assert.True(t, handler2Processed)
	assert.False(t, enterDefaultHandler)
	handler2Processed = false

	dispatcher.Error(&framework.QueuedPodInfo{
		PodInfo: &framework.PodInfo{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "handler1",
				},
			},
		},
	}, nil)
	assert.True(t, handler1Processed)
	assert.False(t, handler2Processed)
	assert.False(t, enterDefaultHandler)
}
