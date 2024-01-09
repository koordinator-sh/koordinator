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

package framework

import (
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_NewDefaultConfig(t *testing.T) {
	expectConfig := &Config{
		CollectResUsedInterval:           1 * time.Second,
		CollectSysMetricOutdatedInterval: 10 * time.Second,
		CollectNodeCPUInfoInterval:       60 * time.Second,
		CollectNodeStorageInfoInterval:   1 * time.Second,
		CPICollectorInterval:             60 * time.Second,
		PSICollectorInterval:             10 * time.Second,
		CPICollectorTimeWindow:           10 * time.Second,
		ColdPageCollectorInterval:        5 * time.Second,
		EnablePageCacheCollector:         false,
		CollectPromMetricRulePath:        "",
	}
	defaultConfig := NewDefaultConfig()
	assert.Equal(t, expectConfig, defaultConfig)
}

func Test_InitFlags(t *testing.T) {
	cmdArgs := []string{
		"",
		"--collect-res-used-interval=3s",
		"--collect-sys-metric-outdated-interval=9s",
		"--collect-node-cpu-info-interval=90s",
		"--collect-node-storage-info-interval=4s",
		"--cpi-collector-interval=90s",
		"--psi-collector-interval=5s",
		"--collect-cpi-timewindow=15s",
		"--coldpage-collector-interval=15s",
		"--collect-prom-metric-rule-path=/prom_rules.yaml",
	}
	fs := flag.NewFlagSet(cmdArgs[0], flag.ExitOnError)

	type fields struct {
		CollectResUsedInterval           time.Duration
		CollectSysMetricOutdatedInterval time.Duration
		CollectNodeCPUInfoInterval       time.Duration
		CollectNodeStorageInfoInterval   time.Duration
		CPICollectorInterval             time.Duration
		PSICollectorInterval             time.Duration
		CPICollectorTimeWindow           time.Duration
		ColdPageCollectorInterval        time.Duration
		CollectPromMetricInterval        time.Duration
		CollectPromMetricRulePath        string
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
				CollectResUsedInterval:           3 * time.Second,
				CollectSysMetricOutdatedInterval: 9 * time.Second,
				CollectNodeCPUInfoInterval:       90 * time.Second,
				CollectNodeStorageInfoInterval:   4 * time.Second,
				CPICollectorInterval:             90 * time.Second,
				PSICollectorInterval:             5 * time.Second,
				CPICollectorTimeWindow:           15 * time.Second,
				ColdPageCollectorInterval:        15 * time.Second,
				CollectPromMetricInterval:        30 * time.Second,
				CollectPromMetricRulePath:        "/prom_rules.yaml",
			},
			args: args{fs: fs},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := &Config{
				CollectResUsedInterval:           tt.fields.CollectResUsedInterval,
				CollectSysMetricOutdatedInterval: tt.fields.CollectSysMetricOutdatedInterval,
				CollectNodeCPUInfoInterval:       tt.fields.CollectNodeCPUInfoInterval,
				CollectNodeStorageInfoInterval:   tt.fields.CollectNodeStorageInfoInterval,
				CPICollectorInterval:             tt.fields.CPICollectorInterval,
				PSICollectorInterval:             tt.fields.PSICollectorInterval,
				CPICollectorTimeWindow:           tt.fields.CPICollectorTimeWindow,
				ColdPageCollectorInterval:        tt.fields.ColdPageCollectorInterval,
				CollectPromMetricRulePath:        tt.fields.CollectPromMetricRulePath,
			}
			c := NewDefaultConfig()
			c.InitFlags(tt.args.fs)
			err := tt.args.fs.Parse(cmdArgs[1:])
			assert.NoError(t, err)
			assert.Equal(t, raw, c)
		})
	}
}

func TestRegisterExtendedInitFlags(t *testing.T) {
	testFlagBool := false
	testFlagInt := 0
	testFlagString := ""
	type fields struct {
		cmdArgs []string
	}
	tests := []struct {
		name      string
		fields    fields
		arg       func(fs *flag.FlagSet)
		wantField func(*testing.T)
	}{
		{
			name: "nop",
			fields: fields{
				cmdArgs: []string{
					"",
					"--collect-res-used-interval=3s",
					"--collect-sys-metric-outdated-interval=9s",
					"--collect-node-cpu-info-interval=90s",
				},
			},
			arg:       func(fs *flag.FlagSet) {},
			wantField: func(t *testing.T) {},
		},
		{
			name: "add some flags",
			fields: fields{
				cmdArgs: []string{
					"",
					"--collect-res-used-interval=3s",
					"--collect-sys-metric-outdated-interval=9s",
					"--collect-node-cpu-info-interval=90s",
					"--test-flag-bool=true",
					"--test-flag-int=10",
					"--test-flag-string=xxx",
				},
			},
			arg: func(fs *flag.FlagSet) {
				fs.BoolVar(&testFlagBool, "test-flag-bool", testFlagBool, "test-flag-bool")
				fs.IntVar(&testFlagInt, "test-flag-int", testFlagInt, "test-flag-int")
				fs.StringVar(&testFlagString, "test-flag-string", testFlagString, "test-flag-string")
			},
			wantField: func(t *testing.T) {
				assert.Equal(t, true, testFlagBool)
				assert.Equal(t, 10, testFlagInt)
				assert.Equal(t, "xxx", testFlagString)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet(tt.fields.cmdArgs[0], flag.ExitOnError)

			RegisterExtendedInitFlags(tt.arg)
			c := NewDefaultConfig()
			c.InitFlags(fs)
			err := fs.Parse(tt.fields.cmdArgs[1:])
			assert.NoError(t, err)
			tt.wantField(t)
		})
	}
}
