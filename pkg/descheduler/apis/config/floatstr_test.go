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

package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFloat64OrString_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantType  Type
		wantFloat float64
		wantStr   string
		wantErr   bool
	}{
		{
			name:      "float literal",
			input:     `3.14`,
			wantType:  Float,
			wantFloat: 3.14,
		},
		{
			name:     "quoted string",
			input:    `"10qps"`,
			wantType: String,
			wantStr:  "10qps",
		},
		{
			name:     "quoted float-as-string",
			input:    `"3.14"`,
			wantType: String,
			wantStr:  "3.14",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Float64OrString
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantType, got.Type)
			if tt.wantType == Float {
				assert.Equal(t, tt.wantFloat, got.FloatVal)
			} else {
				assert.Equal(t, tt.wantStr, got.StrVal)
			}
		})
	}
}

func TestFloat64OrString_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   Float64OrString
		want    string
		wantErr bool
	}{
		{
			name:  "float type",
			input: Float64OrString{Type: Float, FloatVal: 3.14},
			want:  `3.14`,
		},
		{
			name:  "string type",
			input: Float64OrString{Type: String, StrVal: "10qps"},
			want:  `"10qps"`,
		},
		{
			name:    "unknown type returns error",
			input:   Float64OrString{Type: Type(99)},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(&tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestFloat64OrString_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input Float64OrString
	}{
		{
			name:  "float round-trip",
			input: Float64OrString{Type: Float, FloatVal: 5.5},
		},
		{
			name:  "string round-trip",
			input: Float64OrString{Type: String, StrVal: "10qps"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(&tt.input)
			assert.NoError(t, err)
			var got Float64OrString
			assert.NoError(t, json.Unmarshal(data, &got))
			assert.Equal(t, tt.input, got)
		})
	}
}

func TestFloat64OrString_String(t *testing.T) {
	tests := []struct {
		name  string
		input *Float64OrString
		want  string
	}{
		{
			name:  "nil receiver",
			input: nil,
			want:  "<nil>",
		},
		{
			name:  "float type formatted to three decimal places",
			input: &Float64OrString{Type: Float, FloatVal: 3.14},
			want:  "3.140",
		},
		{
			name:  "string type passthrough",
			input: &Float64OrString{Type: String, StrVal: "10qps"},
			want:  "10qps",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.input.String())
		})
	}
}

func TestFloat64OrString_FloatValue(t *testing.T) {
	tests := []struct {
		name  string
		input Float64OrString
		want  float64
	}{
		{
			name:  "float type returns FloatVal directly",
			input: Float64OrString{Type: Float, FloatVal: 3.14},
			want:  3.14,
		},
		{
			name:  "string type with parseable value",
			input: Float64OrString{Type: String, StrVal: "3.14"},
			want:  3.14,
		},
		{
			name:  "string type with unparseable value returns zero",
			input: Float64OrString{Type: String, StrVal: "not-a-number"},
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.input.FloatValue())
		})
	}
}
