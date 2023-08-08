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
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	configuration "github.com/koordinator-sh/koordinator/apis/configuration"
)

var _ ConfigChecker = &ColocationConfigChecker{}

type ColocationConfigChecker struct {
	cfg *configuration.ColocationCfg
	CommonChecker
}

func NewColocationConfigChecker(oldConfig, newConfig *corev1.ConfigMap, needUnmarshal bool) *ColocationConfigChecker {
	checker := &ColocationConfigChecker{CommonChecker: CommonChecker{OldConfigMap: oldConfig, NewConfigMap: newConfig, configKey: configuration.ColocationConfigKey, initStatus: NotInit}}
	if !checker.IsCfgNotEmptyAndChanged() && !needUnmarshal {
		return checker
	}
	if err := checker.initConfig(); err != nil {
		checker.initStatus = err.Error()
	} else {
		checker.initStatus = InitSuccess
	}
	return checker
}

func (c *ColocationConfigChecker) ConfigParamValid() error {
	return c.CheckByValidator(c.cfg)
}

func (c *ColocationConfigChecker) initConfig() error {
	cfg := &configuration.ColocationCfg{}
	configStr := c.NewConfigMap.Data[configuration.ColocationConfigKey]
	err := json.Unmarshal([]byte(configStr), &cfg)
	if err != nil {
		message := fmt.Sprintf("Failed to parse colocation config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err)
		klog.Error(message)
		return buildJsonError(ReasonParseFail, message)
	}
	c.cfg = cfg

	c.NodeConfigProfileChecker, err = CreateNodeConfigProfileChecker(configuration.ColocationConfigKey, c.getConfigProfiles)
	if err != nil {
		klog.Error(fmt.Sprintf("Failed to parse colocation config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err))
		return err
	}

	return nil
}

func (c *ColocationConfigChecker) getConfigProfiles() []configuration.NodeCfgProfile {
	var profiles []configuration.NodeCfgProfile
	for _, nodeCfg := range c.cfg.NodeConfigs {
		profiles = append(profiles, nodeCfg.NodeCfgProfile)
	}
	return profiles
}
