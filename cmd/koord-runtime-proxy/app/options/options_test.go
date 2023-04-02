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

package options

import "testing"

func TestValidate(t *testing.T) {
	type args struct {
		runtimeProxyEndpoint         string
		runtimeMode                  string
		remoteRuntimeServiceEndpoint string
		remoyeRuntimeImageEndpoint   string
	}

	tests := []struct {
		name     string
		args     args
		expected bool
	}{
		{
			name: "runtime proxy options is valid.",
			args: args{
				runtimeProxyEndpoint:         DefaultRuntimeProxyEndpoint,
				runtimeMode:                  BackendRuntimeModeContainerd,
				remoteRuntimeServiceEndpoint: DefaultContainerdRuntimeServiceEndpoint,
				remoyeRuntimeImageEndpoint:   DefaultContainerdImageServiceEndpoint,
			},
			expected: true,
		},
		{
			name: "runtime proxy endpoint does not set.",
			args: args{
				runtimeMode:                  BackendRuntimeModeContainerd,
				remoteRuntimeServiceEndpoint: DefaultContainerdRuntimeServiceEndpoint,
				remoyeRuntimeImageEndpoint:   DefaultContainerdImageServiceEndpoint,
			},
			expected: false,
		},
		{
			name: "runtime mode does not set.",
			args: args{
				runtimeProxyEndpoint:         DefaultRuntimeProxyEndpoint,
				remoteRuntimeServiceEndpoint: DefaultContainerdRuntimeServiceEndpoint,
				remoyeRuntimeImageEndpoint:   DefaultContainerdImageServiceEndpoint,
			},
			expected: false,
		},
		{
			name: "runtime proxy remote runtime service endpoint does not set.",
			args: args{
				runtimeProxyEndpoint:       DefaultRuntimeProxyEndpoint,
				runtimeMode:                BackendRuntimeModeContainerd,
				remoyeRuntimeImageEndpoint: DefaultContainerdImageServiceEndpoint,
			},
			expected: false,
		},
		{
			name: "runtime proxy remote image service endpoint does not set.",
			args: args{
				runtimeProxyEndpoint:         DefaultRuntimeProxyEndpoint,
				runtimeMode:                  BackendRuntimeModeContainerd,
				remoteRuntimeServiceEndpoint: DefaultContainerdRuntimeServiceEndpoint,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{
				RuntimeProxyEndpoint:         tt.args.runtimeProxyEndpoint,
				RuntimeMode:                  tt.args.runtimeMode,
				RemoteRuntimeServiceEndpoint: tt.args.remoteRuntimeServiceEndpoint,
				RemoyeRuntimeImageEndpoint:   tt.args.remoyeRuntimeImageEndpoint,
			}
			err := opts.Validate()
			if (err != nil) == tt.expected {
				t.Errorf("got err: %v, want: %v", err, tt.expected)
			}
		})
	}
}
