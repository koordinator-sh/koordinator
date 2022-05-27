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

package config

const (
	// SLO configmap name
	ConfigNameSpace  = "koordinator-system"
	SLOCtrlConfigMap = "slo-controller-config"

	// keys in the configmap
	ColocationConfigKey        = "colocation-config"
	ResourceThresholdConfigKey = "resource-threshold-config"
	ResourceQoSConfigKey       = "resource-qos-config"
	CPUBurstConfigKey          = "cpu-burst-config"
)

/*
Koordinator uses configmap to manage the configuration of SLO, the configmap is stored in
 <ConfigNameSpace>/<SLOCtrlConfigMap>, with the following keys respectively:
   - <ColocationConfigKey>
   - <ResourceThresholdConfigKey>
   - <ResourceQoSConfigKey>
   - <CPUBurstConfigKey>

et.
  TODO add a sample here
*/
