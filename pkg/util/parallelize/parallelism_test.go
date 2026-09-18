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

package parallelize

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestChunkSizeFor(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{
			name: "0 items",
			n:    0,
			want: 1,
		},
		{
			name: "small n",
			n:    4,
			want: 1,
		},
		{
			name: "exact boundaries",
			n:    16 * 16,
			want: 16,
		},
		{
			name: "large values",
			n:    1000,
			want: 31,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chunkSizeFor(tt.n); got != tt.want {
				t.Errorf("chunkSizeFor(%v) = %v, want %v", tt.n, got, tt.want)
			}
		})
	}
}

func TestUntil(t *testing.T) {
	t.Run("exactly once", func(t *testing.T) {
		pieces := 100
		var count int32

		Until(context.Background(), pieces, func(piece int) {
			atomic.AddInt32(&count, 1)
		})

		if count != int32(pieces) {
			t.Errorf("Until executed %d times, want %d", count, pieces)
		}
	})

	t.Run("0 pieces", func(t *testing.T) {
		var count int32

		Until(context.Background(), 0, func(piece int) {
			atomic.AddInt32(&count, 1)
		})

		if count != 0 {
			t.Errorf("Until executed %d times on 0 pieces, want 0", count)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var count int32

		Until(ctx, 100, func(piece int) {
			// This might execute a few items before the worker checks the context,
			// or items that are sent before cancellation takes full effect,
			// but we can definitely test that it doesn't do all the work
			// or we can simulate a long running task that checks the context.
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&count, 1)
		})

		if count == 100 {
			t.Errorf("Until executed all pieces despite cancelled context")
		}
	})
}
