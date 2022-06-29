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

package cpuset

import (
	"fmt"
	"reflect"
	"strings"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

type cpusetRule struct {
	sharePools []ext.CPUSharedPool
}

func (r *cpusetRule) getContainerCPUSet(containerReq *protocol.ContainerRequest) (string, error) {
	if containerReq == nil {
		return "", nil
	}
	podAnnotations := containerReq.PodAnnotations
	podLabels := containerReq.PodLabels
	podAlloc, err := ext.GetResourceStatus(podAnnotations)
	if err != nil {
		return "", err
	}

	if len(podAlloc.CPUSharedPools) != 0 {
		// pod specified cpu share pool
		cpusetList := make([]string, 0, len(podAlloc.CPUSharedPools))
		for _, specifiedSharePool := range podAlloc.CPUSharedPools {
			for _, nodeSharePool := range r.sharePools {
				if specifiedSharePool.Socket == nodeSharePool.Socket && specifiedSharePool.Node == nodeSharePool.Node {
					cpusetList = append(cpusetList, nodeSharePool.CPUSet)
				}
			}
		}
		return strings.Join(cpusetList, ","), nil
	}

	kubeQOS := util.GetKubeQoSByCgroupParent(containerReq.CgroupParent)
	podQOSClass := ext.GetQoSClassByLabels(podLabels)
	isKubeOriginLS := podQOSClass == ext.QoSNone && kubeQOS != corev1.PodQOSBestEffort
	if podQOSClass == ext.QoSLS || isKubeOriginLS {
		// LS pod which does not specified cpu, bind all cpu share pool
		// TODO use dynamic binding policy in the future
		allSharePoolCPUs := make([]string, 0, len(r.sharePools))
		for _, nodeSharePool := range r.sharePools {
			allSharePoolCPUs = append(allSharePoolCPUs, nodeSharePool.CPUSet)
		}
		return strings.Join(allSharePoolCPUs, ","), nil
	}
	return "", nil
}

func (p *cpusetPlugin) parseRule(nodeTopoIf interface{}) (bool, error) {
	nodeTopo, ok := nodeTopoIf.(*topov1alpha1.NodeResourceTopology)
	if !ok {
		return false, fmt.Errorf("parse format for hook plugin %v failed, expect: %v, got: %T",
			name, "*topov1alpha1.NodeResourceTopology", nodeTopoIf)
	}
	cpuSharePools, err := ext.GetNodeCPUSharePools(nodeTopo.Annotations)
	if err != nil {
		return false, err
	}
	newRule := &cpusetRule{
		sharePools: cpuSharePools,
	}
	updated := p.updateRule(newRule)
	return updated, nil
}

func (p *cpusetPlugin) ruleUpdateCb(pods []*statesinformer.PodMeta) error {
	for _, podMeta := range pods {
		for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
			containerCtx := &protocol.ContainerContext{}
			containerCtx.FromReconciler(podMeta, containerStat.Name)
			if err := p.SetContainerCPUSet(containerCtx); err != nil {
				klog.Infof("parse cpuset from pod annotation failed during callback, error: %v", err)
				continue
			}
			containerCtx.ReconcilerDone()
		}
	}
	return nil
}

func (p *cpusetPlugin) getRule() *cpusetRule {
	p.ruleRWMutex.RLock()
	defer p.ruleRWMutex.RUnlock()
	if p.rule == nil {
		return nil
	}
	rule := *p.rule
	return &rule
}

func (p *cpusetPlugin) updateRule(newRule *cpusetRule) bool {
	p.ruleRWMutex.RLock()
	defer p.ruleRWMutex.RUnlock()
	if !reflect.DeepEqual(newRule, p.rule) {
		p.rule = newRule
		return true
	}
	return false
}
