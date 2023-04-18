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

var _ ConfigChecker = &SystemConfigChecker{}

type SystemConfigChecker struct {
	cfg *extension.SystemCfg
	CommonChecker
}

func NewSystemConfigChecker(oldConfig, newConfig *corev1.ConfigMap, needUnmarshal bool) *SystemConfigChecker {
	checker := &SystemConfigChecker{CommonChecker: CommonChecker{OldConfigMap: oldConfig, NewConfigMap: newConfig, configKey: extension.SystemConfigKey, initStatus: NotInit}}
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

func (c *SystemConfigChecker) ConfigParamValid() error {
	clusterCfg := c.cfg.ClusterStrategy
	if clusterCfg != nil {
		err := checkSystemStrategy(*clusterCfg)
		if err != nil {
			return buildParamInvalidError(fmt.Errorf("check System config cluster cfg fail! error:%s", err.Error()))
		}
	}
	for _, nodeCfg := range c.cfg.NodeStrategies {
		if nodeCfg.SystemStrategy != nil {
			err := checkSystemStrategy(*nodeCfg.SystemStrategy)
			if err != nil {
				return buildParamInvalidError(fmt.Errorf("check System config node cfg fail! name(%s),error:%s", nodeCfg.Name, err.Error()))
			}
		}
	}
	return nil
}

func checkSystemStrategy(cfg slov1alpha1.SystemStrategy) error {

	if cfg.MinFreeKbytesFactor != nil && *cfg.MinFreeKbytesFactor <= 0 {
		return fmt.Errorf("MinFreeKbytesFactor invalid,value:%d", *cfg.MinFreeKbytesFactor)
	}
	if cfg.WatermarkScaleFactor != nil && *cfg.WatermarkScaleFactor <= 0 {
		return fmt.Errorf("WatermarkScaleFactor invalid,value:%d", *cfg.WatermarkScaleFactor)
	}
	if cfg.MemcgReapBackGround != nil && (*cfg.MemcgReapBackGround != 0 && *cfg.MemcgReapBackGround != 1) {
		return fmt.Errorf("MemcgReapBackGround invalid,value:%d", *cfg.MemcgReapBackGround)
	}
	return nil
}

func (c *SystemConfigChecker) initConfig() error {
	cfg := &extension.SystemCfg{}
	configStr := c.NewConfigMap.Data[extension.SystemConfigKey]
	err := json.Unmarshal([]byte(configStr), &cfg)
	if err != nil {
		message := fmt.Sprintf("Failed to parse System config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error())
		klog.Error(message)
		return buildJsonError(ReasonParseFail, message)
	}
	c.cfg = cfg

	c.NodeConfigProfileChecker, err = CreateNodeConfigProfileChecker(extension.SystemConfigKey, c.getConfigProfiles)
	if err != nil {
		klog.Error(fmt.Sprintf("Failed to parse System config in configmap %s/%s, err: %s",
			c.NewConfigMap.Namespace, c.NewConfigMap.Name, err.Error()))
		return err
	}

	return nil
}

func (c *SystemConfigChecker) getConfigProfiles() []extension.NodeCfgProfile {
	var profiles []extension.NodeCfgProfile
	for _, nodeCfg := range c.cfg.NodeStrategies {
		profiles = append(profiles, nodeCfg.NodeCfgProfile)
	}
	return profiles
}
