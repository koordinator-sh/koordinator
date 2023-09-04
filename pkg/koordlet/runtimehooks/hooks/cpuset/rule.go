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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/util/cpuset"
)

type cpusetRule struct {
	kubeletPolicy   ext.KubeletCPUManagerPolicy
	sharePools      []ext.CPUSharedPool
	beSharePools    []ext.CPUSharedPool
	systemQOSCPUSet string
}

func (r *cpusetRule) getContainerCPUSet(containerReq *protocol.ContainerRequest) (*string, error) {
	// pod specifies QoS=BE and share pool id in annotations, use part be cpu share pool if BECPUManager enabled
	// pod specifies share pool id in annotations, use part cpu share pool
	// pod specifies QoS=SYSTEM in labels, use system qos resource if rule exist
	// pod specifies QoS=LS in labels, use all share pool
	// besteffort pod(including QoS=BE) will be managed by cpu suppress policy, inject empty string
	// guaranteed/bustable pod without QoS label, if kubelet use none policy, use all share pool, and if kubelet use
	// static policy, do nothing
	if containerReq == nil {
		return nil, nil
	}
	podAnnotations := containerReq.PodAnnotations
	podLabels := containerReq.PodLabels
	podAlloc, err := ext.GetResourceStatus(podAnnotations)
	if err != nil {
		return nil, err
	}

	podQOSClass := ext.GetQoSClassByAttrs(podLabels, podAnnotations)
	if len(podAlloc.NUMANodeResources) != 0 {
		getCPUFromSharePoolByAllocFn := func(sharePools []ext.CPUSharedPool, alloc *ext.ResourceStatus) string {
			cpusetList := make([]string, 0, len(alloc.NUMANodeResources))
			for _, numaNode := range alloc.NUMANodeResources {
				for _, nodeSharePool := range sharePools {
					if numaNode.Node == nodeSharePool.Node {
						cpusetList = append(cpusetList, nodeSharePool.CPUSet)
					}
				}
			}
			return strings.Join(cpusetList, ",")
		}
		if podQOSClass == ext.QoSBE && features.DefaultKoordletFeatureGate.Enabled(features.BECPUManager) {
			// BE pods which have specified cpu share pool
			cpuSetStr := getCPUFromSharePoolByAllocFn(r.beSharePools, podAlloc)
			klog.V(6).Infof("get cpuset from specified be cpushare pool for container %v/%v",
				containerReq.PodMeta.String(), containerReq.ContainerMeta.Name)
			return pointer.String(cpuSetStr), nil
		} else if podQOSClass != ext.QoSBE {
			// LS pods which have specified cpu share pool
			cpuSetStr := getCPUFromSharePoolByAllocFn(r.sharePools, podAlloc)
			klog.V(6).Infof("get cpuset from specified cpushare pool for container %v/%v",
				containerReq.PodMeta.String(), containerReq.ContainerMeta.Name)
			return pointer.String(cpuSetStr), nil
		}
	}

	allSharePoolCPUs := make([]string, 0, len(r.sharePools))
	for _, nodeSharePool := range r.sharePools {
		allSharePoolCPUs = append(allSharePoolCPUs, nodeSharePool.CPUSet)
	}

	if podQOSClass == ext.QoSSystem && len(r.systemQOSCPUSet) > 0 {
		klog.V(6).Infof("get cpuset from system qos rule for container %v/%v",
			containerReq.PodMeta.String(), containerReq.ContainerMeta.Name)
		return pointer.String(r.systemQOSCPUSet), nil
	} else if podQOSClass == ext.QoSLS {
		// LS pods use all share pool
		klog.V(6).Infof("get cpuset from all share pool for container %v/%v",
			containerReq.PodMeta.String(), containerReq.ContainerMeta.Name)
		return pointer.String(strings.Join(allSharePoolCPUs, ",")), nil
	}

	kubeQOS := util.GetKubeQoSByCgroupParent(containerReq.CgroupParent)
	if kubeQOS == corev1.PodQOSBestEffort {
		// besteffort pods including QoS=BE, clear cpuset of BE container to avoid conflict with kubelet static policy,
		// which will pass cpuset in StartContainerRequest of CRI
		// TODO remove this in the future since cpu suppress will keep besteffort dir as all cpuset
		klog.V(6).Infof("get empty cpuset for be container %v/%v",
			containerReq.PodMeta.String(), containerReq.ContainerMeta.Name)
		return pointer.String(""), nil
	}

	if r.kubeletPolicy.Policy == ext.KubeletCPUManagerPolicyStatic {
		klog.V(6).Infof("get empty cpuset if kubelet is static policy for container %v/%v",
			containerReq.PodMeta.String(), containerReq.ContainerMeta.Name)
		return nil, nil
	} else {
		// none policy
		klog.V(6).Infof("get cpuset from all share pool if kubelet is none policy for container %v/%v",
			containerReq.PodMeta.String(), containerReq.ContainerMeta.Name)
		return pointer.String(strings.Join(allSharePoolCPUs, ",")), nil
	}
}

func (r *cpusetRule) getHostAppCpuset(hostAppReq *protocol.HostAppRequest) (*string, error) {
	if hostAppReq == nil {
		return nil, nil
	}
	if hostAppReq.QOSClass != ext.QoSLS {
		return nil, fmt.Errorf("only LS is supported for host application %v", hostAppReq.Name)
	}
	allSharePoolCPUs := make([]string, 0, len(r.sharePools))
	for _, nodeSharePool := range r.sharePools {
		allSharePoolCPUs = append(allSharePoolCPUs, nodeSharePool.CPUSet)
	}
	klog.V(6).Infof("get cpuset from all share pool for host application %v", hostAppReq.Name)
	return pointer.String(strings.Join(allSharePoolCPUs, ",")), nil
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
	beCPUSharePools, err := ext.GetNodeBECPUSharePools(nodeTopo.Annotations)
	if err != nil {
		return false, err
	}
	cpuManagerPolicy, err := ext.GetKubeletCPUManagerPolicy(nodeTopo.Annotations)
	if err != nil {
		return false, err
	}

	systemQOSCPUSet := ""
	systemQOSRes, err := ext.GetSystemQOSResource(nodeTopo.Annotations)
	if err != nil {
		return false, err
	} else if systemQOSRes != nil {
		// check cpuset format
		if _, err := cpuset.Parse(systemQOSRes.CPUSet); err != nil {
			return false, err
		} else {
			systemQOSCPUSet = systemQOSRes.CPUSet
		}
	}

	newRule := &cpusetRule{
		kubeletPolicy:   *cpuManagerPolicy,
		sharePools:      cpuSharePools,
		beSharePools:    beCPUSharePools,
		systemQOSCPUSet: systemQOSCPUSet,
	}
	updated := p.updateRule(newRule)
	return updated, nil
}

func (p *cpusetPlugin) ruleUpdateCb(target *statesinformer.CallbackTarget) error {
	if target == nil {
		klog.Warningf("callback target is nil")
		return nil
	}
	for _, podMeta := range target.Pods {
		for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
			containerCtx := &protocol.ContainerContext{}
			containerCtx.FromReconciler(podMeta, containerStat.Name, false)
			if err := p.SetContainerCPUSet(containerCtx); err != nil {
				klog.V(4).Infof("parse cpuset from pod annotation failed during callback, error: %v", err)
				continue
			}
			containerCtx.ReconcilerDone(p.executor)
		}

		sandboxContainerCtx := &protocol.ContainerContext{}
		sandboxContainerCtx.FromReconciler(podMeta, "", true)
		if err := p.SetContainerCPUSet(sandboxContainerCtx); err != nil {
			klog.Warningf("set cpuset for failed for pod sandbox %v/%v, error %v",
				sandboxContainerCtx.Request.PodMeta.String(), sandboxContainerCtx.Request.ContainerMeta.ID, err)
			continue
		}
		sandboxContainerCtx.ReconcilerDone(p.executor)
		klog.V(5).Infof("set cpuset finished pod sandbox %v/%v",
			sandboxContainerCtx.Request.PodMeta.String(), sandboxContainerCtx.Request.ContainerMeta.ID)
	}
	for _, hostApp := range target.HostApplications {
		hostCtx := protocol.HooksProtocolBuilder.HostApp(&hostApp)
		if err := p.SetHostAppCPUSet(hostCtx); err != nil {
			klog.Warningf("set host application %v cpuset value failed, error %v", hostApp.Name, err)
		} else {
			hostCtx.ReconcilerDone(p.executor)
			klog.V(5).Infof("set host application %v cpuset value finished", hostApp.Name)
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
