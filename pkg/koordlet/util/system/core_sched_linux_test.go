//go:build linux
// +build linux

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

package system

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
)

func DumpGoThreadInfo(prefix string) string {
	return fmt.Sprintf("[%s] PID=%v, parent PID=%v, TTID=%v, %s",
		prefix, os.Getpid(), os.Getppid(), syscall.Gettid(), DumpGoroutineInfo())
}

func BenchmarkCoreSchedGet(b *testing.B) {
	tests := []struct {
		name        string
		parallelism int
	}{
		{
			name:        "2",
			parallelism: 2,
		},
		{
			name:        "10",
			parallelism: 10,
		},
		{
			name:        "50",
			parallelism: 50,
		},
		{
			name:        "100",
			parallelism: 100,
		},
		{
			name:        "200",
			parallelism: 200,
		},
		{
			name:        "500",
			parallelism: 500,
		},
	}

	b.ResetTimer()
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				for j := 0; j < tt.parallelism; j++ {
					wg.Add(1)
					go func(x int) {
						cs := NewCoreSched()
						tid := syscall.Gettid()
						_, err := cs.Get(CoreSchedScopeThread, uint32(tid))
						if err != nil {
							b.Logf("CORE_SCHED_SCOPE_THREAD %v get failed, err: %s\n", x, err)
						}
						wg.Done()
					}(j)
				}
				wg.Wait()
			}
		})
	}
}

func BenchmarkCoreSchedExtendedAssign(b *testing.B) {
	tests := []struct {
		name        string
		parallelism int
		isBatch     bool
	}{
		{
			name:        "2",
			parallelism: 2,
		},
		{
			name:        "10",
			parallelism: 10,
		},
		{
			name:        "50",
			parallelism: 50,
		},
		{
			name:        "100",
			parallelism: 100,
		},
		{
			name:        "200",
			parallelism: 200,
		},
		{
			name:        "500",
			parallelism: 500,
		},
		{
			name:        "2-batch",
			parallelism: 2,
			isBatch:     true,
		},
		{
			name:        "10-batch",
			parallelism: 10,
			isBatch:     true,
		},
		{
			name:        "50-batch",
			parallelism: 50,
			isBatch:     true,
		},
		{
			name:        "100-batch",
			parallelism: 100,
			isBatch:     true,
		},
		{
			name:        "200-batch",
			parallelism: 200,
			isBatch:     true,
		},
		{
			name:        "500-batch",
			parallelism: 500,
			isBatch:     true,
		},
	}

	b.ResetTimer()
	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if tt.isBatch { // batch assign pids
					err := GoWithNewThread(func() interface{} {
						tid := syscall.Gettid()
						pidsTo := make([]uint32, tt.parallelism)
						for j := 0; j < tt.parallelism; j++ {
							pidsTo[j] = uint32(tid)
						}

						cs := &wrappedCoreSchedExtended{
							CoreSched: &CoreSched{},
							beforeFn: func() {
								b.Log(DumpGoThreadInfo("before [batch]"))
							},
							afterFn: func() {
								b.Log(DumpGoThreadInfo("after [batch]"))
							},
						}
						_, err := cs.Assign(CoreSchedScopeThread, uint32(tid), CoreSchedScopeThread, pidsTo...)
						if err != nil {
							return err
						}
						return nil
					})
					if err != nil {
						b.Logf("CORE_SCHED_SCOPE_THREAD assign batch failed, err: %s\n", err)
					}
					continue
				}

				var wg sync.WaitGroup
				for j := 0; j < tt.parallelism; j++ {
					wg.Add(1)
					go func(x int) {
						tid := syscall.Gettid()
						cs := &wrappedCoreSchedExtended{
							CoreSched: &CoreSched{},
							beforeFn: func() {
								b.Log(DumpGoThreadInfo("before " + strconv.Itoa(x)))
							},
							afterFn: func() {
								b.Log(DumpGoThreadInfo("after " + strconv.Itoa(x)))
							},
						}
						_, err := cs.Assign(CoreSchedScopeThread, uint32(tid), CoreSchedScopeThread, uint32(tid))
						if err != nil {
							b.Logf("CORE_SCHED_SCOPE_THREAD %v assign failed, err: %s\n", x, err)
						}
						wg.Done()
					}(j)
				}
				wg.Wait()
			}
		})
	}
}
