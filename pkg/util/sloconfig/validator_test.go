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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_GetInstance(t *testing.T) {
	strategy := &slov1alpha1.ResourceThresholdStrategy{
		Enable:                             ptr.To[bool](true),
		CPUSuppressThresholdPercent:        ptr.To[int64](-1),
		CPUEvictBESatisfactionUpperPercent: ptr.To[int64](70),
		CPUEvictBESatisfactionLowerPercent: ptr.To[int64](70),
	}
	info, err := GetValidatorInstance().StructWithTrans(strategy)
	fmt.Println(info)
	assert.True(t, info["ResourceThresholdStrategy.CPUEvictBESatisfactionUpperPercent"] != "", info["ResourceThresholdStrategy.CPUEvictBESatisfactionUpperPercent"])
	assert.True(t, info["ResourceThresholdStrategy.CPUEvictBESatisfactionLowerPercent"] != "", info["ResourceThresholdStrategy.CPUEvictBESatisfactionLowerPercent"])
	assert.True(t, info["ResourceThresholdStrategy.CPUSuppressThresholdPercent"] != "", info["ResourceThresholdStrategy.CPUSuppressThresholdPercent"])
	assert.NoError(t, err)
}
