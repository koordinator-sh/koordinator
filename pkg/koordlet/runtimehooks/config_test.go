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

package runtimehooks

import (
	"flag"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_NewDefaultConfig(t *testing.T) {
	expectConfig := &Config{
		RuntimeHooksNetwork:             "unix",
		RuntimeHooksAddr:                "/host-var-run-koordlet/koordlet.sock",
		RuntimeHooksFailurePolicy:       "Ignore",
		RuntimeHooksPluginFailurePolicy: "Ignore",
		RuntimeHookConfigFilePath:       system.Conf.RuntimeHooksConfigDir,
		RuntimeHookHostEndpoint:         "/var/run/koordlet/koordlet.sock",
		RuntimeHookDisableStages:        []string{},
		RuntimeHooksNRI:                 true,
		RuntimeHooksNRIConnectTimeout:   6 * time.Second,
		RuntimeHooksNRIBackOffDuration:  1 * time.Second,
		RuntimeHooksNRIBackOffCap:       1<<62 - 1,
		RuntimeHooksNRIBackOffSteps:     math.MaxInt32,
		RuntimeHooksNRIBackOffFactor:    2,
		RuntimeHooksNRISocketPath:       "nri/nri.sock",
		RuntimeHooksNRIPluginName:       "koordlet_nri",
		RuntimeHooksNRIPluginIndex:      "00",
		RuntimeHookReconcileInterval:    10 * time.Second,
	}
	defaultConfig := NewDefaultConfig()
	assert.Equal(t, expectConfig, defaultConfig)
}

func Test_InitFlags(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.InitFlags(flag.CommandLine)
	flag.Parse()
}
