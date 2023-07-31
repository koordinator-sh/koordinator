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
)

func TestParseCPUStatRaw(t *testing.T) {
	tests := []struct {
		input    string
		expected *CPUStatRaw
		wantErr  bool
	}{
		{
			input: "nr_periods 100\nnr_throttled 20\nthrottled_time 5000",
			expected: &CPUStatRaw{
				NrPeriods:            100,
				NrThrottled:          20,
				ThrottledNanoSeconds: 5000,
			},
			wantErr: false,
		},
		{
			input:    "nr_periods 200\nnr_throttled abc", // Invalid value for nr_throttled
			expected: nil,
			wantErr:  true,
		},
		{
			input:    "nr_periods 100", // Missing nr_throttled and throttled_time
			expected: nil,
			wantErr:  true,
		},
		{
			input:    "", // Empty input
			expected: nil,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		cpuStatRaw, err := ParseCPUStatRaw(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.input, err)
			}
			if cpuStatRaw == nil {
				t.Errorf("Expected non-nil result for input: %s", test.input)
			} else {
				if cpuStatRaw.NrPeriods != test.expected.NrPeriods {
					t.Errorf("For input: %s, got NrPeriods: %d, want: %d", test.input, cpuStatRaw.NrPeriods, test.expected.NrPeriods)
				}
				if cpuStatRaw.NrThrottled != test.expected.NrThrottled {
					t.Errorf("For input: %s, got NrThrottled: %d, want: %d", test.input, cpuStatRaw.NrThrottled, test.expected.NrThrottled)
				}
				if cpuStatRaw.ThrottledNanoSeconds != test.expected.ThrottledNanoSeconds {
					t.Errorf("For input: %s, got ThrottledNanoSeconds: %d, want: %d", test.input, cpuStatRaw.ThrottledNanoSeconds, test.expected.ThrottledNanoSeconds)
				}
			}
		}
	}
}

func TestParseMemoryStatRaw(t *testing.T) {
	tests := []struct {
		input    string
		expected *MemoryStatRaw
		wantErr  bool
	}{
		{
			input: "total_cache 100\ntotal_rss 200\ntotal_inactive_file 50\ntotal_active_file 100\ntotal_inactive_anon 60\ntotal_active_anon 40\ntotal_unevictable 20",
			expected: &MemoryStatRaw{
				Cache:        100,
				RSS:          200,
				InactiveFile: 50,
				ActiveFile:   100,
				InactiveAnon: 60,
				ActiveAnon:   40,
				Unevictable:  20,
			},
			wantErr: false,
		},
		{
			input:    "total_cache 100\ninvalid_field abc", // Invalid field name
			expected: nil,
			wantErr:  true,
		},
		{
			input:    "total_cache 100", // Missing fields
			expected: nil,
			wantErr:  true,
		},
		{
			input:    "", // Empty input
			expected: nil,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		memoryStatRaw, err := ParseMemoryStatRaw(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.input, err)
			}
			if memoryStatRaw == nil {
				t.Errorf("Expected non-nil result for input: %s", test.input)
			} else {
				if memoryStatRaw.Cache != test.expected.Cache {
					t.Errorf("For input: %s, got Cache: %d, want: %d", test.input, memoryStatRaw.Cache, test.expected.Cache)
				}
				if memoryStatRaw.RSS != test.expected.RSS {
					t.Errorf("For input: %s, got RSS: %d, want: %d", test.input, memoryStatRaw.RSS, test.expected.RSS)
				}
				if memoryStatRaw.InactiveFile != test.expected.InactiveFile {
					t.Errorf("For input: %s, got InactiveFile: %d, want: %d", test.input, memoryStatRaw.InactiveFile, test.expected.InactiveFile)
				}
			}
		}
	}
}

func TestParseMemoryNumaStat(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []NumaMemoryPages
		wantErr bool
	}{
		// Test case where the input content is valid and contains NumaMemoryPages data.
		{
			name:    "Valid Input",
			content: `total N0=1234 N1=5678 N2=91011`,
			want: []NumaMemoryPages{
				{NumaId: 0, PagesNum: 1234},
				{NumaId: 1, PagesNum: 5678},
				{NumaId: 2, PagesNum: 91011},
			},
			wantErr: false,
		},
		// Test case where the input content is empty, should return an error.
		{
			name:    "Empty Input",
			content: "",
			want:    nil,
			wantErr: true,
		},
		// Test case where the input content does not start with "total", should return an error.
		{
			name:    "Invalid Input (No 'total' Prefix)",
			content: `N0=1234 N1=5678 N2=91011`,
			want:    nil,
			wantErr: true,
		},
		// Test case where the input content has an invalid format, missing "=" sign, should return an error.
		{
			name:    "Invalid Input (Missing '=' Sign)",
			content: `total N0=1234 N1=5678 N2=91011 N3-123`,
			want:    nil,
			wantErr: true,
		},
		// Test case where the input content has an invalid format, invalid NumaId, should return an error.
		{
			name:    "Invalid Input (Invalid NumaId)",
			content: `total N0=12a4 N1=5678 N2=91011 N3=9876`,
			want:    nil,
			wantErr: true,
		},
		// Test case where the input content has an invalid format, invalid PagesNum, should return an error.
		{
			name:    "Invalid Input (Invalid PagesNum)",
			content: `total N0=1234 N1=5678 N2=invalid`,
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMemoryNumaStat(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMemoryNumaStat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !compareNumaMemoryPages(got, tt.want) {
				t.Errorf("ParseMemoryNumaStat() = %v, want %v", got, tt.want)
			}
		})
	}
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
					NrPeriods:            200,
					NrThrottled:          40,
					ThrottledNanoSeconds: 40000,
				},
				prePoint: &CPUStatRaw{
					NrPeriods:            100,
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
					NrPeriods:            100,
					NrThrottled:          20,
					ThrottledNanoSeconds: 20000,
				},
				prePoint: &CPUStatRaw{
					NrPeriods:            100,
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
