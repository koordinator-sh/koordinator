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

var _ ConfigChecker = &CPUBurstChecker{}

type CPUBurstChecker struct {
	cfg *extension.CPUBurstCfg
	CommonChecker
}

func NewCPUBurstChecker(oldConfig, newConfig *corev1.ConfigMap, needUnmarshal bool) *CPUBurstChecker {
	checker := &CPUBurstChecker{CommonChecker: CommonChecker{OldConfigMap: oldConfig, NewConfigMap: newConfig, configKey: extension.CPUBurstConfigKey, initStatus: NotInit}}
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

func (c *CPUBurstChecker) ConfigParamValid() error {
	clusterCfg := c.cfg.ClusterStrategy
	if clusterCfg != nil {
		err := checkCPUBurstStrategy(*clusterCfg)
		if err != nil {
			return buildParamInvalidError(fmt.Errorf("check CPUBurst cluster cfg fail! error:%s", err.Error()))
		}
	}
	for _, nodeCfg := range c.cfg.NodeStrategies {
		if nodeCfg.CPUBurstStrategy != nil {
			err := checkCPUBurstStrategy(*nodeCfg.CPUBurstStrategy)
			if err != nil {
				return buildParamInvalidError(fmt.Errorf("check CPUBurst node cfg fail! name(%s),error:%s", nodeCfg.Name, err.Error()))
			}
		}
	}
	return nil
}

func checkCPUBurstStrategy(cfg slov1alpha1.CPUBurstStrategy) error {
	if cfg.CPUBurstPercent != nil && *cfg.CPUBurstPercent <= 0 {
		return fmt.Errorf("CPUBurstPercent invalid,value:%d", *cfg.CPUBurstPercent)
	}
	if cfg.CFSQuotaBurstPercent != nil && *cfg.CFSQuotaBurstPercent <= 0 {
		return fmt.Errorf("CFSQuotaBurstPercent invalid,value:%d", *cfg.CFSQuotaBurstPercent)
	}
	if cfg.CFSQuotaBurstPeriodSeconds != nil && *cfg.CFSQuotaBurstPeriodSeconds <= 0 {
		return fmt.Errorf("CFSQuotaBurstPeriodSeconds invalid,value:%d", *cfg.CFSQuotaBurstPeriodSeconds)
	}
	return nil
}

func (c *CPUBurstChecker) initConfig() error {
	cfg := &extension.CPUBurstCfg{}
	configStr := c.NewConfigMap.Data[extension.CPUBurstConfigKey]
	err := json.Unmarshal([]byte(configStr), &cfg)
	if err != nil {
		message := fmt.Sprintf("Failed to parse CpuBurst config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error())
		klog.Error(message)
		return buildJsonError(ReasonParseFail, message)
	}
	c.cfg = cfg

	c.NodeConfigProfileChecker, err = CreateNodeConfigProfileChecker(extension.CPUBurstConfigKey, c.getConfigProfiles)
	if err != nil {
		klog.Error(fmt.Sprintf("Failed to parse CpuBurst config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error()))
		return err
	}

	return nil
}

func (c *CPUBurstChecker) getConfigProfiles() []extension.NodeCfgProfile {
	var profiles []extension.NodeCfgProfile
	for _, nodeCfg := range c.cfg.NodeStrategies {
		profiles = append(profiles, nodeCfg.NodeCfgProfile)
	}
	return profiles
}
