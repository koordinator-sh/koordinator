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

package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetDefaults_ReservationArgs(t *testing.T) {
	obj := &ReservationArgs{}
	SetDefaults_ReservationArgs(obj)
	assert.NotNil(t, obj.EnableAsyncPreemption)
	assert.True(t, *obj.EnableAsyncPreemption)

	enableAsync := false
	obj2 := &ReservationArgs{
		EnableAsyncPreemption: &enableAsync,
	}
	SetDefaults_ReservationArgs(obj2)
	assert.NotNil(t, obj2.EnableAsyncPreemption)
	assert.False(t, *obj2.EnableAsyncPreemption)
}

func TestSetDefaults_CoschedulingArgs(t *testing.T) {
	obj := &CoschedulingArgs{}
	SetDefaults_CoschedulingArgs(obj)
	assert.NotNil(t, obj.EnableAsyncPreemption)
	assert.True(t, *obj.EnableAsyncPreemption)

	enableAsync := false
	obj2 := &CoschedulingArgs{
		EnableAsyncPreemption: &enableAsync,
	}
	SetDefaults_CoschedulingArgs(obj2)
	assert.NotNil(t, obj2.EnableAsyncPreemption)
	assert.False(t, *obj2.EnableAsyncPreemption)
}
