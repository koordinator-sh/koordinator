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

package sloconfig

import "flag"

const (
	Zh = "zh"
	En = "en"
)

var (
	// ConfigNameSpace is the namespace of the slo-controller configmap.
	ConfigNameSpace = "koordinator-system"
	// SLOCtrlConfigMap is the name of the slo-controller configmap.
	SLOCtrlConfigMap = "slo-controller-config"
	// DefaultTranslator = "en"
	DefaultTranslator = "en"
	// NodeStrategyNameNeedCheck true:enable to check name required and not conflict, false: not check
	NodeStrategyNameNeedCheck = "false"
)

func InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&SLOCtrlConfigMap, "slo-config-name", SLOCtrlConfigMap, "determines the name the slo-controller configmap uses.")
	fs.StringVar(&ConfigNameSpace, "config-namespace", ConfigNameSpace, "determines the namespace of configmap uses.")
	fs.StringVar(&DefaultTranslator, "default-config-translator", DefaultTranslator, "determines the sloConfig validator translator. e.g. 'en', 'zh'")
	fs.StringVar(&NodeStrategyNameNeedCheck, "node-strategy-name-need-check", NodeStrategyNameNeedCheck, "determines the sloConfig validator nodeConfig name check enable, 'true':enable, 'false':unable, default:false.")
}

func IsNodeStrategyNameNeedCheck() bool {
	return NodeStrategyNameNeedCheck == "true"
}
