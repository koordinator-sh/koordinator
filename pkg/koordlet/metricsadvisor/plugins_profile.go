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
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/beresource"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/coldmemoryresource"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/hostapplication"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/nodeinfo"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/noderesource"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/nodestorageinfo"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/pagecache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/performance"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/podresource"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/podthrottled"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/collectors/sysresource"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/devices/gpu"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/devices/rdma"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
)

// NOTE: map variables in this file can be overwritten for extension

var (
	devicePlugins = map[string]framework.DeviceFactory{
		gpu.DeviceCollectorName:  gpu.New,
		rdma.DeviceCollectorName: rdma.New,
	}

	collectorPlugins = map[string]framework.CollectorFactory{
		noderesource.CollectorName:       noderesource.New,
		beresource.CollectorName:         beresource.New,
		nodeinfo.CollectorName:           nodeinfo.New,
		nodestorageinfo.CollectorName:    nodestorageinfo.New,
		podresource.CollectorName:        podresource.New,
		podthrottled.CollectorName:       podthrottled.New,
		performance.CollectorName:        performance.New,
		sysresource.CollectorName:        sysresource.New,
		coldmemoryresource.CollectorName: coldmemoryresource.New,
		pagecache.CollectorName:          pagecache.New,
		hostapplication.CollectorName:    hostapplication.New,
	}

	podFilters = map[string]framework.PodFilter{
		podresource.CollectorName:  framework.DefaultPodFilter,
		podthrottled.CollectorName: framework.DefaultPodFilter,
	}
)
