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

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

type checkers []ConfigChecker

func CreateCheckersChanged(oldConfig *corev1.ConfigMap, config *corev1.ConfigMap) checkers {
	checkersAll := CreateCheckersAll(oldConfig, config, false)
	var cfgChangedCheckers checkers
	for _, checker := range checkersAll {
		if !checker.IsCfgNotEmptyAndChanged() {
			continue
		}
		cfgChangedCheckers = append(cfgChangedCheckers, checker)
	}

	return cfgChangedCheckers
}

func CreateCheckersAll(oldConfig *corev1.ConfigMap, config *corev1.ConfigMap, needUnmarshal bool) checkers {
	return checkers{
		NewColocationConfigChecker(oldConfig, config, needUnmarshal),
		NewResourceThresholdChecker(oldConfig, config, needUnmarshal),
		NewResourceQOSChecker(oldConfig, config, needUnmarshal),
		NewSystemConfigChecker(oldConfig, config, needUnmarshal),
		NewCPUBurstChecker(oldConfig, config, needUnmarshal),
	}
}

func (c checkers) CheckConfigContents() error {
	for _, checker := range c {
		if checker.InitStatus() != InitSuccess {
			return fmt.Errorf(checker.InitStatus())
		}
		err := checker.ConfigParamValid()
		if err != nil {
			return err
		}
		err = checker.ProfileParamValid()
		if err != nil {
			return err
		}
		err = checker.NodeSelectorOverlap()
		if err != nil {
			return err
		}
	}
	return nil
}

// NeedCheckForNodes  if exist checker have multi nodeConfig, then should check for nodes
func (c checkers) NeedCheckForNodes() bool {
	for _, checkers := range c {
		if checkers.HasMultiNodeConfigs() {
			return true
		}
	}
	return false
}
