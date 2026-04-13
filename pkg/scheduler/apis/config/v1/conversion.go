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

package v1

import (
	config "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	conversion "k8s.io/apimachinery/pkg/conversion"
)

// Convert_v1_NodeNUMAResourceArgs_To_config_NodeNUMAResourceArgs manually converts
// NodeNUMAResourceArgs from v1 to internal config type.
// DefaultCPUBindPolicy is *CPUBindPolicy in v1 but CPUBindPolicy (value) in config.
func Convert_v1_NodeNUMAResourceArgs_To_config_NodeNUMAResourceArgs(in *NodeNUMAResourceArgs, out *config.NodeNUMAResourceArgs, s conversion.Scope) error {
	if err := autoConvert_v1_NodeNUMAResourceArgs_To_config_NodeNUMAResourceArgs(in, out, s); err != nil {
		return err
	}
	if in.DefaultCPUBindPolicy != nil {
		out.DefaultCPUBindPolicy = config.CPUBindPolicy(*in.DefaultCPUBindPolicy)
	}
	return nil
}

// Convert_config_NodeNUMAResourceArgs_To_v1_NodeNUMAResourceArgs manually converts
// NodeNUMAResourceArgs from internal config type to v1.
func Convert_config_NodeNUMAResourceArgs_To_v1_NodeNUMAResourceArgs(in *config.NodeNUMAResourceArgs, out *NodeNUMAResourceArgs, s conversion.Scope) error {
	if err := autoConvert_config_NodeNUMAResourceArgs_To_v1_NodeNUMAResourceArgs(in, out, s); err != nil {
		return err
	}
	policy := CPUBindPolicy(in.DefaultCPUBindPolicy)
	out.DefaultCPUBindPolicy = &policy
	return nil
}
