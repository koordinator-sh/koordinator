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

package statesinformer

import (
	"flag"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestNewDefaultConfig(t *testing.T) {
	tests := []struct {
		name string
		want *Config
	}{
		{
			name: "config",
			want: &Config{
				KubeletPreferredAddressType: string(corev1.NodeInternalIP),
				KubeletSyncIntervalSeconds:  30,
				KubeletSyncTimeoutSeconds:   3,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewDefaultConfig(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewDefaultConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_InitFlags(t *testing.T) {
	cmdArgs := []string{
		"",
		"--KubeletPreferredAddressType=Hostname",
		"--KubeletSyncIntervalSeconds=10",
		"--KubeletSyncTimeoutSeconds=30",
	}
	fs := flag.NewFlagSet(cmdArgs[0], flag.ExitOnError)

	type fields struct {
		KubeletPreferredAddressType string
		KubeletSyncIntervalSeconds  int
		KubeletSyncTimeoutSeconds   int
		ApiServerSyncTimeoutSeconds int
	}
	type args struct {
		fs *flag.FlagSet
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "not default",
			fields: fields{
				KubeletPreferredAddressType: "Hostname",
				KubeletSyncIntervalSeconds:  10,
				KubeletSyncTimeoutSeconds:   30,
				ApiServerSyncTimeoutSeconds: 20,
			},
			args: args{fs: fs},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			raw := &Config{
				KubeletPreferredAddressType: tt.fields.KubeletPreferredAddressType,
				KubeletSyncIntervalSeconds:  tt.fields.KubeletSyncIntervalSeconds,
				KubeletSyncTimeoutSeconds:   tt.fields.KubeletSyncTimeoutSeconds,
			}
			c := NewDefaultConfig()
			c.InitFlags(tt.args.fs)
			tt.args.fs.Parse(cmdArgs[1:])
			if !reflect.DeepEqual(raw, c) {
				t.Fatalf("InitFlags got: %+v, want: %+v", c, raw)
			}
		})
	}
}
