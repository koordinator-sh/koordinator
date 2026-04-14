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

package coscheduling

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	kubefake "k8s.io/client-go/kubernetes/fake"

	pgfake "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned/fake"
)

// TestCoscheduling_EventsToRegister_ReturnsNoEvents locks in the invariant
// that the Coscheduling plugin intentionally registers no cluster events.
// Gang gating is handled in PreEnqueue; see the comment on
// Coscheduling.EventsToRegister for the reasoning. If this test fails,
// please update the comment and the referencing RFC before changing the
// behaviour.
func TestCoscheduling_EventsToRegister_ReturnsNoEvents(t *testing.T) {
	pgClientSet := pgfake.NewSimpleClientset()
	cs := kubefake.NewSimpleClientset()
	suit := newPluginTestSuit(t, nil, pgClientSet, cs)

	p, err := suit.proxyNew(context.TODO(), suit.gangSchedulingArgs, suit.Handle)
	assert.NoError(t, err)
	pl := p.(*Coscheduling)

	events, err := pl.EventsToRegister(context.TODO())
	assert.NoError(t, err)
	assert.Nil(t, events, "Coscheduling must not register cluster events; gating lives in PreEnqueue")
}
