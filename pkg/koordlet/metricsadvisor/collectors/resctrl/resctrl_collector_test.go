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

package resctrl

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mockmetriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestNewResctrlCollector(t *testing.T) {
	type args struct {
		cfg            *framework.Config
		statesInformer statesinformer.StatesInformer
		metricCache    metriccache.MetricCache
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "new-resctrl-collector",
			args: args{
				cfg:            framework.NewDefaultConfig(),
				statesInformer: nil,
				metricCache:    nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := &framework.Options{
				Config:         tt.args.cfg,
				StatesInformer: tt.args.statesInformer,
				MetricCache:    tt.args.metricCache,
			}
			if got := New(opt); got == nil {
				t.Errorf("NewResctrlCollector() = %v", got)
			}
		})
	}
}

func Test_collectQosResctrlStatNoErr(t *testing.T) {
	type args struct {
		platformFlags string // platformFlags is the platform flags for the resctrl
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockStatesInformer := mockstatesinformer.NewMockStatesInformer(ctrl)
	mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)
	mockStatesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{}).AnyTimes()
	mockStatesInformer.EXPECT().HasSynced().Return(true).AnyTimes()
	appender := mockmetriccache.NewMockAppender(ctrl)
	mockMetricCache.EXPECT().Appender().Return(appender).AnyTimes()
	appender.EXPECT().Append(gomock.Any()).Return(nil).AnyTimes()
	appender.EXPECT().Commit().Return(nil).AnyTimes()

	// create mock resctrl file system data
	mmds := map[string]system.MockMonData{
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
	}

	platformTest := []struct {
		args    args
		wantErr bool
	}{
		{
			args: args{
				platformFlags: "vendor_id       : GenuineIntel\nflags           : fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush dts acpi mmx fxsr sse sse2 ss ht tm pbe syscall nx pdpe1gb rdtscp lm constant_tsc art arch_perfmon pebs bts rep_good nopl xtopology nonstop_tsc cpuid aperfmperf pni pclmulqdq dtes64 ds_cpl vmx smx est tm2 ssse3 sdbg fma cx16 xtpr pdcm pcid dca sse4_1 sse4_2 x2apic movbe popcnt tsc_deadline_timer aes xsave avx f16c rdrand lahf_lm abm 3dnowprefetch cpuid_fault epb cat_l3 invpcid_single intel_ppin ssbd mba ibrs ibpb stibp ibrs_enhanced tpr_shadow vnmi flexpriority ept vpid ept_ad fsgsbase tsc_adjust bmi1 avx2 smep bmi2 erms invpcid cqm rdt_a avx512f avx512dq rdseed adx smap avx512ifma clflushopt clwb intel_pt avx512cd sha_ni avx512bw avx512vl xsaveopt xsavec xgetbv1 xsaves cqm_llc cqm_occup_llc cqm_mbm_total cqm_mbm_local split_lock_detect wbnoinvd dtherm ida arat pln pts avx512vbmi umip pku ospke avx512_vbmi2 gfni vaes vpclmulqdq avx512_vnni avx512_bitalg tme avx512_vpopcntdq la57 rdpid fsrm md_clear pconfig flush_l1d arch_capabilities",
			},
			wantErr: false,
		},
		{
			args: args{
				platformFlags: "vendor_id       : AuthenticAMD\nflags           : fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush mmx fxsr sse sse2 ht syscall nx mmxext fxsr_opt pdpe1gb rdtscp lm constant_tsc rep_good nopl nonstop_tsc extd_apicid pni pclmulqdq monitor ssse3 fma cx16 sse4_1 sse4_2 movbe popcnt aes xsave avx f16c rdrand lahf_lm cmp_legacy svm extapic cr8_legacy abm sse4a misalignsse 3dnowprefetch osvw ibs xop skinit wdt lwp fma4 tce nodeid_msr tbm topoext perfctr_core perfctr_nb bpext perfctr_llc mwaitx cpb hw_pstate ssbd ibpb vmmcall fsgsbase bmi1 avx2 smep bmi2 rdseed adx smap clflushopt sha_ni xsaveopt xsavec xgetbv1 xsaves clzero irperf xsaveerptr arat npt lbrv svm_lock nrip_save tsc_scale vmcb_clean flushbyasid decodeassists pausefilter pfthreshold avic v_vmsave_vmload vgif overflow_recov succor smca",
			},
			wantErr: false,
		},
		{
			args: args{
				platformFlags: "",
			},
			wantErr: false,
		},
	}

	for _, test := range platformTest {
		t.Run("test collect qos resctrl stat", func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.WriteProcSubFileContents("cpuinfo", test.args.platformFlags)
			for ctrlGrp, mmd := range mmds {
				system.TestingPrepareResctrlMondata(t, system.Conf.SysFSRootDir, ctrlGrp, mmd)
			}

			collector := New(&framework.Options{
				Config:         framework.NewDefaultConfig(),
				StatesInformer: mockStatesInformer,
				MetricCache:    mockMetricCache,
				CgroupReader:   resourceexecutor.NewCgroupReader(),
			})

			c := collector.(*resctrlCollector)
			if !test.wantErr {
				assert.NotPanics(t, func() {
					c.collectQoSResctrlStat()
				})
			} else {
				assert.Panics(t, func() {
					c.collectQoSResctrlStat()
				})
			}
		})
	}
}
