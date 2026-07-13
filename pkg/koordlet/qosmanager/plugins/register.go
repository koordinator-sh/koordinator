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

package plugins

import (
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/blkio"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/cgreconcile"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/cpuburst"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/cpuevict"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/cpusuppress"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/memoryevict"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/resctrl"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/sysreconcile"
)

var (
	StrategyPlugins = map[string]framework.QOSStrategyFactory{
		blkio.BlkIOReconcileName:               blkio.New,
		cgreconcile.CgroupReconcileName:        cgreconcile.New,
		cpuburst.CPUBurstName:                  cpuburst.New,
		cpuevict.CPUEvictName:                  cpuevict.New,
		cpusuppress.CPUSuppressName:            cpusuppress.New,
		memoryevict.MemoryEvictName:            memoryevict.New,
		resctrl.ResctrlReconcileName:           resctrl.New,
		sysreconcile.SystemConfigReconcileName: sysreconcile.New,
		psi.PSIReconcileName:                   psi.New,
	}
)
