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

	"github.com/koordinator-sh/koordinator/apis/extension"
)

var _ ConfigChecker = &ResourceQOSChecker{}

type ResourceQOSChecker struct {
	cfg *extension.ResourceQOSCfg
	CommonChecker
}

func NewResourceQOSChecker(oldConfig, newConfig *corev1.ConfigMap, needUnmarshal bool) *ResourceQOSChecker {
	checker := &ResourceQOSChecker{CommonChecker: CommonChecker{OldConfigMap: oldConfig, NewConfigMap: newConfig, configKey: extension.ResourceQOSConfigKey, initStatus: NotInit}}
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

func (c *ResourceQOSChecker) ConfigParamValid() error {
	return c.CheckByValidator(c.cfg)
}

func (c *ResourceQOSChecker) initConfig() error {
	cfg := &extension.ResourceQOSCfg{}
	configStr := c.NewConfigMap.Data[extension.ResourceQOSConfigKey]
	err := json.Unmarshal([]byte(configStr), &cfg)
	if err != nil {
		message := fmt.Sprintf("Failed to parse ResourceQOS config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error())
		klog.Error(message)
		return buildJsonError(ReasonParseFail, message)
	}
	c.cfg = cfg

	c.NodeConfigProfileChecker, err = CreateNodeConfigProfileChecker(extension.ResourceQOSConfigKey, c.getConfigProfiles)
	if err != nil {
		klog.Error(fmt.Sprintf("Failed to parse ResourceQOS config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error()))
		return err
	}
	return nil
}

func (c *ResourceQOSChecker) getConfigProfiles() []extension.NodeCfgProfile {
	var profiles []extension.NodeCfgProfile
	for _, nodeCfg := range c.cfg.NodeStrategies {
		profiles = append(profiles, nodeCfg.NodeCfgProfile)
	}
	return profiles
}
