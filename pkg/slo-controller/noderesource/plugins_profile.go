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

package noderesource

import (
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/framework"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/batchresource"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/cpunormalization"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/gpudeviceresource"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/midresource"
	rdmadeviceresource "github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/rdmadevicereource"
	"github.com/koordinator-sh/koordinator/pkg/slo-controller/noderesource/plugins/resourceamplification"
)

// NOTE: functions in this file can be overwritten for extension

func init() {
	// set default plugins
	addPluginOption(&midresource.Plugin{}, true)
	addPluginOption(&batchresource.Plugin{}, true)
	addPluginOption(&cpunormalization.Plugin{}, true)
	addPluginOption(&resourceamplification.Plugin{}, true)
	addPluginOption(&gpudeviceresource.Plugin{}, true)
	addPluginOption(&rdmadeviceresource.Plugin{}, true)
}

func addPlugins(filter framework.FilterFn) {
	// NOTE: plugins run in order of the registration.
	framework.RegisterSetupExtender(filter, setupPlugins...)
	framework.RegisterNodePreUpdateExtender(filter, nodePreUpdatePlugins...)
	framework.RegisterNodePrepareExtender(filter, nodePreparePlugins...)
	framework.RegisterNodeStatusCheckExtender(filter, nodeStatusCheckPlugins...)
	framework.RegisterNodeMetaCheckExtender(filter, nodeMetaCheckPlugins...)
	framework.RegisterResourceCalculateExtender(filter, resourceCalculatePlugins...)
}

var (
	// SetupPlugins implement the setup for node resource plugin.
	setupPlugins = []framework.SetupPlugin{
		&cpunormalization.Plugin{},
		&resourceamplification.Plugin{},
		&batchresource.Plugin{},
		&gpudeviceresource.Plugin{},
		&rdmadeviceresource.Plugin{},
	}
	// NodePreUpdatePlugin implements node resource pre-updating.
	nodePreUpdatePlugins = []framework.NodePreUpdatePlugin{
		&batchresource.Plugin{},
	}
	// NodePreparePlugin implements node resource preparing for the calculated results.
	nodePreparePlugins = []framework.NodePreparePlugin{
		&cpunormalization.Plugin{}, // should be first
		&resourceamplification.Plugin{},
		&midresource.Plugin{},
		&batchresource.Plugin{},
		&gpudeviceresource.Plugin{},
		&rdmadeviceresource.Plugin{},
	}
	// NodeSyncPlugin implements the check of resource updating.
	nodeStatusCheckPlugins = []framework.NodeStatusCheckPlugin{
		&midresource.Plugin{},
		&batchresource.Plugin{},
		&gpudeviceresource.Plugin{},
		&rdmadeviceresource.Plugin{},
	}
	// nodeMetaCheckPlugins implements the check of node meta updating.
	nodeMetaCheckPlugins = []framework.NodeMetaCheckPlugin{
		&cpunormalization.Plugin{},
		&resourceamplification.Plugin{},
		&gpudeviceresource.Plugin{},
	}
	// ResourceCalculatePlugin implements resource counting and overcommitment algorithms.
	resourceCalculatePlugins = []framework.ResourceCalculatePlugin{
		&cpunormalization.Plugin{},
		&resourceamplification.Plugin{},
		&midresource.Plugin{},
		&batchresource.Plugin{},
		&gpudeviceresource.Plugin{},
		&rdmadeviceresource.Plugin{},
	}
)
