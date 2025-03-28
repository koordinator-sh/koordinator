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

package sloconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_DefaultNodeSLOSpecConfig(t *testing.T) {
	expect := slov1alpha1.NodeSLOSpec{
		ResourceUsedThresholdWithBE: DefaultResourceThresholdStrategy(),
		ResourceQOSStrategy:         DefaultResourceQOSStrategy(),
		CPUBurstStrategy:            DefaultCPUBurstStrategy(),
		SystemStrategy:              DefaultSystemStrategy(),
		Extensions:                  DefaultExtensions(),
	}
	got := DefaultNodeSLOSpecConfig()
	assert.Equal(t, expect, got)
}

func Test_NoneResourceQOSStrategy(t *testing.T) {
	expect := &slov1alpha1.ResourceQOSStrategy{
		Policies:    NoneResourceQOSPolicies(),
		LSRClass:    NoneResourceQOS(apiext.QoSLSR),
		LSClass:     NoneResourceQOS(apiext.QoSLS),
		BEClass:     NoneResourceQOS(apiext.QoSBE),
		SystemClass: NoneResourceQOS(apiext.QoSSystem),
		CgroupRoot:  NoneResourceQOS(apiext.QoSNone),
	}
	got := NoneResourceQOSStrategy()
	assert.Equal(t, expect, got)
}
