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

package nodemetric

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func Test_isNodeAllocatableUpdated(t *testing.T) {
	assert := assert.New(t)
	newNode := &corev1.Node{}
	oldNode := &corev1.Node{}
	assert.Equal(false, isNodeAllocatableUpdated(nil, oldNode))
	assert.Equal(false, isNodeAllocatableUpdated(newNode, nil))
	assert.Equal(false, isNodeAllocatableUpdated(nil, nil))
	assert.Equal(false, isNodeAllocatableUpdated(newNode, oldNode))
	newNode.Status.Allocatable = corev1.ResourceList{}
	newNode.Status.Allocatable["test"] = resource.Quantity{}
	assert.Equal(true, isNodeAllocatableUpdated(newNode, oldNode))
}
