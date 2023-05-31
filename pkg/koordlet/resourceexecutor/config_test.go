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
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_NewDefaultConfig(t *testing.T) {
	expectConfig := &Config{
		ResourceForceUpdateSeconds: 60,
	}
	defaultConfig := NewDefaultConfig()
	assert.Equal(t, expectConfig, defaultConfig)
}

func Test_InitFlags(t *testing.T) {
	type fields struct {
		ResourceForceUpdateSeconds int
	}
	type args struct {
		fs      *flag.FlagSet
		cmdArgs []string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "not default",
			fields: fields{
				ResourceForceUpdateSeconds: 120,
			},
			args: args{
				fs: flag.NewFlagSet("", flag.ExitOnError),
				cmdArgs: []string{
					"",
					"--resource-force-update-seconds=120",
				},
			},
		},
		{
			name: "not default 1",
			fields: fields{
				ResourceForceUpdateSeconds: 90,
			},
			args: args{
				fs: flag.NewFlagSet("", flag.ExitOnError),
				cmdArgs: []string{
					"",
					"--resource-force-update-seconds=90",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := &Config{
				ResourceForceUpdateSeconds: tt.fields.ResourceForceUpdateSeconds,
			}
			c := NewDefaultConfig()
			c.InitFlags(tt.args.fs)
			err := tt.args.fs.Parse(tt.args.cmdArgs[1:])
			assert.NoError(t, err, err)
			assert.Equal(t, want, c)
		})
	}
}
