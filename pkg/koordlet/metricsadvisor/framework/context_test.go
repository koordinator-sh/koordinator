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

func TestSharedState_UpdateHostAppUsage(t *testing.T) {
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
			name: "add host point",
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
			r.UpdateHostAppUsage(tt.args.cpu, tt.args.memory)
			gotCPU, gotMemory := r.GetHostAppUsage()
			assert.Equal(t, tt.want.cpu, *gotCPU)
			assert.Equal(t, tt.want.memory, *gotMemory)
		})
	}
}

func TestSharedState_NodeMemoryWithPageCache(t *testing.T) {
	now := time.Now()
	r := NewSharedState()
	p := metriccache.Point{Timestamp: now, Value: 2048}
	r.UpdateNodeMemoryWithPageCache(p)
	got := r.GetNodeMemoryWithPageCache()
	assert.Equal(t, p, *got)
}

func TestSharedState_NodeMemoryWithHotPageCache(t *testing.T) {
	now := time.Now()
	r := NewSharedState()
	p := metriccache.Point{Timestamp: now, Value: 3072}
	r.UpdateNodeMemoryWithHotPageCache(p)
	got := r.GetNodeMemoryWithHotPageCache()
	assert.Equal(t, p, *got)
}

func TestSharedState_PodsMemoryWithPageCache(t *testing.T) {
	now := time.Now()
	r := NewSharedState()
	p := metriccache.Point{Timestamp: now, Value: 4096}
	r.UpdatePodsMemoryWithPageCache("c1", p)
	got := r.GetPodsMemoryWithPageCacheByCollector()
	assert.Equal(t, p, got["c1"])
}

func TestSharedState_PodsMemoryWithHotPageCache(t *testing.T) {
	now := time.Now()
	r := NewSharedState()
	p := metriccache.Point{Timestamp: now, Value: 5120}
	r.UpdatePodsMemoryWithHotPageCache("c1", p)
	got := r.GetPodsMemoryWithHotPageCacheByCollector()
	assert.Equal(t, p, got["c1"])
}

func TestSharedState_HostAppMemoryWithPageCache(t *testing.T) {
	now := time.Now()
	r := NewSharedState()
	p := metriccache.Point{Timestamp: now, Value: 6144}
	r.UpdateHostAppMemoryWithPageCache(p)
	got := r.GetHostAppMemoryWithPageCache()
	assert.Equal(t, p, *got)
}

func TestSharedState_HostAppMemoryWithHotPageCache(t *testing.T) {
	now := time.Now()
	r := NewSharedState()
	p := metriccache.Point{Timestamp: now, Value: 7168}
	r.UpdateHostAppMemoryWithHotPageCache(p)
	got := r.GetHostAppMemoryWithHotPageCache()
	assert.Equal(t, p, *got)
}
