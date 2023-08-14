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

package helper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/scheduler"
)

func TestSharedInformerFactory(t *testing.T) {
	client := kubefake.NewSimpleClientset()
	factory := scheduler.NewInformerFactory(client, 0)
	factory = NewForceSyncSharedInformerFactory(factory)
	assert.NotNil(t, factory.Storage())

	informer := factory.Core().V1().Nodes().Informer()
	assert.False(t, informer.HasSynced())
	_, ok := informer.(*forceSyncsharedIndexInformer)
	assert.True(t, ok)

	informer.AddEventHandler(&forceSyncEventHandler{})
	assert.False(t, informer.HasSynced())

	informer.AddEventHandler(&cache.ResourceEventHandlerFuncs{})
	assert.True(t, informer.HasSynced())
}
