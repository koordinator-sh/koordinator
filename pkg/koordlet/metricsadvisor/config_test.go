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

package metricsadvisor

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_NewDefaultConfig(t *testing.T) {
	expectConfig := &Config{
		CollectResUsedIntervalSeconds:         1,
		CollectNodeCPUInfoIntervalSeconds:     60,
		PerformanceCollectorIntervalSeconds:   60,
		PerformanceCollectorTimeWindowSeconds: 10,
	}
	defaultConfig := NewDefaultConfig()
	assert.Equal(t, expectConfig, defaultConfig)
}

func Test_InitFlags(t *testing.T) {
	cmdArgs := []string{
		"",
		"--collect-res-used-interval-seconds=3",
		"--collect-node-cpu-info-interval-seconds=90",
		"--collect-interference-interval-seconds=90",
		"--collect-interference-timewindow-seconds=15",
	}
	fs := flag.NewFlagSet(cmdArgs[0], flag.ExitOnError)

	type fields struct {
		CollectResUsedIntervalSeconds        int
		CollectNodeCPUInfoIntervalSeconds    int
		CollectInterferenceIntervalSeconds   int
		CollectInterferenceTimeWindowSeconds int
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
				CollectResUsedIntervalSeconds:        3,
				CollectNodeCPUInfoIntervalSeconds:    90,
				CollectInterferenceIntervalSeconds:   90,
				CollectInterferenceTimeWindowSeconds: 15,
			},
			args: args{fs: fs},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := &Config{
				CollectResUsedIntervalSeconds:         tt.fields.CollectResUsedIntervalSeconds,
				CollectNodeCPUInfoIntervalSeconds:     tt.fields.CollectNodeCPUInfoIntervalSeconds,
				PerformanceCollectorIntervalSeconds:   tt.fields.CollectInterferenceIntervalSeconds,
				PerformanceCollectorTimeWindowSeconds: tt.fields.CollectInterferenceTimeWindowSeconds,
			}
			c := NewDefaultConfig()
			c.InitFlags(tt.args.fs)
			tt.args.fs.Parse(cmdArgs[1:])
			assert.Equal(t, raw, c)
		})
	}
}
