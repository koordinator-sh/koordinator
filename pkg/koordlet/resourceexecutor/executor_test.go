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

package resourceexecutor

import (
	"fmt"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util/cache"
)

func TestNewResourceUpdateExecutor(t *testing.T) {
	t.Run("", func(t *testing.T) {
		e := NewResourceUpdateExecutor()
		assert.NotNil(t, e)
	})
}

func TestNewResourceUpdateExecutor_Run(t *testing.T) {
	t.Run("", func(t *testing.T) {
		e := &ResourceUpdateExecutorImpl{
			ResourceCache: cache.NewCacheDefault(),
			Config:        NewDefaultConfig(),
		}
		stop := make(chan struct{})
		defer func() {
			close(stop)
		}()

		e.Run(stop)
	})
}

func TestResourceUpdateExecutor_Update(t *testing.T) {
	testUpdater, err := DefaultCgroupUpdaterFactory.New(sysutil.CPUCFSQuotaName, "test", "-1", &audit.EventHelper{})
	assert.NoError(t, err)
	testUpdater1, err := DefaultCgroupUpdaterFactory.New(sysutil.MemoryLimitName, "test", "1048576", &audit.EventHelper{})
	assert.NoError(t, err)
	testUpdater2, err := DefaultCgroupUpdaterFactory.New(sysutil.CPUSetCPUSName, "test", "0-31", &audit.EventHelper{})
	assert.NoError(t, err)
	testUpdater3, err := DefaultCgroupUpdaterFactory.New(sysutil.CPUSharesName, "test", "1024", &audit.EventHelper{})
	assert.NoError(t, err)
	testInvalidUpdater, err := DefaultCgroupUpdaterFactory.New(sysutil.CPUSetCPUSName, "test", "invalid content", &audit.EventHelper{})
	assert.NoError(t, err)
	type fields struct {
		notStarted   bool
		pathNotExist bool
		config       *Config
	}
	type args struct {
		isCacheable bool
		resource    ResourceUpdater
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "non-cacheable update",
			args: args{
				isCacheable: false,
				resource:    testUpdater,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "cacheable update",
			args: args{
				isCacheable: true,
				resource:    testUpdater1,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "cacheable update but not started",
			fields: fields{
				notStarted: true,
			},
			args: args{
				isCacheable: true,
				resource:    testUpdater2,
			},
			want:    false,
			wantErr: true,
		},
		{
			name: "cacheable update error",
			args: args{
				isCacheable: true,
				resource:    testInvalidUpdater,
			},
			want:    false,
			wantErr: true,
		},
		{
			name: "ignore update error for path not exist",
			fields: fields{
				pathNotExist: true,
			},
			args: args{
				isCacheable: false,
				resource:    testUpdater3,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "ignore cacheable update error for path not exist",
			fields: fields{
				pathNotExist: true,
			},
			args: args{
				isCacheable: true,
				resource:    testUpdater3,
			},
			want:    false,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			if !tt.fields.pathNotExist { // prepare test file
				helper.WriteFileContents(tt.args.resource.Path(), "")
			}
			e := &ResourceUpdateExecutorImpl{
				ResourceCache: cache.NewCacheDefault(),
				Config:        NewDefaultConfig(),
			}
			if tt.fields.config != nil {
				e.Config = tt.fields.config
			}
			if !tt.fields.notStarted {
				stop := make(chan struct{})
				defer func() {
					close(stop)
				}()

				e.Run(stop)
			}

			got, gotErr := e.Update(tt.args.isCacheable, tt.args.resource)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func TestResourceUpdateExecutor_UpdateBatch(t *testing.T) {
	testUpdater, err := DefaultCgroupUpdaterFactory.New(sysutil.CPUCFSQuotaName, "test", "-1", &audit.EventHelper{})
	assert.NoError(t, err)
	testUpdater1, err := DefaultCgroupUpdaterFactory.New(sysutil.MemoryLimitName, "test", "1048576", &audit.EventHelper{})
	assert.NoError(t, err)
	type fields struct {
		notStarted bool
	}
	type args struct {
		isCacheable bool
		resources   []ResourceUpdater
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "nothing to update",
			args: args{
				isCacheable: false,
			},
		},
		{
			name: "non-cacheable update a batch of resources",
			args: args{
				isCacheable: false,
				resources: []ResourceUpdater{
					testUpdater,
					testUpdater1,
				},
			},
		},
		{
			name: "cacheable update a batch of resource",
			args: args{
				isCacheable: true,
				resources: []ResourceUpdater{
					testUpdater,
					testUpdater1,
				},
			},
		},
		{
			name: "abort cacheable update when GC is not started",
			fields: fields{
				notStarted: true,
			},
			args: args{
				isCacheable: true,
				resources: []ResourceUpdater{
					testUpdater,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			e := &ResourceUpdateExecutorImpl{
				ResourceCache: cache.NewCacheDefault(),
				Config:        NewDefaultConfig(),
			}
			if !tt.fields.notStarted {
				stop := make(chan struct{})
				defer func() {
					close(stop)
				}()

				e.Run(stop)
			}

			e.UpdateBatch(tt.args.isCacheable, tt.args.resources...)
		})
	}
}

// TestResourceUpdateExecutor_UnsupportedErrNotRetried verifies that a resource whose update fails with an
// unsupported-classified error (e.g. a cgroup write rejected with EINVAL, see wrapCgroupWriteErr) is marked
// as done in the resource cache and is not retried by subsequent update batches.
func TestResourceUpdateExecutor_UnsupportedErrNotRetried(t *testing.T) {
	var calls int32
	updateFn := func(r ResourceUpdater) error {
		atomic.AddInt32(&calls, 1)
		return sysutil.WrapResourceUnsupportedErr(fmt.Errorf("write cgroup memory.high failed, err: %w", syscall.EINVAL))
	}
	updater, err := NewCommonDefaultUpdaterWithUpdateFunc("test-einval-unsupported", "/tmp/cgroup/memory.high", "1024", updateFn, &audit.EventHelper{})
	assert.NoError(t, err)

	e := &ResourceUpdateExecutorImpl{
		ResourceCache: cache.NewCacheDefault(),
		Config:        NewDefaultConfig(),
	}
	stop := make(chan struct{})
	defer close(stop)
	e.Run(stop)

	// the first batch invokes the update once
	e.UpdateBatch(true, updater)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

	// the failed task is cached as unsupported, so the following batches do not retry it
	e.UpdateBatch(true, updater)
	e.UpdateBatch(true, updater)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

	// LeveledUpdateBatch also stops retrying the unsupported resource
	e.LeveledUpdateBatch([][]ResourceUpdater{{updater}})
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

// TestResourceUpdateExecutor_NonUnsupportedErrStillRetried verifies that errors which are not classified as
// unsupported (e.g. EACCES) do not stop the retry: the task stays queued and is attempted again in the next
// batch.
func TestResourceUpdateExecutor_NonUnsupportedErrStillRetried(t *testing.T) {
	var calls int32
	updateFn := func(r ResourceUpdater) error {
		atomic.AddInt32(&calls, 1)
		return fmt.Errorf("open %s: %w", r.Path(), syscall.EACCES)
	}
	updater, err := NewCommonDefaultUpdaterWithUpdateFunc("test-eacces-retry", "/tmp/cgroup/memory.high", "1024", updateFn, &audit.EventHelper{})
	assert.NoError(t, err)

	e := &ResourceUpdateExecutorImpl{
		ResourceCache: cache.NewCacheDefault(),
		Config:        NewDefaultConfig(),
	}
	stop := make(chan struct{})
	defer close(stop)
	e.Run(stop)

	e.UpdateBatch(true, updater)
	e.UpdateBatch(true, updater)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
}

// TestResourceUpdateExecutor_CgroupDirErrStillRetried verifies the pre-existing behavior is kept: a cgroup
// dir-not-exist error is ignored (not surfaced to callers) but is NOT cached as done, so the task is
// retried, e.g. when the pod cgroup is created later.
func TestResourceUpdateExecutor_CgroupDirErrStillRetried(t *testing.T) {
	var calls int32
	updateFn := func(r ResourceUpdater) error {
		atomic.AddInt32(&calls, 1)
		return ResourceCgroupDirErr("write cgroup memory.high failed, msg: cgroup dir not exist")
	}
	updater, err := NewCommonDefaultUpdaterWithUpdateFunc("test-cgroupdir-retry", "/tmp/cgroup/memory.high", "1024", updateFn, &audit.EventHelper{})
	assert.NoError(t, err)

	e := &ResourceUpdateExecutorImpl{
		ResourceCache: cache.NewCacheDefault(),
		Config:        NewDefaultConfig(),
	}
	stop := make(chan struct{})
	defer close(stop)
	e.Run(stop)

	e.UpdateBatch(true, updater)
	e.UpdateBatch(true, updater)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
}

// TestLeveledUpdateBatch_UnsupportedCachesOnMergePass verifies that the merge-pass cacheUnsupported call site
// is exercised: a fresh executor with no pre-cached key calls LeveledUpdateBatch once, the merge-pass invokes
// cacheUnsupported, and the direct-pass is skipped (needUpdate returns false after caching).
func TestLeveledUpdateBatch_UnsupportedCachesOnMergePass(t *testing.T) {
	var calls int32
	updateFn := func(r ResourceUpdater) error {
		atomic.AddInt32(&calls, 1)
		return sysutil.WrapResourceUnsupportedErr(fmt.Errorf("write cgroup %s failed, err: %w", r.Key(), syscall.EINVAL))
	}
	updater, err := NewCommonDefaultUpdaterWithUpdateFunc("test-einval-merge-pass", "/tmp/mem", "1024", updateFn, &audit.EventHelper{})
	assert.NoError(t, err)

	e := &ResourceUpdateExecutorImpl{
		ResourceCache: cache.NewCacheDefault(),
		Config:        NewDefaultConfig(),
	}
	stop := make(chan struct{})
	defer close(stop)
	e.Run(stop)

	// merge-pass: needUpdate true → MergeUpdate fails with unsupported → cacheUnsupported caches
	e.LeveledUpdateBatch([][]ResourceUpdater{{updater}})
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

	// second call: needUpdate false (cached) → no update
	e.LeveledUpdateBatch([][]ResourceUpdater{{updater}})
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

// TestLeveledUpdateBatch_UnsupportedCachesOnDirectPass verifies that the direct-pass cacheUnsupported call
// site is exercised: the merge-pass fails with a non-ignored error (no cache entry), then the direct-pass
// fails with an unsupported error, triggering cacheUnsupported from the direct-pass path.
func TestLeveledUpdateBatch_UnsupportedCachesOnDirectPass(t *testing.T) {
	var callCount int32
	updateFn := func(r ResourceUpdater) error {
		cnt := atomic.AddInt32(&callCount, 1)
		if cnt == 1 {
			// merge-pass: non-ignored transient error, no cache entry created
			return fmt.Errorf("transient write error")
		}
		// direct-pass: unsupported error
		return sysutil.WrapResourceUnsupportedErr(fmt.Errorf("write cgroup %s failed, err: %w", r.Key(), syscall.EINVAL))
	}
	updater, err := NewCommonDefaultUpdaterWithUpdateFunc("test-einval-direct-pass", "/tmp/mem", "1024", updateFn, &audit.EventHelper{})
	assert.NoError(t, err)

	e := &ResourceUpdateExecutorImpl{
		ResourceCache: cache.NewCacheDefault(),
		Config:        NewDefaultConfig(),
	}
	stop := make(chan struct{})
	defer close(stop)
	e.Run(stop)

	// merge-pass: transient error → no cache; direct-pass: unsupported → cacheUnsupported called from direct-pass
	e.LeveledUpdateBatch([][]ResourceUpdater{{updater}})
	assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))

	// second call: needUpdate false (cached by direct-pass) → both passes skip
	e.LeveledUpdateBatch([][]ResourceUpdater{{updater}})
	assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
}

// TestCacheUnsupported_SetDefaultError verifies the V(5) SetDefault error log branch in cacheUnsupported by
// calling it on an executor whose cache has not started GC, causing SetDefault to return an error.
func TestCacheUnsupported_SetDefaultError(t *testing.T) {
	e := &ResourceUpdateExecutorImpl{
		ResourceCache: cache.NewCacheDefault(),
		Config:        NewDefaultConfig(),
	}
	// Intentionally do NOT call e.Run() — gcStarted stays false → SetDefault fails

	updater, _ := NewCommonDefaultUpdaterWithUpdateFunc("test-unstarted", "/tmp/test", "1024",
		func(r ResourceUpdater) error { return nil }, &audit.EventHelper{})
	err := sysutil.WrapResourceUnsupportedErr(fmt.Errorf("test unsupported error"))

	// Must not panic; the V(5) SetDefault error log fires internally.
	e.cacheUnsupported(updater, err)

	// Verify the updater was NOT cached (SetDefault failed)
	_, found := e.ResourceCache.Get("test-unstarted")
	assert.False(t, found)
}
