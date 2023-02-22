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
	"path"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSystemResource(t *testing.T) {
	type fields struct {
		resource    Resource
		createdFile bool
		value       int64
	}
	tests := []struct {
		name             string
		fields           fields
		wantPath         string
		wantSupported    bool
		wantValid        bool
		wantResourceType string
	}{
		{
			name: "resource value valid",
			fields: fields{
				resource:    MinFreeKbytes,
				createdFile: false,
				value:       5 * 1024 * 1024,
			},
			wantPath:         path.Join(Conf.ProcRootDir, ProcSysVmRelativePath, MinFreeKbytesFileName),
			wantSupported:    true,
			wantValid:        true,
			wantResourceType: MinFreeKbytesFileName,
		},
		{
			name: "resource value notValid and must be supported",
			fields: fields{
				resource:    MinFreeKbytes,
				createdFile: false,
				value:       1024,
			},
			wantPath:         path.Join(Conf.ProcRootDir, ProcSysVmRelativePath, MinFreeKbytesFileName),
			wantSupported:    true,
			wantValid:        false,
			wantResourceType: MinFreeKbytesFileName,
		},
		{
			name: "resource not supported",
			fields: fields{
				resource:    NewCommonSystemResource(ProcSysVmRelativePath, MinFreeKbytesFileName, GetProcRootDir).WithValidator(MinFreeKbytesValidator).WithCheckSupported(SupportedIfFileExists),
				createdFile: false,
				value:       5 * 1024 * 1024,
			},
			wantPath:         path.Join(Conf.ProcRootDir, ProcSysVmRelativePath, MinFreeKbytesFileName),
			wantSupported:    false,
			wantValid:        true,
			wantResourceType: MinFreeKbytesFileName,
		},
		{
			name: "resource supported",
			fields: fields{
				resource:    NewCommonSystemResource(ProcSysVmRelativePath, MinFreeKbytesFileName, GetProcRootDir).WithValidator(MinFreeKbytesValidator).WithCheckSupported(SupportedIfFileExists),
				createdFile: true,
				value:       5 * 1024 * 1024,
			},
			wantPath:         path.Join(Conf.ProcRootDir, ProcSysVmRelativePath, MinFreeKbytesFileName),
			wantSupported:    true,
			wantValid:        true,
			wantResourceType: MinFreeKbytesFileName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := NewFileTestUtil(t)
			defer testHelper.Cleanup()

			if tt.fields.createdFile {
				testHelper.WriteFileContents(tt.fields.resource.Path(""), "1000000")
			}
			assert.Equal(t, ResourceType(tt.wantResourceType), tt.fields.resource.ResourceType(), "checkResourceType")
			isValid, _ := tt.fields.resource.IsValid(strconv.FormatInt(tt.fields.value, 10))
			assert.Equal(t, tt.wantValid, isValid, "checkValid")
			isSupported, _ := tt.fields.resource.IsSupported("")
			assert.Equal(t, tt.wantSupported, isSupported)
		})
	}
}
