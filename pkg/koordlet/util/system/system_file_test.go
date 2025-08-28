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
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcSysctl(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		helper := NewFileTestUtil(t)

		testProcSysFile := "test_file"
		testProcSysFilepath := filepath.Join(SysctlSubDir, testProcSysFile)
		testContent := "0"
		assert.False(t, FileExists(GetProcSysFilePath(testProcSysFile)))
		helper.WriteProcSubFileContents(testProcSysFilepath, testContent)

		s := NewProcSysctl()
		v, err := s.GetSysctl(testProcSysFile)
		assert.NoError(t, err)
		assert.Equal(t, testContent, strconv.Itoa(v))

		testValue := 1
		err = s.SetSysctl(testProcSysFile, testValue)
		assert.NoError(t, err)
		got := helper.ReadProcSubFileContents(testProcSysFilepath)
		gotV, err := strconv.ParseInt(got, 10, 32)
		assert.NoError(t, err)
		assert.Equal(t, int(gotV), testValue)
	})
}

func TestSetSchedGroupIdentity(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		helper := NewFileTestUtil(t)

		// system not supported
		err := SetSchedGroupIdentity(false)
		assert.Error(t, err)

		// system supported, already disabled
		testProcSysFile := KernelSchedGroupIdentityEnable
		testProcSysFilepath := filepath.Join(SysctlSubDir, testProcSysFile)
		testContent := "0"
		assert.False(t, FileExists(GetProcSysFilePath(testProcSysFile)))
		helper.WriteProcSubFileContents(testProcSysFilepath, testContent)
		err = SetSchedGroupIdentity(false)
		assert.NoError(t, err)
		got := helper.ReadProcSubFileContents(testProcSysFilepath)
		assert.Equal(t, got, testContent)

		// system supported, set enabled
		testContent = "1"
		err = SetSchedGroupIdentity(true)
		assert.NoError(t, err)
		got = helper.ReadProcSubFileContents(testProcSysFilepath)
		assert.Equal(t, got, testContent)
	})
}

func TestSetSchedCore(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		helper := NewFileTestUtil(t)

		// system not supported
		err := SetSchedCore(false)
		assert.Error(t, err)

		// system supported, already disabled
		testProcSysFile := KernelSchedCore
		testProcSysFilepath := filepath.Join(SysctlSubDir, testProcSysFile)
		testContent := "0"
		assert.False(t, FileExists(GetProcSysFilePath(testProcSysFile)))
		helper.WriteProcSubFileContents(testProcSysFilepath, testContent)
		err = SetSchedCore(false)
		assert.NoError(t, err)
		got := helper.ReadProcSubFileContents(testProcSysFilepath)
		assert.Equal(t, got, testContent)

		// system supported, set enabled
		testContent = "1"
		err = SetSchedCore(true)
		assert.NoError(t, err)
		got = helper.ReadProcSubFileContents(testProcSysFilepath)
		assert.Equal(t, got, testContent)
	})
}

func TestGetSchedFeatures(t *testing.T) {
	tests := []struct {
		name      string
		prepareFn func(helper *FileTestUtil)
		wantErr   bool
		want      map[string]bool
	}{
		{
			name:    "read sched_features failed",
			wantErr: true,
		},
		{
			name: "empty sched_features",
			prepareFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				helper.WriteFileContents(featurePath, "")
			},
			wantErr: false,
			want:    map[string]bool{},
		},
		{
			name: "read sched_features correctly",
			prepareFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				helper.WriteFileContents(featurePath, "NO_ID_EXPELLER_SHARE_CORE ID_ABSOLUTE_EXPEL")
			},
			wantErr: false,
			want: map[string]bool{
				"ID_EXPELLER_SHARE_CORE": false,
				"ID_ABSOLUTE_EXPEL":      true,
			},
		},
		{
			name: "read invalid sched_features",
			prepareFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				helper.WriteFileContents(featurePath, "NO_ID_ABSOLUTE_EXPEL ID_ABSOLUTE_EXPEL")
			},
			wantErr: true,
			want: map[string]bool{
				"ID_ABSOLUTE_EXPEL": false,
			},
		},
		{
			name: "read invalid sched_features 1",
			prepareFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				helper.WriteFileContents(featurePath, "ID_ABSOLUTE_EXPEL NO_ID_ABSOLUTE_EXPEL")
			},
			wantErr: true,
			want: map[string]bool{
				"ID_ABSOLUTE_EXPEL": true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			if tt.prepareFn != nil {
				tt.prepareFn(helper)
			}
			defer helper.Cleanup()
			got, gotErr := GetSchedFeatures()
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetSchedFeatures(t *testing.T) {
	type args struct {
		featureMap map[string]bool
		valueMap   map[string]bool
	}
	tests := []struct {
		name      string
		prepareFn func(helper *FileTestUtil)
		args      args
		wantErr   bool
		wantFn    func(helper *FileTestUtil)
	}{
		{
			name:    "no feature to set",
			wantErr: false,
		},
		{
			name: "invalid feature map",
			args: args{
				valueMap: map[string]bool{
					"ID_ABSOLUTE_EXPEL": true,
				},
			},
			wantErr: true,
		},
		{
			name: "write sched_features failed",
			args: args{
				featureMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": false,
				},
				valueMap: map[string]bool{
					"ID_ABSOLUTE_EXPEL": true,
				},
			},
			wantErr: true,
		},
		{
			name: "write sched_features failed 1",
			args: args{
				featureMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": false,
				},
				valueMap: map[string]bool{
					"ID_ABSOLUTE_EXPEL": false,
				},
			},
			wantErr: true,
		},
		{
			name: "no changed feature to write",
			args: args{
				featureMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": false,
				},
				valueMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": false,
				},
			},
			wantErr: false,
		},
		{
			name: "update sched_features correctly",
			prepareFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				helper.WriteFileContents(featurePath, "ID_EXPELLER_SHARE_CORE NO_ID_ABSOLUTE_EXPEL")
			},
			args: args{
				featureMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": true,
					"ID_ABSOLUTE_EXPEL":      false,
				},
				valueMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": false,
				},
			},
			wantErr: false,
			wantFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				s := helper.ReadFileContents(featurePath)
				assert.Equal(helper.t, s, "NO_ID_EXPELLER_SHARE_CORE\n")
			},
		},
		{
			name: "update sched_features correctly",
			prepareFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				helper.WriteFileContents(featurePath, "ID_EXPELLER_SHARE_CORE NO_ID_ABSOLUTE_EXPEL")
			},
			args: args{
				featureMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": true,
					"ID_ABSOLUTE_EXPEL":      false,
				},
				valueMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": false,
				},
			},
			wantErr: false,
			wantFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				s := helper.ReadFileContents(featurePath)
				assert.Equal(helper.t, s, "NO_ID_EXPELLER_SHARE_CORE\n")
			},
		},
		{
			name: "update sched_features correctly 1",
			prepareFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				helper.WriteFileContents(featurePath, "ID_EXPELLER_SHARE_CORE NO_ID_ABSOLUTE_EXPEL")
			},
			args: args{
				featureMap: map[string]bool{
					"ID_EXPELLER_SHARE_CORE": true,
					"ID_ABSOLUTE_EXPEL":      false,
				},
				valueMap: map[string]bool{
					"ID_ABSOLUTE_EXPEL": true,
				},
			},
			wantErr: false,
			wantFn: func(helper *FileTestUtil) {
				featurePath := SchedFeatures.Path("")
				s := helper.ReadFileContents(featurePath)
				assert.Equal(helper.t, s, "ID_ABSOLUTE_EXPEL\n")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			if tt.prepareFn != nil {
				tt.prepareFn(helper)
			}
			defer helper.Cleanup()
			gotErr := SetSchedFeatures(tt.args.featureMap, tt.args.valueMap)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			if tt.wantFn != nil {
				tt.wantFn(helper)
			}
		})
	}
}
