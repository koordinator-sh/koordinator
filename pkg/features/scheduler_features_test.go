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

package features

import (
	"testing"

	"github.com/stretchr/testify/assert"
	k8sfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
)

func TestReservationNominatedNodeNameFeatureGate(t *testing.T) {
	// 1. Verify constant value
	assert.Equal(t, featuregate.Feature("ReservationNominatedNodeName"), ReservationNominatedNodeName)

	// 2. Verify that it is registered in defaultSchedulerFeatureGates
	spec, exists := defaultSchedulerFeatureGates[ReservationNominatedNodeName]
	assert.True(t, exists, "ReservationNominatedNodeName must be registered in defaultSchedulerFeatureGates")
	assert.False(t, spec.Default, "ReservationNominatedNodeName default must be false")
	assert.Equal(t, featuregate.Alpha, spec.PreRelease)

	// 3. Verify it is registered in DefaultFeatureGate and disabled by default
	assert.False(t, k8sfeature.DefaultFeatureGate.Enabled(ReservationNominatedNodeName))

	// 4. Test mutable feature gate behavior
	err := k8sfeature.DefaultMutableFeatureGate.Set("ReservationNominatedNodeName=true")
	assert.NoError(t, err)
	assert.True(t, k8sfeature.DefaultFeatureGate.Enabled(ReservationNominatedNodeName))

	// Reset
	err = k8sfeature.DefaultMutableFeatureGate.Set("ReservationNominatedNodeName=false")
	assert.NoError(t, err)
	assert.False(t, k8sfeature.DefaultFeatureGate.Enabled(ReservationNominatedNodeName))
}
