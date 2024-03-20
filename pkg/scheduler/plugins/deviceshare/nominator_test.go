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

package deviceshare

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
)

func TestNominator(t *testing.T) {
	nominator := NewNominator()
	assert.Equal(t, 0, len(nominator.nominateMap))

	pod := &corev1.Pod{}
	pod.Namespace = "test"
	pod.Name = "job1"

	used := make(map[v1alpha1.DeviceType]deviceResources)
	used[v1alpha1.GPU] = make(deviceResources)
	used[v1alpha1.GPU][0] = corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("10"),
	}

	nominator.AddPod(pod, used)
	assert.Equal(t, 1, len(nominator.nominateMap))
	used = nominator.GetPodAllocated(pod)
	usedCPU := used[v1alpha1.GPU][0][corev1.ResourceCPU]
	assert.Equal(t, usedCPU.String(), "10")
	assert.Equal(t, true, nominator.IsPodExist(pod))

	nominator.RemovePod(pod)
	assert.Equal(t, 0, len(nominator.nominateMap))
}
