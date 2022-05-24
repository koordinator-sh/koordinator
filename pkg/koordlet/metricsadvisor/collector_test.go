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
	"testing"
	"time"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

func TestNewCollector(t *testing.T) {
	type args struct {
		cfg         *Config
		metaService statesinformer.StatesInformer
		metricCache metriccache.MetricCache
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "new-collector",
			args: args{
				cfg:         &Config{},
				metaService: nil,
				metricCache: nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewCollector(tt.args.cfg, tt.args.metaService, tt.args.metricCache); got == nil {
				t.Errorf("NewCollector() = %v", got)
			}
		})
	}
}

func Test_cleanupContext(t *testing.T) {
	c := collector{config: &Config{CollectResUsedIntervalSeconds: 1}, context: newCollectContext(), state: newCollectState()}
	for k, v := range map[string]contextRecord{
		"expired": {cpuTick: 100, ts: time.Now().Add(0 - 2*time.Duration(contextExpiredRatio)*time.Second)},
		"valid":   {cpuTick: 10, ts: time.Now()},
	} {
		c.context.lastPodCPUStat.Store(k, v)
	}
	c.cleanupContext()
	if _, ok := c.context.lastPodCPUStat.Load("expired"); ok {
		t.Errorf("expects removing the expired pod record after cleanupContext() but actually not")
	}
}
