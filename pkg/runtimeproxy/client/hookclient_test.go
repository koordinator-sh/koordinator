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

package client

import "testing"

func TestNewRuntimeHookClient(t *testing.T) {
	tests := []struct {
		name     string
		sockPath string
		expected bool
	}{
		{
			name:     "set runtime hook server sock",
			sockPath: "/var/run/koordlet/koordlet.sock",
			expected: true,
		},
		{
			name:     "runtime hook server does not set sock",
			sockPath: "",
			expected: false,
		},
	}

	for _, tt := range tests {
		_, err := newRuntimeHookClient(tt.sockPath)
		if (err != nil) == tt.expected {
			t.Errorf("got error: %v, want: %v", err, tt.expected)
		}
	}
}

func TestRuntimeHookServerClient(t *testing.T) {
	type args struct {
		hookServerPath HookServerPath
	}

	tests := []struct {
		name     string
		args     args
		expected bool
	}{
		{
			name: "set runtime hook server sock path",
			args: args{
				hookServerPath: HookServerPath{
					Path: "/var/run/koordlet/koordlet.sock",
				},
			},
			expected: true,
		},
		{
			name: "runtime hook server does not sock path",
			args: args{
				hookServerPath: HookServerPath{
					Path: "",
				},
			},
			expected: false,
		},
	}

	manger := NewClientManager()
	for _, tt := range tests {
		_, err := manger.RuntimeHookServerClient(tt.args.hookServerPath)
		if (err != nil) == tt.expected {
			t.Errorf("got err: %v, want: %v", err, tt.expected)
		}
	}
}
