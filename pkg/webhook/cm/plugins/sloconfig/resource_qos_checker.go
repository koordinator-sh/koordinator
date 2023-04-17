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
	clusterCfg := c.cfg.ClusterStrategy
	if clusterCfg != nil {
		err := checkResourceQOSStrategy(*clusterCfg)
		if err != nil {
			return buildParamInvalidError(fmt.Errorf("check ResourceQOS cluster cfg fail! error:%s", err.Error()))
		}
	}
	for _, nodeCfg := range c.cfg.NodeStrategies {
		if nodeCfg.ResourceQOSStrategy != nil {
			err := checkResourceQOSStrategy(*nodeCfg.ResourceQOSStrategy)
			if err != nil {
				return buildParamInvalidError(fmt.Errorf("check ResourceQOS node cfg fail! name(%s),error:%s", nodeCfg.Name, err.Error()))
			}
		}
	}
	return nil
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

func checkResourceQOSStrategy(cfg slov1alpha1.ResourceQOSStrategy) error {

	err := checkResourceQOS(cfg.LSRClass)
	if err != nil {
		return err
	}
	err = checkResourceQOS(cfg.LSClass)
	if err != nil {
		return err
	}
	err = checkResourceQOS(cfg.BEClass)
	if err != nil {
		return err
	}
	err = checkResourceQOS(cfg.CgroupRoot)
	if err != nil {
		return err
	}
	return nil
}

func checkResourceQOS(resource *slov1alpha1.ResourceQOS) error {
	if resource == nil {
		return nil
	}
	if resource.CPUQOS != nil {
		err := checkCPUQOS(resource.CPUQOS.CPUQOS)
		if err != nil {
			return err
		}
	}
	if resource.MemoryQOS != nil {
		err := checkMemoryQOS(resource.MemoryQOS.MemoryQOS)
		if err != nil {
			return err
		}
	}
	if resource.ResctrlQOS != nil {
		err := checkResctrlQOS(resource.ResctrlQOS.ResctrlQOS)
		if err != nil {
			return err
		}
	}
	return nil
}

func checkCPUQOS(cfg slov1alpha1.CPUQOS) error {
	if cfg.GroupIdentity != nil && (*cfg.GroupIdentity < -1 || *cfg.GroupIdentity > 2) {
		return fmt.Errorf("GroupIdentity invalid,value:%d", *cfg.GroupIdentity)
	}
	return nil
}

func checkMemoryQOS(cfg slov1alpha1.MemoryQOS) error {
	if isValueInvalidForPercent(cfg.MinLimitPercent) {
		return fmt.Errorf("MinLimitPercent invalid,value:%d", *cfg.MinLimitPercent)
	}
	if isValueInvalidForPercent(cfg.LowLimitPercent) {
		return fmt.Errorf("LowLimitPercent invalid,value:%d", *cfg.LowLimitPercent)
	}
	if isValueInvalidForPercent(cfg.ThrottlingPercent) {
		return fmt.Errorf("ThrottlingPercent invalid,value:%d", *cfg.ThrottlingPercent)
	}
	if isValueInvalidForPercent(cfg.WmarkRatio) {
		return fmt.Errorf("WmarkRatio invalid,value:%d", *cfg.WmarkRatio)
	}
	if isValueInvalidForPercent(cfg.WmarkScalePermill) {
		return fmt.Errorf("WmarkScalePermill invalid,value:%d", *cfg.WmarkScalePermill)
	}
	if cfg.WmarkMinAdj != nil && (*cfg.WmarkMinAdj < -25 || *cfg.WmarkMinAdj > 50) {
		return fmt.Errorf("WmarkMinAdj invalid,value:%d", *cfg.WmarkMinAdj)
	}
	if cfg.PriorityEnable != nil && (*cfg.PriorityEnable < 0 || *cfg.PriorityEnable > 1) {
		return fmt.Errorf("PriorityEnable invalid,value:%d", *cfg.PriorityEnable)
	}
	if cfg.Priority != nil && (*cfg.Priority < 0 || *cfg.Priority > 12) {
		return fmt.Errorf("Priority invalid, value:%d ", *cfg.Priority)
	}
	if cfg.OomKillGroup != nil && (*cfg.OomKillGroup < 0 || *cfg.OomKillGroup > 1) {
		return fmt.Errorf("OomKillGroup invalid, value:%d ", *cfg.OomKillGroup)
	}
	return nil
}

func checkResctrlQOS(cfg slov1alpha1.ResctrlQOS) error {
	if isValueInvalidForPercent(cfg.CATRangeStartPercent) {
		return fmt.Errorf("CATRangeStartPercent invalid,value:%d", *cfg.CATRangeStartPercent)
	}
	if isValueInvalidForPercent(cfg.CATRangeEndPercent) {
		return fmt.Errorf("CATRangeEndPercent invalid,value:%d", *cfg.CATRangeEndPercent)
	}
	if isValueInvalidForPercent(cfg.MBAPercent) {
		return fmt.Errorf("CATRangeEndPercent invalid,value:%d", *cfg.MBAPercent)
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
