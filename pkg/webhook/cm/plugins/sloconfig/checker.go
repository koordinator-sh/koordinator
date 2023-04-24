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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

const InitSuccess = "Success"
const NotInit = "NotInit"

type ConfigChecker interface {
	IsCfgNotEmptyAndChanged() bool
	InitStatus() string
	ConfigParamValid() error
	NodeConfigProfileChecker
}

type NodeConfigProfileChecker interface {
	HasMultiNodeConfigs() bool                 //nodeConfigs nums > 1
	ProfileParamValid() error                  //name must not conflict
	NodeSelectorOverlap() error                //check nodeselector if overlap
	ExistNodeConflict(node *corev1.Node) error //check a node if exist conflict
}

type CommonChecker struct {
	configKey    string
	OldConfigMap *corev1.ConfigMap
	NewConfigMap *corev1.ConfigMap

	initStatus string
	NodeConfigProfileChecker
}

func (c *CommonChecker) IsCfgNotEmptyAndChanged() bool {
	newCfgStr := c.NewConfigMap.Data[c.configKey]
	if newCfgStr == "" {
		return false
	}
	if c.OldConfigMap == nil {
		return true
	}

	oldCfgStr := c.OldConfigMap.Data[c.configKey]
	if newCfgStr != oldCfgStr {
		return true
	}
	return false
}

func (c *CommonChecker) InitStatus() string {
	return c.initStatus
}

func (c *CommonChecker) CheckByValidator(config interface{}) error {
	info, err := sloconfig.GetValidatorInstance().StructWithTrans(config)
	if err != nil {
		return err
	}
	if len(info) > 0 {
		return buildJsonError(ReasonParamInvalid, info)
	}
	return nil
}

type nodeConfigProfileChecker struct {
	cfgName     string
	nodeConfigs []profileCheckInfo
}

type profileCheckInfo struct {
	profile   extension.NodeCfgProfile
	selectors labels.Selector
}

func CreateNodeConfigProfileChecker(configName string, profiles func() []extension.NodeCfgProfile) (NodeConfigProfileChecker, error) {
	checker := &nodeConfigProfileChecker{cfgName: configName}

	nodeCfgs := profiles()
	var profileCheckInfos []profileCheckInfo
	for _, nodeConfig := range nodeCfgs {
		//checkNodeSelector empty
		if nodeConfig.NodeSelector == nil ||
			(len(nodeConfig.NodeSelector.MatchLabels) == 0 && len(nodeConfig.NodeSelector.MatchExpressions) == 0) {
			return nil, buildParamInvalidError(fmt.Errorf("nodeConfigs NodeSelector must not empty error! configType: %s,nodeConfig name:%s", configName, nodeConfig.Name))

		}

		selector, err := metav1.LabelSelectorAsSelector(nodeConfig.NodeSelector)
		if err != nil {
			return nil, buildParamInvalidError(fmt.Errorf("failed to parse node selector %v,configType: %s,nodeConfig name:%s, err: %s", nodeConfig.NodeSelector, configName, nodeConfig.Name, err.Error()))
		}

		profileCheckInfo := profileCheckInfo{profile: nodeConfig}
		profileCheckInfo.selectors = selector
		profileCheckInfos = append(profileCheckInfos, profileCheckInfo)
	}

	checker.nodeConfigs = profileCheckInfos
	return checker, nil
}

func (n *nodeConfigProfileChecker) HasMultiNodeConfigs() bool {
	return len(n.nodeConfigs) > 1
}

func (n *nodeConfigProfileChecker) ProfileParamValid() error {
	if sloconfig.IsNodeStrategyNameNeedCheck() {
		return n.checkName()
	}
	return nil
}

func (n *nodeConfigProfileChecker) checkName() error {
	nameSets := sets.String{}
	for _, nodeCfg := range n.nodeConfigs {
		//checkName
		if nodeCfg.profile.Name == "" {
			return buildParamInvalidError(fmt.Errorf("nodeConfig Name need! configType:%s", n.cfgName))
		}
		if nameSets.Has(nodeCfg.profile.Name) {
			return buildParamInvalidError(fmt.Errorf("nodeConfig Name confilict, configType:%s,nodeConfig Name:%s! ", n.cfgName, nodeCfg.profile.Name))
		}
		nameSets = nameSets.Insert(nodeCfg.profile.Name)
	}
	return nil
}

/*
NodeSelectorOverlap detect overlap by nodeSelector before checkConflict By node.
example: config1.nodeSelector{aa=true}, config2.nodeSelector{aa=true,bb=true} ,config1 contains config2
*/
func (n *nodeConfigProfileChecker) NodeSelectorOverlap() error {
	if len(n.nodeConfigs) <= 0 {
		return nil
	}
	var testNodes []virtualNode
	for _, nodeConfig := range n.nodeConfigs {
		testNodes = append(testNodes, generateNodesByNodeSelector(nodeConfig.profile.NodeSelector)...)
	}

	for _, testNode := range testNodes {
		var matchConfigs []string
		for _, nodeConfig := range n.nodeConfigs {
			nodeLabels := labels.Set(testNode)
			if nodeConfig.selectors.Matches(nodeLabels) {
				matchConfigs = append(matchConfigs, nodeConfig.profile.Name)
			}
		}
		if len(matchConfigs) > 1 {
			return buildJsonError(ReasonOverlap, &ExsitNodeConflictMessage{
				Config:                 n.cfgName,
				ConflictNodeStrategies: matchConfigs,
			})
		}
	}
	return nil
}

/*
ExistNodeConflict checkConflict By node.
example: config1.nodeSelector{aa=true}, config2.nodeSelector{aa=true,bb=true} conflict with node have label{aa=true,bb=true}
*/
func (n *nodeConfigProfileChecker) ExistNodeConflict(node *corev1.Node) error {
	if !n.HasMultiNodeConfigs() {
		return nil
	}
	nodeLabels := labels.Set(node.Labels)
	var matchConfigs []string
	for _, nodeConfig := range n.nodeConfigs {
		if nodeConfig.selectors.Matches(nodeLabels) {
			matchConfigs = append(matchConfigs, nodeConfig.profile.Name)
		}
	}
	if len(matchConfigs) > 1 {
		return buildJsonError(ReasonExistNodeConfict, &ExsitNodeConflictMessage{
			Config:                 n.cfgName,
			ConflictNodeStrategies: matchConfigs,
			ExampleNodes:           []string{node.Name},
		})
	}
	return nil
}
