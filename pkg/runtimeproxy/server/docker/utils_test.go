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

package docker

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_calculateContentLength(t *testing.T) {
	type testCase struct {
		data      io.Reader
		expectLen int64
		expectErr bool
	}
	tests := []testCase{
		{
			bytes.NewBuffer([]byte("")),
			0,
			false,
		},
		{
			bytes.NewBuffer([]byte("1234567")),
			7,
			false,
		},
		{
			nil,
			-1,
			true,
		},
	}
	for _, tt := range tests {
		l, err := calculateContentLength(tt.data)
		assert.Equal(t, tt.expectErr, err != nil, err)
		assert.Equal(t, tt.expectLen, l)
	}
}

func Test_getContainerID(t *testing.T) {
	type testCase struct {
		url               string
		expectErr         bool
		expectContainerID string
	}
	tests := []testCase{
		{
			"/1.3/containers/5345hjkhjkf/start",
			false,
			"5345hjkhjkf",
		},
		{
			"1.3",
			true,
			"",
		},
	}
	for _, tt := range tests {
		cid, err := getContainerID(tt.url)
		assert.Equal(t, tt.expectErr, err != nil, err)
		assert.Equal(t, tt.expectContainerID, cid)
	}
}
