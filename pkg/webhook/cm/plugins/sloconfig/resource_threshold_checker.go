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
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

var _ ConfigChecker = &ResourceThresholdChecker{}

type ResourceThresholdChecker struct {
	cfg *extension.ResourceThresholdCfg
	CommonChecker
}

func NewResourceThresholdChecker(oldConfig, newConfig *corev1.ConfigMap, needUnmarshal bool) *ResourceThresholdChecker {
	checker := &ResourceThresholdChecker{CommonChecker: CommonChecker{OldConfigMap: oldConfig, NewConfigMap: newConfig, configKey: extension.ResourceThresholdConfigKey, initStatus: NotInit}}
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

func (c *ResourceThresholdChecker) ConfigParamValid() error {
	clusterCfg := c.cfg.ClusterStrategy
	if clusterCfg != nil {
		err := checkThresholdStrategy(*clusterCfg)
		if err != nil {
			return buildParamInvalidError(fmt.Errorf("check ResourceThreshold cluster cfg fail! error:%s", err.Error()))
		}
	}
	for _, nodeCfg := range c.cfg.NodeStrategies {
		if nodeCfg.ResourceThresholdStrategy != nil {
			err := checkThresholdStrategy(*nodeCfg.ResourceThresholdStrategy)
			if err != nil {
				return buildParamInvalidError(fmt.Errorf("check ResourceThreshold node cfg fail! name(%s),error:%s", nodeCfg.Name, err.Error()))
			}
		}
	}
	return nil
}

func (c *ResourceThresholdChecker) initConfig() error {
	cfg := &extension.ResourceThresholdCfg{}
	configStr := c.NewConfigMap.Data[extension.ResourceThresholdConfigKey]
	err := json.Unmarshal([]byte(configStr), &cfg)
	if err != nil {
		message := fmt.Sprintf("Failed to parse resourceThreshold config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error())
		klog.Error(message)
		return buildJsonError(ReasonParseFail, message)
	}
	c.cfg = cfg

	c.NodeConfigProfileChecker, err = CreateNodeConfigProfileChecker(extension.ResourceThresholdConfigKey, c.getConfigProfiles)
	if err != nil {
		klog.Error(fmt.Sprintf("Failed to parse resourceThreshold config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error()))
		return err
	}

	return nil
}

func checkThresholdStrategy(cfg slov1alpha1.ResourceThresholdStrategy) error {
	if isValueInvalidForPercent(cfg.CPUSuppressThresholdPercent) {
		return fmt.Errorf("CPUSuppressThresholdPercent invalid,value:%d", *cfg.CPUSuppressThresholdPercent)
	}
	if isValueInvalidForPercent(cfg.CPUEvictBESatisfactionLowerPercent) {
		return fmt.Errorf("CPUEvictBESatisfactionLowerPercent invalid,value:%d", *cfg.CPUEvictBESatisfactionLowerPercent)
	}

	if isValueInvalidForPercent(cfg.CPUEvictBESatisfactionUpperPercent) {
		return fmt.Errorf("CPUEvictBESatisfactionUpperPercent invalid,value:%d", *cfg.CPUEvictBESatisfactionUpperPercent)
	}

	if cfg.CPUEvictBESatisfactionLowerPercent != nil && *cfg.CPUEvictBESatisfactionLowerPercent > 0 {
		if cfg.CPUEvictBESatisfactionUpperPercent == nil {
			return fmt.Errorf("CPUEvictBESatisfactionPercent invalid,upper must set when lower (%d) not nil", *cfg.CPUEvictBESatisfactionLowerPercent)
		}
		if *cfg.CPUEvictBESatisfactionUpperPercent < *cfg.CPUEvictBESatisfactionLowerPercent {
			return fmt.Errorf("CPUEvictBESatisfactionPercent invalid,upper(%d) must set  larger than lower (%d)", *cfg.CPUEvictBESatisfactionUpperPercent, *cfg.CPUEvictBESatisfactionLowerPercent)
		}
	}

	if isValueInvalidForPercent(cfg.MemoryEvictThresholdPercent) {
		return fmt.Errorf("MemoryEvictThresholdPercent invalid,value:%d", *cfg.MemoryEvictThresholdPercent)
	}
	if cfg.CPUEvictTimeWindowSeconds != nil && *cfg.CPUEvictTimeWindowSeconds <= 0 {
		return fmt.Errorf("CPUEvictTimeWindowSeconds invalid,value:%d", *cfg.CPUEvictTimeWindowSeconds)
	}
	return nil
}

func (c *ResourceThresholdChecker) getConfigProfiles() []extension.NodeCfgProfile {
	var profiles []extension.NodeCfgProfile
	for _, nodeCfg := range c.cfg.NodeStrategies {
		profiles = append(profiles, nodeCfg.NodeCfgProfile)
	}
	return profiles
}
