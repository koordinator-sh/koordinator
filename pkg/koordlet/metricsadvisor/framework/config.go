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
	"time"
)

const (
	CleanupInterval     = 600 * time.Second
	ContextExpiredRatio = 20
)

type Config struct {
	CollectResUsedInterval           time.Duration
	CollectSysMetricOutdatedInterval time.Duration
	CollectNodeCPUInfoInterval       time.Duration
	CollectNodeStorageInfoInterval   time.Duration
	CPICollectorInterval             time.Duration
	PSICollectorInterval             time.Duration
	CPICollectorTimeWindow           time.Duration
	ColdPageCollectorInterval        time.Duration
	ResctrlCollectorInterval         time.Duration
	EnablePageCacheCollector         bool
	EnableResctrlCollector           bool
}

func NewDefaultConfig() *Config {
	return &Config{
		CollectResUsedInterval:           1 * time.Second,
		CollectSysMetricOutdatedInterval: 10 * time.Second,
		CollectNodeCPUInfoInterval:       60 * time.Second,
		CollectNodeStorageInfoInterval:   1 * time.Second,
		CPICollectorInterval:             60 * time.Second,
		PSICollectorInterval:             10 * time.Second,
		CPICollectorTimeWindow:           10 * time.Second,
		ColdPageCollectorInterval:        5 * time.Second,
		ResctrlCollectorInterval:         1 * time.Second,
		EnablePageCacheCollector:         false,
		EnableResctrlCollector:           false,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.DurationVar(&c.CollectResUsedInterval, "collect-res-used-interval", c.CollectResUsedInterval, "Collect node/pod resource usage interval. Minimum interval is 1 second. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	fs.DurationVar(&c.CollectSysMetricOutdatedInterval, "collect-sys-metric-outdated-interval", c.CollectSysMetricOutdatedInterval, "Collecy system metrics outdated interval. Node or pods metrics whose timestamps are before the interval will be ignored.")
	fs.DurationVar(&c.CollectNodeCPUInfoInterval, "collect-node-cpu-info-interval", c.CollectNodeCPUInfoInterval, "Collect node cpu info interval. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	fs.DurationVar(&c.CollectNodeStorageInfoInterval, "collect-node-storage-info-interval", c.CollectNodeStorageInfoInterval, "Collect node storage info interval. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	fs.DurationVar(&c.CPICollectorInterval, "cpi-collector-interval", c.CPICollectorInterval, "Collect cpi interval. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	fs.DurationVar(&c.PSICollectorInterval, "psi-collector-interval", c.PSICollectorInterval, "Collect psi interval. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	fs.DurationVar(&c.CPICollectorTimeWindow, "collect-cpi-timewindow", c.CPICollectorTimeWindow, "Collect cpi time window. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	fs.DurationVar(&c.ColdPageCollectorInterval, "coldpage-collector-interval", c.ColdPageCollectorInterval, "Collect cold page interval. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
	fs.BoolVar(&c.EnablePageCacheCollector, "enable-pagecache-collector", c.EnablePageCacheCollector, "Enable cache collector of node, pods and containers")
	fs.BoolVar(&c.EnableResctrlCollector, "enable-resctrl-collector", c.EnableResctrlCollector, "Enable cache collector of node, pods and containers")
	fs.DurationVar(&c.ResctrlCollectorInterval, "resctrl-collector-interval", c.ResctrlCollectorInterval, "Collect cpi time window. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h).")
}
