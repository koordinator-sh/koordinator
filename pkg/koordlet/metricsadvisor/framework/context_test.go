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

package framework

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
)

func TestSharedState_UpdateNodeUsage(t *testing.T) {
	now := time.Now()
	type args struct {
		cpu    metriccache.Point
		memory metriccache.Point
	}
	type want struct {
		cpu    metriccache.Point
		memory metriccache.Point
	}
	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "add node point",
			args: args{
				cpu: metriccache.Point{
					Timestamp: now,
					Value:     1,
				},
				memory: metriccache.Point{
					Timestamp: now,
					Value:     1024,
				},
			},
			want: want{
				cpu: metriccache.Point{
					Timestamp: now,
					Value:     1,
				},
				memory: metriccache.Point{
					Timestamp: now,
					Value:     1024,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewSharedState()
			r.UpdateNodeUsage(tt.args.cpu, tt.args.memory)
			gotCPU, gotMemory := r.GetNodeUsage()
			assert.Equal(t, tt.want.cpu, *gotCPU)
			assert.Equal(t, tt.want.memory, *gotMemory)
		})
	}
}

func TestSharedState_UpdatePodUsage(t *testing.T) {
	now := time.Now()
	type args struct {
		collectorName string
		cpu           metriccache.Point
		memory        metriccache.Point
	}
	type want struct {
		cpu    map[string]metriccache.Point
		memory map[string]metriccache.Point
	}
	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "update pod usage",
			args: args{
				collectorName: "test-collector",
				cpu: metriccache.Point{
					Timestamp: now,
					Value:     1,
				},
				memory: metriccache.Point{
					Timestamp: now,
					Value:     1024,
				},
			},
			want: want{
				cpu: map[string]metriccache.Point{
					"test-collector": {
						Timestamp: now,
						Value:     1,
					},
				},
				memory: map[string]metriccache.Point{
					"test-collector": {
						Timestamp: now,
						Value:     1024,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewSharedState()
			r.UpdatePodUsage(tt.args.collectorName, tt.args.cpu, tt.args.memory)
			gotCPU, gotMemory := r.GetPodsUsageByCollector()
			assert.Equal(t, tt.want.cpu, gotCPU)
			assert.Equal(t, tt.want.memory, gotMemory)
		})
	}
}
