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

func TestParseCPUCFSQuotaV2(t *testing.T) {
	tests := []struct {
		content string
		expect  int64
		isError bool
	}{
		// Test cases for valid input formats
		{"max 100000", -1, false},
		{"100000 100000", 100000, false},
		{"999999 100000", 999999, false},
		{"0 100000", 0, false},
		{"100000 not_a_number", 100000, false},

		// Test cases for invalid input formats
		{"100000", -1, true},              // Missing fields
		{"max", -1, true},                 // Missing quota value
		{"not_a_number 100000", -1, true}, // Invalid quota value
	}

	for _, test := range tests {
		result, err := ParseCPUCFSQuotaV2(test.content)

		if test.isError {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.content)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.content, err)
			}
			if result != test.expect {
				t.Errorf("For input: %s, expected: %d, got: %d", test.content, test.expect, result)
			}
		}
	}
}

func TestParseCPUAcctStatRawV2(t *testing.T) {
	tests := []struct {
		input   string
		want    *CPUStatV2Raw
		wantErr bool
	}{
		{
			input: "usage_usec 1000\nuser_usec 200\nsystem_usec 50\nnr_periods 10\nnr_throttled 2\nthrottled_usec 5",
			want: &CPUStatV2Raw{
				UsageUsec:     1000,
				UserUsec:      200,
				SystemUSec:    50,
				NrPeriods:     10,
				NrThrottled:   2,
				ThrottledUSec: 5,
			},
			wantErr: false,
		},
		{
			input:   "usage_usec 1000", // Missing fields
			want:    nil,
			wantErr: true,
		},
		{
			input:   "invalid_field 1000\nuser_usec 200\nsystem_usec 50\nnr_periods 10\nnr_throttled 2\nthrottled_usec 5", // Invalid field
			want:    nil,
			wantErr: true,
		},
		{
			input:   "usage_usec not_a_number\nuser_usec 200\nsystem_usec 50\nnr_periods 10\nnr_throttled 2\nthrottled_usec 5", // Invalid value
			want:    nil,
			wantErr: true,
		},
	}

	for _, test := range tests {
		got, err := ParseCPUAcctStatRawV2(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.input, err)
			}
			if !compareCPUStatV2Raw(got, test.want) {
				t.Errorf("For input: %s, got: %v, want: %v", test.input, got, test.want)
			}
		}
	}
}

func compareCPUStatV2Raw(a, b *CPUStatV2Raw) bool {
	return a.UsageUsec == b.UsageUsec &&
		a.UserUsec == b.UserUsec &&
		a.SystemUSec == b.SystemUSec &&
		a.NrPeriods == b.NrPeriods &&
		a.NrThrottled == b.NrThrottled &&
		a.ThrottledUSec == b.ThrottledUSec
}

func TestParseCPUAcctUsageV2(t *testing.T) {
	tests := []struct {
		input   string
		want    uint64
		wantErr bool
	}{
		{
			input:   "usage_usec 1000000",
			want:    1000000000,
			wantErr: false,
		},
		{
			input:   "usage_usec not_a_number",
			want:    0,
			wantErr: true,
		},
		{
			input:   "invalid_field 1000000",
			want:    0,
			wantErr: true,
		},
		// Add more test cases here to cover edge cases and error scenarios
	}

	for _, test := range tests {
		got, err := ParseCPUAcctUsageV2(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.input, err)
			}
			if got != test.want {
				t.Errorf("For input: %s, got: %d, want: %d", test.input, got, test.want)
			}
		}
	}
}

func TestParseCPUStatRawV2(t *testing.T) {
	tests := []struct {
		input   string
		want    *CPUStatRaw
		wantErr bool
	}{
		{
			input: "nr_periods 10\nnr_throttled 2\nthrottled_usec 5",
			want: &CPUStatRaw{
				NrPeriods:            10,
				NrThrottled:          2,
				ThrottledNanoSeconds: 5000,
			},
			wantErr: false,
		},
		{
			input:   "nr_periods not_a_number\nnr_throttled 2\nthrottled_usec 5",
			want:    nil,
			wantErr: true,
		},
		{
			input:   "invalid_field 10\nnr_throttled 2\nthrottled_usec 5",
			want:    nil,
			wantErr: true,
		},
	}

	for _, test := range tests {
		got, err := ParseCPUStatRawV2(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.input, err)
			}
			if !compareCPUStatRaw(got, test.want) {
				t.Errorf("For input: %s, got: %v, want: %v", test.input, got, test.want)
			}
		}
	}
}

func compareCPUStatRaw(a, b *CPUStatRaw) bool {
	return a.NrPeriods == b.NrPeriods &&
		a.NrThrottled == b.NrThrottled &&
		a.ThrottledNanoSeconds == b.ThrottledNanoSeconds
}

func TestParseMemoryStatRawV2(t *testing.T) {
	tests := []struct {
		input   string
		want    *MemoryStatRaw
		wantErr bool
	}{
		{
			input: "file 100\nanon 200\ninactive_file 50\nactive_file 60\ninactive_anon 70\nactive_anon 80\nunevictable 10",
			want: &MemoryStatRaw{
				Cache:        100,
				RSS:          200,
				InactiveFile: 50,
				ActiveFile:   60,
				InactiveAnon: 70,
				ActiveAnon:   80,
				Unevictable:  10,
			},
			wantErr: false,
		},
		{
			input:   "file not_a_number\nanon 200\ninactive_file 50\nactive_file 60\ninactive_anon 70\nactive_anon 80\nunevictable 10",
			want:    nil,
			wantErr: true,
		},
		{
			input:   "invalid_field 100\nanon 200\ninactive_file 50\nactive_file 60\ninactive_anon 70\nactive_anon 80\nunevictable 10",
			want:    nil,
			wantErr: true,
		},
	}

	for _, test := range tests {
		got, err := ParseMemoryStatRawV2(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.input, err)
			}
			if !compareMemoryStatRaw(got, test.want) {
				t.Errorf("For input: %s, got: %v, want: %v", test.input, got, test.want)
			}
		}
	}
}

func compareMemoryStatRaw(a, b *MemoryStatRaw) bool {
	return a.Cache == b.Cache &&
		a.RSS == b.RSS &&
		a.InactiveFile == b.InactiveFile &&
		a.ActiveFile == b.ActiveFile &&
		a.InactiveAnon == b.InactiveAnon &&
		a.ActiveAnon == b.ActiveAnon &&
		a.Unevictable == b.Unevictable
}

func TestParseMemoryNumaStatV2(t *testing.T) {
	tests := []struct {
		input   string
		want    []NumaMemoryPages
		wantErr bool
	}{
		{
			input: "anon N0=12345 N1=67890\nfile N0=10000 N1=20000",
			want: []NumaMemoryPages{
				{NumaId: 0, PagesNum: 5},
				{NumaId: 1, PagesNum: 20},
			},
			wantErr: false,
		},
		{
			input:   "invalid_line_format",
			want:    nil,
			wantErr: true,
		},
		{
			input:   "anon N0=not_a_number",
			want:    nil,
			wantErr: true,
		},
		{
			input:   "anon N0=12345 N2=67890",
			want:    nil,
			wantErr: true,
		},
		// Add more test cases here to cover edge cases and error scenarios
	}

	for _, test := range tests {
		got, err := ParseMemoryNumaStatV2(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.input, err)
			}
			if !compareNumaMemoryPages(got, test.want) {
				t.Errorf("For input: %s, got: %v, want: %v", test.input, got, test.want)
			}
		}
	}
}

func compareNumaMemoryPages(a, b []NumaMemoryPages) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if a[i].NumaId != b[i].NumaId || a[i].PagesNum != b[i].PagesNum {
			return false
		}
	}
	return true
}

func TestConvertCPUWeightToShares(t *testing.T) {
	tests := []struct {
		input    int64
		expected int64
		wantErr  bool
	}{
		{
			input:    50,
			expected: 512,
			wantErr:  false,
		},
		{
			input:    10000,
			expected: 102400,
			wantErr:  false,
		},
		{
			input:   0,
			wantErr: true,
		},
		{
			input:   10001,
			wantErr: true,
		},
		{
			input:    -1,
			expected: 2,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		got, err := ConvertCPUWeightToShares(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %d", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %d, err: %v", test.input, err)
			}
			if got != test.expected {
				t.Errorf("For input: %d, got: %d, want: %d", test.input, got, test.expected)
			}
		}
	}
}

func TestConvertCPUSharesToWeight(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{
			input:    "1000",
			expected: 97, // 1000 * 100 / 1024 = 97
			wantErr:  false,
		},
		{
			input:    "1024",
			expected: 100, // 1024 * 100 / 1024 = 100
			wantErr:  false,
		},
		{
			input:    "512",
			expected: 50, // 512 * 100 / 1024 = 50
			wantErr:  false,
		},
		{
			input:   "0",
			wantErr: true,
		},
		{
			input:    "2000",
			expected: 195, // 2000 * 100 / 1024 = 195
			wantErr:  false,
		},
		{
			input:    "50000",
			expected: 4882, // 50000 * 100 / 1024 = 4882
			wantErr:  false,
		},
		{
			input:    "not_a_number", // Invalid input, not a valid integer
			expected: 0,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		got, err := ConvertCPUSharesToWeight(test.input)

		if test.wantErr {
			if err == nil {
				t.Errorf("Expected an error for input: %s", test.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input: %s, err: %v", test.input, err)
			}
			if got != test.expected {
				t.Errorf("For input: %s, got: %d, want: %d", test.input, got, test.expected)
			}
		}
	}
}
