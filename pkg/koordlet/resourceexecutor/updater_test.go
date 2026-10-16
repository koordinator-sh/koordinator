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
	"context"
	"math"
	"path/filepath"
	"testing"

	systemddbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/stretchr/testify/assert"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type fakeSystemdPropertyWriter struct {
	calls []systemdPropertyCall
}

type systemdPropertyCall struct {
	unitName   string
	runtime    bool
	properties []systemddbus.Property
}

func (f *fakeSystemdPropertyWriter) SetUnitProperties(_ context.Context, unitName string, runtime bool, properties ...systemddbus.Property) error {
	f.calls = append(f.calls, systemdPropertyCall{
		unitName:   unitName,
		runtime:    runtime,
		properties: append([]systemddbus.Property(nil), properties...),
	})
	return nil
}

func TestNewCommonCgroupUpdater(t *testing.T) {
	type fields struct {
		UseCgroupsV2 bool
	}
	type args struct {
		resourceType sysutil.ResourceType
		parentDir    string
		value        string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    ResourceUpdater
		wantErr bool
	}{
		{
			name: "updater not found",
			args: args{
				resourceType: sysutil.ResourceType("UnknownResource"),
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "get cfs quota updater",
			args: args{
				resourceType: sysutil.CPUCFSQuotaName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "-1",
			},
			want: &CgroupResourceUpdater{
				file:       sysutil.CPUCFSQuota,
				parentDir:  "/kubepods.slice/kubepods.slice-podxxx",
				value:      "-1",
				updateFunc: CommonCgroupUpdateFunc,
			},
			wantErr: false,
		},
		{
			name: "get cpu max updater",
			fields: fields{
				UseCgroupsV2: true,
			},
			args: args{
				resourceType: sysutil.CPUCFSQuotaName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "-1",
			},
			want: &CgroupResourceUpdater{
				file:       sysutil.CPUCFSQuotaV2,
				parentDir:  "/kubepods.slice/kubepods.slice-podxxx",
				value:      "-1",
				updateFunc: CommonCgroupUpdateFunc,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.SetCgroupsV2(tt.fields.UseCgroupsV2)

			got, gotErr := NewCommonCgroupUpdater(tt.args.resourceType, tt.args.parentDir, tt.args.value, nil)
			if !tt.wantErr {
				assert.NotNil(t, got)
				assert.Equal(t, tt.want.ResourceType(), got.ResourceType())
				assert.Equal(t, tt.want.Path(), got.Path())
				assert.Equal(t, tt.want.Value(), got.Value())
			}
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func TestCgroupResourceUpdater_Update(t *testing.T) {
	type fields struct {
		UseCgroupsV2 bool
		initialValue string
	}
	type args struct {
		resourceType sysutil.ResourceType
		parentDir    string
		value        string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "update cfs quota",
			fields: fields{
				initialValue: "10000",
			},
			args: args{
				resourceType: sysutil.CPUCFSQuotaName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "-1",
			},
			want:    "-1",
			wantErr: false,
		},
		{
			name: "update memory.min",
			fields: fields{
				UseCgroupsV2: true,
				initialValue: "0",
			},
			args: args{
				resourceType: sysutil.MemoryMinName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "1048576",
			},
			want:    "1048576",
			wantErr: false,
		},
		{
			name: "update failed since file not exist",
			args: args{
				resourceType: sysutil.MemoryMinName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "1048576",
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.SetCgroupsV2(tt.fields.UseCgroupsV2)

			u, gotErr := NewCommonCgroupUpdater(tt.args.resourceType, tt.args.parentDir, tt.args.value, nil)
			assert.NoError(t, gotErr)
			c, ok := u.(*CgroupResourceUpdater)
			assert.True(t, ok)
			if tt.fields.initialValue != "" {
				helper.WriteCgroupFileContents(tt.args.parentDir, c.file, tt.fields.initialValue)
			}

			gotErr = u.update()
			assert.Equal(t, tt.wantErr, gotErr != nil)
			if !tt.wantErr {
				assert.Equal(t, tt.want, helper.ReadCgroupFileContents(c.parentDir, c.file))
			}
		})
	}
}

func TestCgroupResourceUpdater_UpdateSystemdCPUQuotaForQOSPath(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()

	writer := &fakeSystemdPropertyWriter{}
	restoreWriter := setSystemdPropertyWriterForTest(writer)
	defer restoreWriter()

	beQOSDir := "kubepods.slice/kubepods-besteffort.slice"
	helper.WriteCgroupFileContents(beQOSDir, sysutil.CPUCFSQuota, "100000")

	u, err := DefaultCgroupUpdaterFactory.New(sysutil.CPUCFSQuotaName, beQOSDir, "-1", nil)
	assert.NoError(t, err)

	err = u.update()
	assert.NoError(t, err)
	assert.Equal(t, "-1", helper.ReadCgroupFileContents(beQOSDir, sysutil.CPUCFSQuota))
	assert.Len(t, writer.calls, 1)
	assert.Equal(t, "kubepods-besteffort.slice", writer.calls[0].unitName)
	assert.True(t, writer.calls[0].runtime)
	assert.Len(t, writer.calls[0].properties, 1)
	assert.Equal(t, "CPUQuotaPerSecUSec", writer.calls[0].properties[0].Name)
	assert.Equal(t, uint64(math.MaxUint64), writer.calls[0].properties[0].Value.Value())
}

func TestCgroupResourceUpdater_UpdateSystemdOnlyForExactQOSPath(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()

	writer := &fakeSystemdPropertyWriter{}
	restoreWriter := setSystemdPropertyWriterForTest(writer)
	defer restoreWriter()

	podDir := "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxx.slice"
	helper.WriteCgroupFileContents(podDir, sysutil.CPUCFSQuota, "100000")

	u, err := DefaultCgroupUpdaterFactory.New(sysutil.CPUCFSQuotaName, podDir, "200000", nil)
	assert.NoError(t, err)

	err = u.update()
	assert.NoError(t, err)
	assert.Equal(t, "200000", helper.ReadCgroupFileContents(podDir, sysutil.CPUCFSQuota))
	assert.Empty(t, writer.calls)
}

func TestSystemdPropertyForCgroupResource(t *testing.T) {
	tests := []struct {
		name          string
		resourceType  sysutil.ResourceType
		value         string
		wantName      string
		wantValue     uint64
		wantSupported bool
	}{
		{
			name:          "finite cpu quota",
			resourceType:  sysutil.CPUCFSQuotaName,
			value:         "200000",
			wantName:      "CPUQuotaPerSecUSec",
			wantValue:     2000000,
			wantSupported: true,
		},
		{
			name:          "cpu quota max",
			resourceType:  sysutil.CPUCFSQuotaName,
			value:         "max",
			wantName:      "CPUQuotaPerSecUSec",
			wantValue:     uint64(math.MaxUint64),
			wantSupported: true,
		},
		{
			name:          "memory high max",
			resourceType:  sysutil.MemoryHighName,
			value:         "max",
			wantName:      "MemoryHigh",
			wantValue:     uint64(math.MaxUint64),
			wantSupported: true,
		},
		{
			name:          "unsupported resource",
			resourceType:  sysutil.CPUSharesName,
			value:         "1024",
			wantSupported: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			property, supported, err := systemdPropertyForCgroupResource(tt.resourceType, tt.value)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantSupported, supported)
			if !tt.wantSupported {
				return
			}
			assert.Equal(t, tt.wantName, property.Name)
			assert.Equal(t, tt.wantValue, property.Value.Value())
		})
	}
}

func TestSystemBusAddressUsesHostRunRoot(t *testing.T) {
	helper := sysutil.NewFileTestUtil(t)
	defer helper.Cleanup()
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "")

	oldRunRootDir := sysutil.Conf.RunRootDir
	oldVarRunRootDir := sysutil.Conf.VarRunRootDir
	defer func() {
		sysutil.Conf.RunRootDir = oldRunRootDir
		sysutil.Conf.VarRunRootDir = oldVarRunRootDir
	}()

	sysutil.Conf.RunRootDir = filepath.Join(helper.TempDir, "host-run")
	sysutil.Conf.VarRunRootDir = filepath.Join(helper.TempDir, "host-var-run")
	socketPath := filepath.Join(sysutil.Conf.RunRootDir, "dbus/system_bus_socket")
	helper.WriteFileContents(socketPath, "")

	assert.Equal(t, "unix:path="+socketPath, systemBusAddress())
}

func TestCgroupResourceUpdater_MergeUpdate(t *testing.T) {
	type fields struct {
		UseCgroupsV2 bool
		initialValue string
	}
	type args struct {
		resourceType sysutil.ResourceType
		parentDir    string
		value        string
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		wantErr    bool
		wantMerged string
		wantFinal  string
	}{
		{
			name: "merge update cfs quota",
			fields: fields{
				initialValue: "10000",
			},
			args: args{
				resourceType: sysutil.CPUCFSQuotaName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "-1",
			},
			wantMerged: "-1",
			wantFinal:  "-1",
			wantErr:    false,
		},
		{
			name: "merge update cfs quota (case 1)",
			fields: fields{
				initialValue: "-1",
			},
			args: args{
				resourceType: sysutil.CPUCFSQuotaName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "10000",
			},
			wantMerged: "-1",
			wantFinal:  "10000",
			wantErr:    false,
		},
		{
			name: "failed to merge update cfs quota on cgroup v2",
			fields: fields{
				UseCgroupsV2: true,
				initialValue: "invalid content",
			},
			args: args{
				resourceType: sysutil.CPUCFSQuotaName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "-1",
			},
			wantErr: true,
		},
		{
			name: "merge update cfs quota on cgroup v2",
			fields: fields{
				UseCgroupsV2: true,
				initialValue: "10000 100000",
			},
			args: args{
				resourceType: sysutil.CPUCFSQuotaName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "-1",
			},
			wantMerged: "max", // should be `max 100000` in the real cgroup
			wantFinal:  "max", // should be `max 100000` in the real cgroup
			wantErr:    false,
		},
		{
			name: "merge update cfs quota on cgroup v2 (case 1)",
			fields: fields{
				UseCgroupsV2: true,
				initialValue: "max 100000",
			},
			args: args{
				resourceType: sysutil.CPUCFSQuotaName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "200000",
			},
			wantMerged: "max 100000",
			wantFinal:  "200000", // should be `200000 100000` in the real cgroup
			wantErr:    false,
		},
		{
			name: "merge update min",
			fields: fields{
				UseCgroupsV2: true,
				initialValue: "1048576",
			},
			args: args{
				resourceType: sysutil.MemoryMinName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "2097152",
			},
			wantMerged: "2097152",
			wantFinal:  "2097152",
			wantErr:    false,
		},
		{
			name: "merge update min (case 1)",
			fields: fields{
				UseCgroupsV2: true,
				initialValue: "2097152",
			},
			args: args{
				resourceType: sysutil.MemoryMinName,
				parentDir:    "/kubepods.slice/kubepods.slice-podxxx",
				value:        "1048576",
			},
			wantMerged: "2097152",
			wantFinal:  "1048576",
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.SetCgroupsV2(tt.fields.UseCgroupsV2)

			u, gotErr := DefaultCgroupUpdaterFactory.New(tt.args.resourceType, tt.args.parentDir, tt.args.value, nil)
			assert.NoError(t, gotErr)
			c, ok := u.(*CgroupResourceUpdater)
			assert.True(t, ok)
			if tt.fields.initialValue != "" {
				helper.WriteCgroupFileContents(tt.args.parentDir, c.file, tt.fields.initialValue)
			}

			mergedUpdater, gotErr := u.MergeUpdate()
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			if tt.wantErr {
				return
			}
			assert.NotNil(t, mergedUpdater)
			gotErr = mergedUpdater.update()
			assert.NoError(t, gotErr)
			assert.Equal(t, tt.wantMerged, helper.ReadCgroupFileContents(c.parentDir, c.file))
			gotErr = u.update()
			assert.NoError(t, gotErr)
			assert.Equal(t, tt.wantFinal, helper.ReadCgroupFileContents(c.parentDir, c.file))
		})
	}
}

func TestCgroupResourceUpdater_MergeUpdateSystemdMemoryQOS(t *testing.T) {
	tests := []struct {
		name         string
		resourceType sysutil.ResourceType
		file         sysutil.Resource
		propertyName string
	}{
		{
			name:         "memory.min",
			resourceType: sysutil.MemoryMinName,
			file:         sysutil.MemoryMinV2,
			propertyName: "MemoryMin",
		},
		{
			name:         "memory.low",
			resourceType: sysutil.MemoryLowName,
			file:         sysutil.MemoryLowV2,
			propertyName: "MemoryLow",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.SetCgroupsV2(true)

			writer := &fakeSystemdPropertyWriter{}
			restoreWriter := setSystemdPropertyWriterForTest(writer)
			defer restoreWriter()

			burstableQOSDir := "kubepods.slice/kubepods-burstable.slice"
			helper.WriteCgroupFileContents(burstableQOSDir, tt.file, "1024")

			u, err := DefaultCgroupUpdaterFactory.New(tt.resourceType, burstableQOSDir, "2048", nil)
			assert.NoError(t, err)

			mergedUpdater, err := u.MergeUpdate()
			assert.NoError(t, err)
			assert.NotNil(t, mergedUpdater)
			assert.Equal(t, "2048", helper.ReadCgroupFileContents(burstableQOSDir, tt.file))
			assert.Len(t, writer.calls, 1)
			assert.Equal(t, "kubepods-burstable.slice", writer.calls[0].unitName)
			assert.True(t, writer.calls[0].runtime)
			assert.Len(t, writer.calls[0].properties, 1)
			assert.Equal(t, tt.propertyName, writer.calls[0].properties[0].Name)
			assert.Equal(t, uint64(2048), writer.calls[0].properties[0].Value.Value())
		})
	}
}

func TestDefaultResourceUpdater_Update(t *testing.T) {
	type fields struct {
		initialValue string
	}
	type args struct {
		file   string
		subDir string
		value  string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "update test file successfully",
			fields: fields{
				initialValue: "1234",
			},
			args: args{
				file:   "test_file",
				subDir: "test_dir",
				value:  "5678",
			},
			want:    "5678",
			wantErr: false,
		},
		{
			name: "update test file successfully 1",
			fields: fields{
				initialValue: "5678",
			},
			args: args{
				file:   "test_file",
				subDir: "test_dir",
				value:  "5678",
			},
			want:    "5678",
			wantErr: false,
		},
		{
			name: "update failed since file not exist",
			args: args{
				file:   "test_file1",
				subDir: "test_dir1",
				value:  "5678",
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()

			file := filepath.Join(helper.TempDir, tt.args.subDir, tt.args.file)
			u, gotErr := NewCommonDefaultUpdater(file, file, tt.args.value, nil)
			assert.NoError(t, gotErr)
			_, ok := u.(*DefaultResourceUpdater)
			assert.True(t, ok)
			if tt.fields.initialValue != "" {
				helper.WriteFileContents(filepath.Join(tt.args.subDir, tt.args.file), tt.fields.initialValue)
			}

			gotErr = u.update()
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			if !tt.wantErr {
				assert.Equal(t, tt.want, helper.ReadFileContents(filepath.Join(tt.args.subDir, tt.args.file)))
			}
		})
	}
}
