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
	"testing"

	"github.com/stretchr/testify/assert"
)

var _ CoreSchedExtendedInterface = (*wrappedCoreSchedExtended)(nil)

type wrappedCoreSchedExtended struct {
	*CoreSched
	beforeFn func()
	afterFn  func()
}

func (w *wrappedCoreSchedExtended) Clear(pidType CoreSchedScopeType, pid ...uint32) ([]uint32, error) {
	if w.beforeFn != nil {
		w.beforeFn()
	}
	failedPIDs, err := w.CoreSched.Clear(pidType, pid...)
	if w.afterFn != nil {
		w.afterFn()
	}
	return failedPIDs, err
}

func (w *wrappedCoreSchedExtended) Assign(pidTypeFrom CoreSchedScopeType, pidFrom uint32, pidTypeTo CoreSchedScopeType, pidsTo ...uint32) ([]uint32, error) {
	if w.beforeFn != nil {
		w.beforeFn()
	}
	failedPIDs, err := w.CoreSched.Assign(pidTypeFrom, pidFrom, pidTypeTo, pidsTo...)
	if w.afterFn != nil {
		w.afterFn()
	}
	return failedPIDs, err
}

func TestCoreSched(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		invalidPidType := CoreSchedScopeType(100)
		cs := NewCoreSched()
		got, gotErr := cs.Get(invalidPidType, 0)
		assert.Error(t, gotErr)
		assert.Equal(t, uint64(0), got)
		gotErr = cs.Create(invalidPidType, 0)
		assert.Error(t, gotErr)

		cse := NewCoreSchedExtended()
		_, gotErr = cse.Clear(invalidPidType, 0)
		assert.Error(t, gotErr)
		_, gotErr = cse.Assign(invalidPidType, 0, invalidPidType, 0)
		assert.Error(t, gotErr)
	})
}

func TestFakeCoreSchedExtended(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		initPIDToCookie := map[uint32]uint64{
			1:     0,
			2:     0,
			10000: 1,
			10001: 1,
			10010: 2,
			20000: 3,
		}
		initPIDToPGID := map[uint32]uint32{
			1:     1,
			2:     1,
			10000: 10000,
			10001: 10000,
			10010: 10010,
			20000: 20000,
			11000: 1,
			21000: 10000,
		}
		initPIDError := map[uint32]bool{
			3:     true,
			9999:  true,
			10002: true,
		}

		// new
		cs := NewFakeCoreSchedExtended(initPIDToCookie, initPIDToPGID, initPIDError)
		assert.NotNil(t, cs)
		f, ok := cs.(*FakeCoreSchedExtended)
		assert.True(t, ok)
		assert.NotNil(t, f)
		f.SetCurPID(2)
		f.SetNextCookieID(20001)

		// get
		got, gotErr := cs.Get(CoreSchedScopeProcessGroup, 1)
		assert.Equal(t, uint64(0), got)
		assert.Error(t, gotErr)
		got, gotErr = cs.Get(CoreSchedScopeThread, 3)
		assert.Equal(t, uint64(0), got)
		assert.Error(t, gotErr)
		got, gotErr = cs.Get(CoreSchedScopeThread, 20000)
		assert.Equal(t, uint64(3), got)
		assert.NoError(t, gotErr)

		// create
		got, gotErr = cs.Get(CoreSchedScopeThread, 21000)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(0), got)
		gotErr = cs.Create(CoreSchedScopeProcessGroup, 10000)
		assert.NoError(t, gotErr)
		got, gotErr = cs.Get(CoreSchedScopeThread, 10000)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(20001), got)
		got, gotErr = cs.Get(CoreSchedScopeThread, 21000)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(20001), got)

		// shareTo
		gotErr = cs.ShareTo(CoreSchedScopeThread, 21000)
		assert.NoError(t, gotErr)
		got, gotErr = cs.Get(CoreSchedScopeThread, 21000)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(0), got)

		// shareFrom
		gotErr = cs.ShareFrom(CoreSchedScopeThread, 10000)
		assert.NoError(t, gotErr)
		got, gotErr = cs.Get(CoreSchedScopeThread, 2)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(20001), got)
		gotErr = cs.ShareFrom(CoreSchedScopeThread, 1)
		assert.NoError(t, gotErr)
		got, gotErr = cs.Get(CoreSchedScopeThread, 2)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(0), got)

		// clear
		gotErrPIDs, gotErr := cs.Clear(CoreSchedScopeThread, 10010)
		assert.NoError(t, gotErr)
		assert.Nil(t, gotErrPIDs)
		got, gotErr = cs.Get(CoreSchedScopeThread, 10010)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(0), got)

		// assign
		gotErrPIDs, gotErr = cs.Assign(CoreSchedScopeThread, 21000, CoreSchedScopeProcessGroup, 10000)
		assert.NoError(t, gotErr)
		assert.Nil(t, gotErrPIDs)
		got, gotErr = cs.Get(CoreSchedScopeThread, 10000)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(0), got)
		got, gotErr = cs.Get(CoreSchedScopeThread, 10001)
		assert.NoError(t, gotErr)
		assert.Equal(t, uint64(0), got)
		gotErrPIDs, gotErr = cs.Assign(CoreSchedScopeThread, 1, CoreSchedScopeProcessGroup, 9999, 10000, 10001, 10002)
		assert.Error(t, gotErr)
		assert.Equal(t, []uint32{9999, 10002}, gotErrPIDs)
	})
}

func TestEnableCoreSchedIfSupported(t *testing.T) {
	type fields struct {
		prepareFn func(helper *FileTestUtil)
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
		want1  string
	}{
		{
			name:  "unsupported since no sched features file",
			want:  false,
			want1: "core sched not supported",
		},
		{
			name: "unsupported when sched features content is unexpected",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					featuresPath := SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, ``)
				},
			},
			want:  false,
			want1: "core sched not supported",
		},
		{
			name: "unsupported when sched features content has no core sched",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					featuresPath := SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C`)
				},
			},
			want:  false,
			want1: "core sched not supported",
		},
		{
			name: "supported when core sched shows in the sysctl",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					sysctlFeaturePath := GetProcSysFilePath(KernelSchedCore)
					helper.WriteFileContents(sysctlFeaturePath, "1\n")
				},
			},
			want:  true,
			want1: "",
		},
		{
			name: "supported when core sched disabled in the sysctl but can be enabled",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					sysctlFeaturePath := GetProcSysFilePath(KernelSchedCore)
					helper.WriteFileContents(sysctlFeaturePath, "0\n")
				},
			},
			want:  true,
			want1: "",
		},
		{
			name: "supported when core sched shows in the features",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					featuresPath := SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
				},
			},
			want:  true,
			want1: "",
		},
		{
			name: "supported when core sched shows in the features 1",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					featuresPath := SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A CORE_SCHED FEATURE_B`)
				},
			},
			want:  true,
			want1: "",
		},
		{
			name: "supported when sysctl disabled but can be enabled",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					sysctlFeaturePath := GetProcSysFilePath(KernelSchedCore)
					helper.WriteFileContents(sysctlFeaturePath, "0\n")
				},
			},
			want:  true,
			want1: "",
		},
		{
			name: "supported when sched_features disabled but can be enabled",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					featuresPath := SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B NO_CORE_SCHED`)
				},
			},
			want:  true,
			want1: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			got, got1 := EnableCoreSchedIfSupported()
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestSetCoreSchedFeatureEnabled(t *testing.T) {
	type fields struct {
		prepareFn func(helper *FileTestUtil)
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
		want1  string
	}{
		{
			name:  "unsupported since no sched features file",
			want:  false,
			want1: "failed to read sched_features",
		},
		{
			name: "supported when core sched shows in the features",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					featuresPath := SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
				},
			},
			want:  true,
			want1: "",
		},
		{
			name: "supported when core sched shows in the features 1",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					featuresPath := SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A CORE_SCHED FEATURE_B`)
				},
			},
			want:  true,
			want1: "",
		},
		{
			name: "enabled when add sched features for core sched",
			fields: fields{
				prepareFn: func(helper *FileTestUtil) {
					featuresPath := SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C`)
				},
			},
			want:  true,
			want1: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			got, got1 := SetCoreSchedFeatureEnabled()
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}
