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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCgroupFileWriteUintIfDifferent(t *testing.T) {
	taskDir := "/"

	type args struct {
		file     CgroupFile
		oldValue string
		newValue string
	}

	tests := []struct {
		name      string
		args      args
		expectOld string
		expectNew string
	}{
		{
			name: "test_diffent_value",
			args: args{
				file:     CPUShares,
				oldValue: "1024",
				newValue: "2",
			},
			expectOld: "1024",
			expectNew: "2",
		},
		{
			name: "test_equal_value",
			args: args{
				file:     CPUShares,
				oldValue: "2",
				newValue: "2",
			},
			expectOld: "2",
			expectNew: "2",
		},
		{
			name: "test_equal_value_for_max_str",
			args: args{
				file:     MemHigh,
				oldValue: CgroupMaxSymbolStr,
				newValue: CgroupMaxValueStr,
			},
			expectOld: CgroupMaxSymbolStr,
			expectNew: CgroupMaxSymbolStr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.CreateCgroupFile(taskDir, tt.args.file)

			err := CgroupFileWriteIfDifferent(taskDir, tt.args.file, tt.args.oldValue)
			assert.NoError(t, err)

			gotOld, _ := CgroupFileRead(taskDir, tt.args.file)
			assert.Equal(t, tt.expectOld, gotOld)

			err = CgroupFileWriteIfDifferent(taskDir, tt.args.file, tt.args.newValue)
			assert.NoError(t, err)
			gotNew, _ := CgroupFileRead(taskDir, tt.args.file)
			assert.Equal(t, tt.expectNew, gotNew)

		})
	}

}

func TestCgroupFileReadInt(t *testing.T) {
	taskDir := "/"
	testingInt64 := int64(1024)
	testingMaxInt64 := int64(math.MaxInt64)
	type args struct {
		file  CgroupFile
		value string
	}
	tests := []struct {
		name      string
		args      args
		expect    *int64
		expectErr bool
	}{
		{
			name: "test_read_success",
			args: args{
				file:  CPUShares,
				value: "1024",
			},
			expect:    &testingInt64,
			expectErr: false,
		},
		{
			name: "test_read_error",
			args: args{
				file:  CPUShares,
				value: "unknown",
			},
			expect:    nil,
			expectErr: true,
		},
		{
			name: "test_read_value_for_max_str",
			args: args{
				file:  MemHigh,
				value: CgroupMaxSymbolStr,
			},
			expect:    &testingMaxInt64,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.CreateCgroupFile(taskDir, tt.args.file)

			err := CgroupFileWrite(taskDir, tt.args.file, tt.args.value)
			assert.NoError(t, err)

			got, gotErr := CgroupFileReadInt(taskDir, tt.args.file)

			assert.Equal(t, tt.expect, got)
			assert.Equal(t, tt.expectErr, gotErr != nil)
		})
	}

}

func Test_CPUIdentityEnabled(t *testing.T) {
	tests := []struct {
		name       string
		cgroupFile CgroupFile
		expect     bool
	}{
		{
			name:       "alios_bvt_test",
			cgroupFile: CPUBVTWarpNs,
			expect:     true,
		},
		{
			name:       "not_alios_test",
			cgroupFile: CPUShares,
			expect:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.CreateCgroupFile("/", tt.cgroupFile)

			enable, _ := CPUIdentityEnabled()
			assert.Equal(t, tt.expect, enable)

		})
	}

}

func genCPUStatContent() string {
	return "nr_periods 18491717\n" +
		"nr_throttled 12\n" +
		"throttled_time 123\n"
}

func TestGetCPUStatRaw(t *testing.T) {
	helper := NewFileTestUtil(t)
	defer helper.Cleanup()
	testCPUDir := "cpu"
	filePath := GetCgroupFilePath(testCPUDir, CpuacctStat)

	goodContent := genCPUStatContent()
	helper.WriteCgroupFileContents(testCPUDir, CpuacctStat, goodContent)
	got, err := GetCPUStatRaw(filePath)
	assert.NoError(t, err)
	assert.Equal(t, int64(18491717), got.NrPeriod)
	assert.Equal(t, int64(12), got.NrThrottled)
	assert.Equal(t, int64(123), got.ThrottledNanoSeconds)

	badContent1 := "nr_periods a"
	helper.WriteCgroupFileContents(testCPUDir, CpuacctStat, badContent1)
	_, err1 := GetCPUStatRaw(filePath)
	assert.Error(t, err1)

	badContent2 := "nr_throttled a"
	helper.WriteCgroupFileContents(testCPUDir, CpuacctStat, badContent2)
	_, err2 := GetCPUStatRaw(filePath)
	assert.Error(t, err2)

	badContent3 := "throttled_time a"
	helper.WriteCgroupFileContents(testCPUDir, CpuacctStat, badContent3)
	_, err3 := GetCPUStatRaw(filePath)
	assert.Error(t, err3)

	badContent4 := "nr_periods 18491717\n" + "nr_throttled 12\n"
	helper.WriteCgroupFileContents(testCPUDir, CpuacctStat, badContent4)
	_, err4 := GetCPUStatRaw(filePath)
	assert.Error(t, err4)
}

func TestCalcCPUThrottledRatio(t *testing.T) {
	type args struct {
		curPoint *CPUStatRaw
		prePoint *CPUStatRaw
	}
	tests := []struct {
		name string
		args args
		want float64
	}{
		{
			name: "calculate-throttled-ratio",
			args: args{
				curPoint: &CPUStatRaw{
					NrPeriod:             200,
					NrThrottled:          40,
					ThrottledNanoSeconds: 40000,
				},
				prePoint: &CPUStatRaw{
					NrPeriod:             100,
					NrThrottled:          20,
					ThrottledNanoSeconds: 20000,
				},
			},
			want: 0.2,
		},
		{
			name: "calculate-throttled-ratio-zero-period",
			args: args{
				curPoint: &CPUStatRaw{
					NrPeriod:             100,
					NrThrottled:          20,
					ThrottledNanoSeconds: 20000,
				},
				prePoint: &CPUStatRaw{
					NrPeriod:             100,
					NrThrottled:          20,
					ThrottledNanoSeconds: 20000,
				},
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CalcCPUThrottledRatio(tt.args.curPoint, tt.args.prePoint); got != tt.want {
				t.Errorf("CalcCPUThrottledRatio() = %v, want %v", got, tt.want)
			}
		})
	}
}
