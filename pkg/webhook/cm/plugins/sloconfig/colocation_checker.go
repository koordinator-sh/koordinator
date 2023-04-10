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

var _ ConfigChecker = &ColocationConfigChecker{}

type ColocationConfigChecker struct {
	cfg *extension.ColocationCfg
	CommonChecker
}

func NewColocationConfigChecker(oldConfig, newConfig *corev1.ConfigMap, needUnmarshal bool) *ColocationConfigChecker {
	checker := &ColocationConfigChecker{CommonChecker: CommonChecker{OldConfigMap: oldConfig, NewConfigMap: newConfig, configKey: extension.ColocationConfigKey, initStatus: NotInit}}
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
	clusterCfg := c.cfg.ColocationStrategy
	err := checkColocationStrategy(clusterCfg)
	if err != nil {
		return buildParamInvalidError(fmt.Errorf("check colocation cluster cfg fail! error:%s", err.Error()))
	}
	for _, nodeCfg := range c.cfg.NodeConfigs {
		err = checkColocationStrategy(nodeCfg.ColocationStrategy)
		if err != nil {
			return buildParamInvalidError(fmt.Errorf("check colocation node cfg fail! name(%s),error:%s", nodeCfg.Name, err.Error()))
		}
	}
	return nil
}

func checkColocationStrategy(cfg extension.ColocationStrategy) error {
	if cfg.MetricAggregateDurationSeconds != nil && *cfg.MetricAggregateDurationSeconds <= 0 {
		return fmt.Errorf("MetricAggregateDurationSeconds invalid,value:%d", *cfg.MetricAggregateDurationSeconds)
	}
	if cfg.UpdateTimeThresholdSeconds != nil && *cfg.UpdateTimeThresholdSeconds <= 0 {
		return fmt.Errorf("UpdateTimeThresholdSeconds invalid,value:%d", *cfg.UpdateTimeThresholdSeconds)
	}
	if cfg.DegradeTimeMinutes != nil && *cfg.DegradeTimeMinutes <= 0 {
		return fmt.Errorf("DegradeTimeMinutes invalid,value:%d", *cfg.DegradeTimeMinutes)
	}
	if isValueInvalidForPercent(cfg.CPUReclaimThresholdPercent) {
		return fmt.Errorf("CPUReclaimThresholdPercent invalid,value:%d", *cfg.CPUReclaimThresholdPercent)
	}
	if isValueInvalidForPercent(cfg.MemoryReclaimThresholdPercent) {
		return fmt.Errorf("MemoryReclaimThresholdPercent invalid,value:%v", *cfg.MemoryReclaimThresholdPercent)
	}
	if cfg.ResourceDiffThreshold != nil &&
		(*cfg.ResourceDiffThreshold <= 0 || *cfg.ResourceDiffThreshold >= 1) {
		return fmt.Errorf("ResourceDiffThreshold invalid,value:%f", *cfg.ResourceDiffThreshold)
	}
	return nil
}

func (c *ColocationConfigChecker) initConfig() error {
	cfg := &extension.ColocationCfg{}
	configStr := c.NewConfigMap.Data[extension.ColocationConfigKey]
	err := json.Unmarshal([]byte(configStr), &cfg)
	if err != nil {
		message := fmt.Sprintf("Failed to parse colocation config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err)
		klog.Error(message)
		return buildJsonError(ReasonParseFail, message)
	}
	c.cfg = cfg

	c.NodeConfigProfileChecker, err = CreateNodeConfigProfileChecker(extension.ColocationConfigKey, c.getConfigProfiles)
	if err != nil {
		klog.Error(fmt.Sprintf("Failed to parse colocation config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err))
		return err
	}

	return nil
}

func (c *ColocationConfigChecker) getConfigProfiles() []extension.NodeCfgProfile {
	var profiles []extension.NodeCfgProfile
	for _, nodeCfg := range c.cfg.NodeConfigs {
		profiles = append(profiles, nodeCfg.NodeCfgProfile)
	}
	return profiles
}
