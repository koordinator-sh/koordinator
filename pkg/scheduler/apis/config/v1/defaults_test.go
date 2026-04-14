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
	"k8s.io/utils/ptr"
)

func TestSetDefaults_EnableQueueHint(t *testing.T) {
	t.Run("Reservation defaults EnableQueueHint to false when unset", func(t *testing.T) {
		args := &ReservationArgs{}
		SetDefaults_ReservationArgs(args)
		assert.NotNil(t, args.EnableQueueHint)
		assert.False(t, *args.EnableQueueHint)
	})

	t.Run("Reservation keeps EnableQueueHint when already set", func(t *testing.T) {
		args := &ReservationArgs{EnableQueueHint: ptr.To(true)}
		SetDefaults_ReservationArgs(args)
		assert.NotNil(t, args.EnableQueueHint)
		assert.True(t, *args.EnableQueueHint)
	})

	t.Run("DeviceShare defaults EnableQueueHint to false when unset", func(t *testing.T) {
		args := &DeviceShareArgs{}
		SetDefaults_DeviceShareArgs(args)
		assert.NotNil(t, args.EnableQueueHint)
		assert.False(t, *args.EnableQueueHint)
	})

	t.Run("DeviceShare keeps EnableQueueHint when already set", func(t *testing.T) {
		args := &DeviceShareArgs{EnableQueueHint: ptr.To(true)}
		SetDefaults_DeviceShareArgs(args)
		assert.NotNil(t, args.EnableQueueHint)
		assert.True(t, *args.EnableQueueHint)
	})

	t.Run("NodeNUMAResource defaults EnableQueueHint to false when unset", func(t *testing.T) {
		args := &NodeNUMAResourceArgs{}
		SetDefaults_NodeNUMAResourceArgs(args)
		assert.NotNil(t, args.EnableQueueHint)
		assert.False(t, *args.EnableQueueHint)
	})

	t.Run("NodeNUMAResource keeps EnableQueueHint when already set", func(t *testing.T) {
		args := &NodeNUMAResourceArgs{EnableQueueHint: ptr.To(true)}
		SetDefaults_NodeNUMAResourceArgs(args)
		assert.NotNil(t, args.EnableQueueHint)
		assert.True(t, *args.EnableQueueHint)
	})

	t.Run("LoadAware defaults EnableQueueHint to false when unset", func(t *testing.T) {
		args := &LoadAwareSchedulingArgs{}
		SetDefaults_LoadAwareSchedulingArgs(args)
		assert.NotNil(t, args.EnableQueueHint)
		assert.False(t, *args.EnableQueueHint)
	})

	t.Run("LoadAware keeps EnableQueueHint when already set", func(t *testing.T) {
		args := &LoadAwareSchedulingArgs{EnableQueueHint: ptr.To(true)}
		SetDefaults_LoadAwareSchedulingArgs(args)
		assert.NotNil(t, args.EnableQueueHint)
		assert.True(t, *args.EnableQueueHint)
	})
}
