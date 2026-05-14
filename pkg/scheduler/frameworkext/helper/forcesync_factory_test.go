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
	// Reset registrations to avoid interference from other tests.
	ResetRegistrations()

	client := kubefake.NewSimpleClientset()
	factory := scheduler.NewInformerFactory(client, 0)
	factory = NewForceSyncSharedInformerFactory(factory)
	assert.NotNil(t, factory.Storage())

	informer := factory.Core().V1().Nodes().Informer()
	_, ok := informer.(*forceSyncsharedIndexInformer)
	assert.True(t, ok, "expected forceSyncsharedIndexInformer wrapper")

	// AddEventHandler via the wrapper should collect the registration.
	informer.AddEventHandler(&cache.ResourceEventHandlerFuncs{})
	regs := GetRegistrations()
	assert.Len(t, regs, 1, "expected 1 registration after AddEventHandler")

	// A second AddEventHandler should add another registration.
	informer.AddEventHandler(&cache.ResourceEventHandlerFuncs{})
	regs = GetRegistrations()
	assert.Len(t, regs, 2, "expected 2 registrations after second AddEventHandler")
}

// TestForceSyncFromInformerNoDuplicateRegistration verifies that calling ForceSyncFromInformer
// with a forceSyncsharedIndexInformer-wrapped informer does NOT double-count the registration.
// The wrapper's AddEventHandlerWithResyncPeriod already calls addRegistration internally;
// ForceSyncFromInformer must skip its own addRegistration in that case.
func TestForceSyncFromInformerNoDuplicateRegistration(t *testing.T) {
	// Reset registrations to avoid interference from other tests.
	ResetRegistrations()

	client := kubefake.NewSimpleClientset()
	factory := scheduler.NewInformerFactory(client, 0)
	wrappedFactory := NewForceSyncSharedInformerFactory(factory)

	wrapperInformer := wrappedFactory.Core().V1().Nodes().Informer()
	_, ok := wrapperInformer.(*forceSyncsharedIndexInformer)
	assert.True(t, ok, "expected forceSyncsharedIndexInformer wrapper")

	// Calling ForceSyncFromInformer on a wrapper informer should result in exactly
	// one registration, not two.
	ForceSyncFromInformer(nil, nil, wrapperInformer, &cache.ResourceEventHandlerFuncs{})
	regs := GetRegistrations()
	assert.Len(t, regs, 1, "ForceSyncFromInformer on wrapper informer must not double-count registration")

	// Calling ForceSyncFromInformer again adds exactly one more (not two more).
	ForceSyncFromInformer(nil, nil, wrapperInformer, &cache.ResourceEventHandlerFuncs{})
	regs = GetRegistrations()
	assert.Len(t, regs, 2, "second ForceSyncFromInformer on wrapper informer should add exactly one registration")
}
