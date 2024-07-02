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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestNewResctrlReader(t *testing.T) {
	type args struct {
		content string
	}
	tests := []struct {
		name    string
		args    args
		want    reflect.Type
		wantErr bool
	}{
		{
			name: "test amd",
			args: args{
				content: "vendor_id       : AuthenticAMD\n",
			},
			want:    reflect.TypeOf(&ResctrlAMDReader{}),
			wantErr: false,
		},
		{
			name: "test intel",
			args: args{
				content: "vendor_id       : GenuineIntel",
			},
			want:    reflect.TypeOf(&ResctrlRDTReader{}),
			wantErr: false,
		},
		{
			name: "test arm",
			args: args{
				content: "vendor_id       : arm",
			},
			want:    reflect.TypeOf(&fakeReader{}),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			helper.WriteProcSubFileContents("cpuinfo", tt.args.content)

			rr := NewResctrlReader()
			assert.Equal(t, tt.want, reflect.TypeOf(rr))
		})
	}
}

// just for x86 system
func TestResctrlReader(t *testing.T) {
	type args struct {
		mmds map[string]system.MockMonData
		qos  string
	}

	// llc result
	llcTests := []struct {
		name    string
		args    args
		want    map[CacheId]uint64
		wantErr bool
	}{
		{
			name: "root resctrl get llc result",
			args: args{
				mmds: map[string]system.MockMonData{
					"": {
						CacheItems: map[int]system.MockCacheItem{
							0: {
								"llc_occupancy":   1,
								"mbm_local_bytes": 2,
								"mbm_total_bytes": 3,
							},
							1: {
								"llc_occupancy":   4,
								"mbm_local_bytes": 5,
								"mbm_total_bytes": 6,
							},
						},
					},
				},
				qos: "",
			},
			want: map[CacheId]uint64{
				0: 1,
				1: 4,
			},
			wantErr: false,
		},
		{
			name: "be qos get llc result",
			args: args{
				mmds: map[string]system.MockMonData{
					"": {
						CacheItems: map[int]system.MockCacheItem{
							0: {
								"llc_occupancy":   1,
								"mbm_local_bytes": 2,
								"mbm_total_bytes": 3,
							},
							1: {
								"llc_occupancy":   4,
								"mbm_local_bytes": 5,
								"mbm_total_bytes": 6,
							},
						},
					},
					"BE": {
						CacheItems: map[int]system.MockCacheItem{
							0: {
								"llc_occupancy":   11,
								"mbm_local_bytes": 21,
								"mbm_total_bytes": 31,
							},
							1: {
								"llc_occupancy":   41,
								"mbm_local_bytes": 51,
								"mbm_total_bytes": 61,
							},
						},
					},
				},
				qos: "BE",
			},
			want: map[CacheId]uint64{
				0: 11,
				1: 41,
			},
			wantErr: false,
		},
	}
	for _, test := range llcTests {
		t.Run("test resctrl llc collect", func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()

			// add cpuinfo for x86 cpuid
			helper.WriteProcSubFileContents("cpuinfo", "vendor_id       : GenuineIntel\nflags           : fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush dts acpi mmx fxsr sse sse2 ss ht tm pbe syscall nx pdpe1gb rdtscp lm constant_tsc art arch_perfmon pebs bts rep_good nopl xtopology nonstop_tsc cpuid aperfmperf pni pclmulqdq dtes64 ds_cpl vmx smx est tm2 ssse3 sdbg fma cx16 xtpr pdcm pcid dca sse4_1 sse4_2 x2apic movbe popcnt tsc_deadline_timer aes xsave avx f16c rdrand lahf_lm abm 3dnowprefetch cpuid_fault epb cat_l3 invpcid_single intel_ppin ssbd mba ibrs ibpb stibp ibrs_enhanced tpr_shadow vnmi flexpriority ept vpid ept_ad fsgsbase tsc_adjust bmi1 avx2 smep bmi2 erms invpcid cqm rdt_a avx512f avx512dq rdseed adx smap avx512ifma clflushopt clwb intel_pt avx512cd sha_ni avx512bw avx512vl xsaveopt xsavec xgetbv1 xsaves cqm_llc cqm_occup_llc cqm_mbm_total cqm_mbm_local split_lock_detect wbnoinvd dtherm ida arat pln pts avx512vbmi umip pku ospke avx512_vbmi2 gfni vaes vpclmulqdq avx512_vnni avx512_bitalg tme avx512_vpopcntdq la57 rdpid fsrm md_clear pconfig flush_l1d arch_capabilities")

			for ctrlGrp, mmd := range test.args.mmds {
				system.TestingPrepareResctrlMondata(t, system.Conf.SysFSRootDir, ctrlGrp, mmd)
			}

			data, err := NewResctrlReader().ReadResctrlL3Stat(test.args.qos)
			assert.NoError(t, err)

			for cacheId, value := range data {
				assert.Equal(t, test.want[cacheId], value)
			}
		})
	}

	mbTests := []struct {
		name    string
		args    args
		want    map[CacheId]system.MBStatData
		wantErr bool
	}{
		{
			name: "root resctrl get llc result",
			args: args{
				mmds: map[string]system.MockMonData{
					"": {
						CacheItems: map[int]system.MockCacheItem{
							0: {
								"llc_occupancy":   1,
								"mbm_local_bytes": 2,
								"mbm_total_bytes": 3,
							},
							1: {
								"llc_occupancy":   4,
								"mbm_local_bytes": 5,
								"mbm_total_bytes": 6,
							},
						},
					},
				},
				qos: "",
			},
			want: map[CacheId]system.MBStatData{
				0: {
					"mbm_local_bytes": 2,
					"mbm_total_bytes": 3,
				},
				1: {
					"mbm_local_bytes": 5,
					"mbm_total_bytes": 6,
				},
			},
			wantErr: false,
		},
		{
			name: "be qos get llc result",
			args: args{
				mmds: map[string]system.MockMonData{
					"": {
						CacheItems: map[int]system.MockCacheItem{
							0: {
								"llc_occupancy":   1,
								"mbm_local_bytes": 2,
								"mbm_total_bytes": 3,
							},
							1: {
								"llc_occupancy":   4,
								"mbm_local_bytes": 5,
								"mbm_total_bytes": 6,
							},
						},
					},
					"BE": {
						CacheItems: map[int]system.MockCacheItem{
							0: {
								"llc_occupancy":   11,
								"mbm_local_bytes": 21,
								"mbm_total_bytes": 31,
							},
							1: {
								"llc_occupancy":   41,
								"mbm_local_bytes": 51,
								"mbm_total_bytes": 61,
							},
						},
					},
				},
				qos: "BE",
			},
			want: map[CacheId]system.MBStatData{
				0: {
					"mbm_local_bytes": 21,
					"mbm_total_bytes": 31,
				},
				1: {
					"mbm_local_bytes": 51,
					"mbm_total_bytes": 61,
				},
			},
			wantErr: false,
		},
	}
	for _, test := range mbTests {
		t.Run("test resctrl mb collect", func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()

			// add cpuinfo for intel cpuid
			helper.WriteProcSubFileContents("cpuinfo", "vendor_id       : GenuineIntel\nflags           : fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush dts acpi mmx fxsr sse sse2 ss ht tm pbe syscall nx pdpe1gb rdtscp lm constant_tsc art arch_perfmon pebs bts rep_good nopl xtopology nonstop_tsc cpuid aperfmperf pni pclmulqdq dtes64 ds_cpl vmx smx est tm2 ssse3 sdbg fma cx16 xtpr pdcm pcid dca sse4_1 sse4_2 x2apic movbe popcnt tsc_deadline_timer aes xsave avx f16c rdrand lahf_lm abm 3dnowprefetch cpuid_fault epb cat_l3 invpcid_single intel_ppin ssbd mba ibrs ibpb stibp ibrs_enhanced tpr_shadow vnmi flexpriority ept vpid ept_ad fsgsbase tsc_adjust bmi1 avx2 smep bmi2 erms invpcid cqm rdt_a avx512f avx512dq rdseed adx smap avx512ifma clflushopt clwb intel_pt avx512cd sha_ni avx512bw avx512vl xsaveopt xsavec xgetbv1 xsaves cqm_llc cqm_occup_llc cqm_mbm_total cqm_mbm_local split_lock_detect wbnoinvd dtherm ida arat pln pts avx512vbmi umip pku ospke avx512_vbmi2 gfni vaes vpclmulqdq avx512_vnni avx512_bitalg tme avx512_vpopcntdq la57 rdpid fsrm md_clear pconfig flush_l1d arch_capabilities")

			for ctrlGrp, mmd := range test.args.mmds {
				system.TestingPrepareResctrlMondata(t, system.Conf.SysFSRootDir, ctrlGrp, mmd)
			}

			data, err := NewResctrlReader().ReadResctrlMBStat(test.args.qos)
			assert.NoError(t, err)

			for cacheId, value := range data {
				assert.Equal(t, test.want[cacheId], value)
			}
		})
	}
}
