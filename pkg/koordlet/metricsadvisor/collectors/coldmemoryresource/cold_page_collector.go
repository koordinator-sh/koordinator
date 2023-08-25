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

package coldmemoryresource

import (
	"go.uber.org/atomic"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	CollectorName = "coldPageCollector"
)

type nonColdPageCollector struct {
}

func New(opt *framework.Options) framework.Collector {
	// check whether support kidled cold page info collector
	if system.IsKidledSupported() {
		system.SetIsSupportColdMemory(true)
		return &kidledcoldPageCollector{
			collectInterval: opt.Config.CollectResUsedInterval,
			cgroupReader:    opt.CgroupReader,
			statesInformer:  opt.StatesInformer,
			podFilter:       framework.DefaultPodFilter,
			appendableDB:    opt.MetricCache,
			metricDB:        opt.MetricCache,
			started:         atomic.NewBool(false),
		}
	}
	// TODO(BUPT-wxq): check kstaled cold page collector
	// TODO(BUPT-wxq): check DAMON cold page collector
	// nonCollector does nothing
	return &nonColdPageCollector{}
}

func (n *nonColdPageCollector) Run(stopCh <-chan struct{}) {}

func (n *nonColdPageCollector) Started() bool {
	return false
}

func (n *nonColdPageCollector) Enabled() bool {
	return false
}

func (n *nonColdPageCollector) Setup(c1 *framework.Context) {}
