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
	CollectorName = "ColdPageCollector"
)

type nonColdPageCollector struct {
}

func New(opt *framework.Options) framework.Collector {
	// check whether support kidled cold page info collector
	if system.IsKidledSupport() {
		kidledConfig := system.NewDefaultKidledConfig()
		return &kidledcoldPageCollector{
			collectInterval: opt.Config.ColdPageCollectorInterval,
			cgroupReader:    opt.CgroupReader,
			statesInformer:  opt.StatesInformer,
			// TODO(BUPT-wxq): implement podFilter for the VM-based pods and containers
			podFilter:    framework.DefaultPodFilter,
			appendableDB: opt.MetricCache,
			metricDB:     opt.MetricCache,
			started:      atomic.NewBool(false),
			coldBoundary: kidledConfig.KidledColdBoundary,
		}
	}
	// TODO(BUPT-wxq): check kstaled cold page collector
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
