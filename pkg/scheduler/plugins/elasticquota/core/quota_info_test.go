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

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	schetesting "k8s.io/kubernetes/pkg/scheduler/testing"
)

func TestQuotaInfo_AddPodIfNotPresent_RemovePodIfPresent_GetPodCache(t *testing.T) {
	qi := NewQuotaInfo(false, true, "qi1", "root")
	pod := schetesting.MakePod().Name("test").Obj()
	qi.AddPodIfNotPresent(pod)
	assert.Equal(t, 1, len(qi.GetPodCache()))
	qi.AddPodIfNotPresent(pod)
	assert.False(t, qi.GetPodIsAssigned(pod))
	err := qi.UpdatePodIsAssigned(pod.Name, false)
	assert.NotNil(t, err)
	err = qi.UpdatePodIsAssigned(pod.Name, true)
	assert.Nil(t, err)
	assert.True(t, qi.GetPodIsAssigned(pod))

	qi.RemovePodIfPreSent(pod.Name)
	assert.Equal(t, 0, len(qi.GetPodCache()))
	qi.RemovePodIfPreSent(pod.Name)
}

func TestQuotaInfo_DeepCopy(t *testing.T) {
	var qi *QuotaInfo
	copyObj := qi.DeepCopy()
	assert.Nil(t, copyObj)
	qi = &QuotaInfo{
		Name:              "test",
		ParentName:        "root",
		IsParent:          false,
		RuntimeVersion:    10,
		AllowLentResource: true,
		PodCache: map[string]*PodInfo{
			"testPod": {
				pod:        schetesting.MakePod().Name("testPod").Obj(),
				isAssigned: true,
				resource:   createResourceList(10, 10),
			},
		},
		CalculateInfo: QuotaCalculateInfo{
			Max:          createResourceList(20, 20),
			AutoScaleMin: createResourceList(10, 23),
			OriginalMin:  createResourceList(20, 10),
			Used:         createResourceList(20, 14),
			Request:      createResourceList(31, 40),
			SharedWeight: createResourceList(32, 40),
			Runtime:      createResourceList(3, 4),
		},
	}
	copyObj = qi.DeepCopy()
	assert.Equal(t, copyObj, qi)
	assert.Equal(t, createResourceList(31, 40), copyObj.GetRequest())
	assert.Equal(t, createResourceList(3, 4), copyObj.GetRuntime())
}
