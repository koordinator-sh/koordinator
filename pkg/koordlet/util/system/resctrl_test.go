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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ReadResctrlTasksMap(t *testing.T) {
	type args struct {
		groupPath string
	}
	type fields struct {
		tasksStr    string
		invalidPath bool
	}
	tests := []struct {
		name    string
		args    args
		fields  fields
		want    map[int32]struct{}
		wantErr bool
	}{
		{
			name:    "do not panic but throw an error for empty input",
			want:    map[int32]struct{}{},
			wantErr: false,
		},
		{
			name:    "invalid path",
			fields:  fields{invalidPath: true},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "parse correctly",
			fields:  fields{tasksStr: "101\n111\n"},
			want:    map[int32]struct{}{101: {}, 111: {}},
			wantErr: false,
		},
		{
			name:    "parse correctly 1",
			args:    args{groupPath: "BE"},
			fields:  fields{tasksStr: "101\n111\n"},
			want:    map[int32]struct{}{101: {}, 111: {}},
			wantErr: false,
		},
		{
			name:    "parse error for invalid task str",
			fields:  fields{tasksStr: "101\n1aa\n"},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sysFSRootDir := t.TempDir()
			resctrlDir := filepath.Join(sysFSRootDir, ResctrlDir, tt.args.groupPath)
			err := os.MkdirAll(resctrlDir, 0700)
			assert.NoError(t, err)

			tasksPath := filepath.Join(resctrlDir, ResctrlTasksName)
			err = os.WriteFile(tasksPath, []byte(tt.fields.tasksStr), 0666)
			assert.NoError(t, err)

			Conf = &Config{
				SysFSRootDir: sysFSRootDir,
			}
			if tt.fields.invalidPath {
				Conf.SysFSRootDir = "invalidPath"
			}

			got, err := ReadResctrlTasksMap(tt.args.groupPath)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResctrlSchemataRaw(t *testing.T) {
	type fields struct {
		cacheids  []int
		l3Num     int
		l3Mask    string
		mbPercent string
	}
	tests := []struct {
		name         string
		fields       fields
		wantL3String string
		wantMBString string
	}{
		{
			name: "new l3 schemata",
			fields: fields{
				cacheids: []int{0},
				l3Num:    1,
				l3Mask:   "f",
			},
			wantL3String: "L3:0=f;\n",
		},
		{
			name: "new mba schemata",
			fields: fields{
				cacheids:  []int{0},
				l3Num:     1,
				mbPercent: "90",
			},
			wantMBString: "MB:0=90;\n",
		},
		{
			name: "new l3 with mba schemata",
			fields: fields{
				cacheids:  []int{0, 1},
				l3Num:     2,
				l3Mask:    "fff",
				mbPercent: "100",
			},
			wantL3String: "L3:0=fff;1=fff;\n",
			wantMBString: "MB:0=100;1=100;\n",
		},
		{
			name: "non-contiguous cache ids",
			fields: fields{
				cacheids:  []int{0, 8},
				l3Num:     2,
				l3Mask:    "fff",
				mbPercent: "100",
			},
			wantL3String: "L3:0=fff;8=fff;\n",
			wantMBString: "MB:0=100;8=100;\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewResctrlSchemataRaw(tt.fields.cacheids)
			r.WithL3Num(tt.fields.l3Num).WithL3Mask(tt.fields.l3Mask).WithMB(tt.fields.mbPercent)
			if len(tt.fields.l3Mask) > 0 {
				got := r.L3String()
				assert.Equal(t, tt.wantL3String, got)
			}
			if len(tt.fields.mbPercent) > 0 {
				got1 := r.MBString()
				assert.Equal(t, tt.wantMBString, got1)
			}
		})
	}
}

func Test_CheckAndTryEnableResctrlCat(t *testing.T) {
	type fields struct {
		cbmStr      string
		invalidPath bool
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name:    "return disabled for a invalid path",
			fields:  fields{invalidPath: true},
			wantErr: true,
		},
		{
			name:    "return enabled for a valid l3_cbm",
			fields:  fields{cbmStr: "3f"},
			wantErr: false,
		},
		// TODO: add mount case
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()

			cbmPath := ResctrlL3CbmMask.Path("")
			helper.WriteFileContents(cbmPath, tt.fields.cbmStr)
			if tt.fields.invalidPath {
				Conf.SysFSRootDir = "invalidPath"
			}

			gotErr := CheckAndTryEnableResctrlCat()

			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}

func TestCheckResctrlSchemataValid(t *testing.T) {
	type fields struct {
		isSchemataExist bool
		schemataStr     string
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name:    "check failed when schemata dir not exist",
			wantErr: true,
		},
		{
			name: "check failed when schemata content is empty",
			fields: fields{
				isSchemataExist: true,
				schemataStr:     ``,
			},
			wantErr: true,
		},
		{
			name: "check failed when schemata content is missing L3 CAT",
			fields: fields{
				isSchemataExist: true,
				schemataStr:     `MB:0=100;1=100`,
			},
			wantErr: true,
		},
		{
			name: "check successfully when schemata content is valid",
			fields: fields{
				isSchemataExist: true,
				schemataStr: `MB:0=100;1=100
L3:0=fff;1=fff`,
			},
			wantErr: false,
		},
		{
			name: "check successfully when schemata content is valid 1",
			fields: fields{
				isSchemataExist: true,
				schemataStr: `    L3:0=ffff;1=ffff;2=ffff;3=ffff;4=ffff
    MB:0=2048;1=2048;2=2048;3=2048;4=2048`,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.isSchemataExist {
				schemataPath := ResctrlSchemata.Path("")
				helper.WriteFileContents(schemataPath, tt.fields.schemataStr)
			}

			gotErr := CheckResctrlSchemataValid()
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
		})
	}
}

func TestGetVendorIDByCPUInfo(t *testing.T) {
	type args struct {
		content string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "test amd",
			args: args{
				content: "vendor_id       : AuthenticAMD\n",
			},
			want:    AMD_VENDOR_ID,
			wantErr: false,
		},
		{
			name: "test amd on one line",
			args: args{
				content: "vendor_id       : AuthenticAMD",
			},
			want:    AMD_VENDOR_ID,
			wantErr: false,
		},
		{
			name: "test intel",
			args: args{
				content: "vendor_id       : GenuineIntel",
			},
			want:    "GenuineIntel",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			helper.WriteProcSubFileContents("cpuinfo", tt.args.content)
			got, err := GetVendorIDByCPUInfo(filepath.Join(Conf.ProcRootDir, "cpuinfo"))
			if (err != nil) != tt.wantErr {
				t.Errorf("GetVendorIDByCPUInfo() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetVendorIDByCPUInfo() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_isResctrlAvailableByCpuInfo(t *testing.T) {
	type field struct {
		cpuInfoContents string
	}

	tests := []struct {
		name               string
		field              field
		expectIsCatFlagSet bool
		expectIsMbaFlagSet bool
	}{
		{
			name: "testResctrlEnable",
			field: field{
				cpuInfoContents: "flags		: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush dts acpi mmx fxsr sse sse2 ss ht tm pbe syscall nx pdpe1gb rdtscp lm constant_tsc art arch_perfmon pebs bts rep_good nopl xtopology nonstop_tsc cpuid aperfmperf pni pclmulqdq dtes64 monitor ds_cpl vmx smx est tm2 ssse3 sdbg fma cx16 xtpr pdcm pcid dca sse4_1 sse4_2 x2apic movbe popcnt tsc_deadline_timer aes xsave avx f16c rdrand lahf_lm abm 3dnowprefetch cpuid_fault epb cat_l3 cdp_l3 invpcid_single intel_ppin ssbd mba ibrs ibpb stibp ibrs_enhanced tpr_shadow vnmi flexpriority ept vpid ept_ad tsc_adjust bmi1 avx2 smep bmi2 erms invpcid cqm mpx rdt_a avx512f avx512dq rdseed adx smap clflushopt clwb intel_pt avx512cd avx512bw avx512vl xsaveopt xsavec xgetbv1 xsaves cqm_llc cqm_occup_llc cqm_mbm_total cqm_mbm_local dtherm ida arat pln pts pku ospke avx512_vnni md_clear flush_l1d arch_capabilities",
			},
			expectIsCatFlagSet: true,
			expectIsMbaFlagSet: true,
		},
		{
			name: "testResctrlUnable",
			field: field{
				cpuInfoContents: "flags		: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush mmx fxsr sse sse2 ss ht syscall nx pdpe1gb rdtscp lm constant_tsc rep_good nopl nonstop_tsc cpuid tsc_known_freq pni pclmulqdq monitor ssse3 fma cx16 pcid sse4_1 sse4_2 x2apic movbe popcnt aes xsave avx f16c rdrand hypervisor lahf_lm abm 3dnowprefetch cpuid_fault invpcid_single ibrs_enhanced tsc_adjust bmi1 avx2 smep bmi2 erms invpcid avx512f avx512dq rdseed adx smap avx512ifma clflushopt clwb avx512cd sha_ni avx512bw avx512vl xsaveopt xsavec xgetbv1 xsaves wbnoinvd arat avx512vbmi pku ospke avx512_vbmi2 gfni vaes vpclmulqdq avx512_vnni avx512_bitalg avx512_vpopcntdq rdpid fsrm arch_capabilities",
			},
			expectIsCatFlagSet: false,
			expectIsMbaFlagSet: false,
		},
		{
			name: "testContentsInvalid",
			field: field{
				cpuInfoContents: "invalid contents",
			},
			expectIsCatFlagSet: false,
			expectIsMbaFlagSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.WriteProcSubFileContents("cpuinfo", tt.field.cpuInfoContents)

			gotIsCatFlagSet, gotIsMbaFlagSet, err := isResctrlAvailableByCpuInfo(GetCPUInfoPath())
			assert.NoError(t, err, "testError")
			assert.Equal(t, tt.expectIsCatFlagSet, gotIsCatFlagSet, "checkIsCatFlagSet")
			assert.Equal(t, tt.expectIsMbaFlagSet, gotIsMbaFlagSet, "checkIsMbaFlagSet")
		})
	}
}

func Test_isResctrlAvailableByKernelCmd(t *testing.T) {
	type args struct {
		content string
	}
	tests := []struct {
		name    string
		args    args
		wantCat bool
		wantMba bool
	}{
		{
			name: "testResctrlEnable",
			args: args{
				content: "BOOT_IMAGE=/boot/vmlinuz-4.19.91-24.1.al7.x86_64 root=UUID=231efa3b-302b-4e82-9445-0f7d5d353dda rdt=cmt,l3cat,l3cdp,mba",
			},
			wantCat: true,
			wantMba: true,
		},
		{
			name: "testResctrlCatDisable",
			args: args{
				content: "BOOT_IMAGE=/boot/vmlinuz-4.19.91-24.1.al7.x86_64 root=UUID=231efa3b-302b-4e82-9445-0f7d5d353dda rdt=cmt,mba,l3cdp",
			},
			wantCat: false,
			wantMba: true,
		},
		{
			name: "testResctrlMBADisable",
			args: args{
				content: "BOOT_IMAGE=/boot/vmlinuz-4.19.91-24.1.al7.x86_64 root=UUID=231efa3b-302b-4e82-9445-0f7d5d353dda rdt=cmt,l3cat,l3cdp",
			},
			wantCat: true,
			wantMba: false,
		},
		{
			name: "testContentsInvalid",
			args: args{
				content: "invalid contents",
			},
			wantCat: false,
			wantMba: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			helper.WriteProcSubFileContents("cmdline", tt.args.content)
			isCatFlagSet, isMbaFlagSet, err := isResctrlAvailableByKernelCmd(filepath.Join(Conf.ProcRootDir, KernelCmdlineFileName))
			assert.NoError(t, err)
			assert.Equal(t, tt.wantCat, isCatFlagSet)
			assert.Equal(t, tt.wantMba, isMbaFlagSet)
		})
	}
}
