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

package cpunormalization

import (
	"fmt"
	"math"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
	"github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	name        = "CPUNormalization"
	description = "adjust cpu cgroups value for cpu normalized LS pod"
)

var podQOSConditions = []string{string(extension.QoSLS), string(extension.QoSNone)}

type Plugin struct {
	rule     *Rule
	executor resourceexecutor.ResourceUpdateExecutor
}

var singleton *Plugin

func Object() *Plugin {
	if singleton == nil {
		singleton = newPlugin()
	}
	return singleton
}

func newPlugin() *Plugin {
	return &Plugin{
		rule: newRule(),
	}
}

func (p *Plugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", name)
	rule.Register(name, description,
		rule.WithParseFunc(statesinformer.RegisterTypeNodeMetadata, p.parseRule),
		rule.WithUpdateCallback(p.ruleUpdateCb))
	hooks.Register(rmconfig.PreRunPodSandbox, name, description+" (pod)", p.AdjustPodCFSQuota)
	hooks.Register(rmconfig.PreCreateContainer, name, description+" (container)", p.AdjustContainerCFSQuota)
	hooks.Register(rmconfig.PreUpdateContainerResources, name, description+" (container)", p.AdjustContainerCFSQuota)
	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, sysutil.CPUCFSQuota, description+" (pod cfs quota)",
		p.AdjustPodCFSQuota, reconciler.PodQOSFilter(), podQOSConditions...)
	reconciler.RegisterCgroupReconciler(reconciler.ContainerLevel, sysutil.CPUCFSQuota, description+" (container cfs quota)",
		p.AdjustContainerCFSQuota, reconciler.PodQOSFilter(), podQOSConditions...)
	p.executor = op.Executor
}

func (p *Plugin) AdjustPodCFSQuota(proto protocol.HooksProtocol) error {
	podCtx := proto.(*protocol.PodContext)
	if podCtx == nil {
		return fmt.Errorf("pod protocol is nil for plugin %s", name)
	}
	if podCtx.Request.Resources == nil { // currently only reconciler mode provides Resources in ctx
		return nil
	}

	if !isPodCPUShare(podCtx.Request.Labels, podCtx.Request.Annotations) {
		return nil
	}

	return p.adjustPodCFSQuota(podCtx)
}

func (p *Plugin) AdjustContainerCFSQuota(proto protocol.HooksProtocol) error {
	containerCtx := proto.(*protocol.ContainerContext)
	if containerCtx == nil {
		return fmt.Errorf("container protocol is nil for plugin %s", name)
	}
	if containerCtx.Request.Resources == nil { // currently only reconciler mode provides Resources in ctx
		return nil
	}

	if !isPodCPUShare(containerCtx.Request.PodLabels, containerCtx.Request.PodAnnotations) {
		return nil
	}

	return p.adjustContainerCFSQuota(containerCtx)
}

func (p *Plugin) adjustPodCFSQuota(podCtx *protocol.PodContext) error {
	if !p.rule.IsEnabled() {
		return nil
	}

	originalCFSQuota := podCtx.Request.Resources.CFSQuota
	if originalCFSQuota == nil || *originalCFSQuota <= 0 { // no need to scale when cgroup is unset
		return nil
	}

	ratio := p.rule.GetCPUNormalizationRatio()
	cfsQuota := *originalCFSQuota
	if ratio > 1.0 {
		cfsQuota = int64(math.Ceil(float64(*originalCFSQuota) / ratio))
		klog.V(6).Infof("plugin %s adjusts pod %s/%s cfs quota from %d to %d",
			name, podCtx.Request.PodMeta.Namespace, podCtx.Request.PodMeta.Name, *originalCFSQuota, cfsQuota)
	}

	podCtx.Response.Resources.CFSQuota = &cfsQuota
	return nil
}

func (p *Plugin) adjustContainerCFSQuota(containerCtx *protocol.ContainerContext) error {
	if !p.rule.IsEnabled() {
		return nil
	}

	originalCFSQuota := containerCtx.Request.Resources.CFSQuota
	if originalCFSQuota == nil || *originalCFSQuota <= 0 { // no need to scale when cgroup is unset
		return nil
	}

	ratio := p.rule.GetCPUNormalizationRatio()
	cfsQuota := *originalCFSQuota
	if ratio > 1.0 {
		cfsQuota = int64(math.Ceil(float64(*originalCFSQuota) / ratio))
		klog.V(6).Infof("plugin %s adjusts container %s/%s/%s cfs quota from %d to %d", name,
			containerCtx.Request.PodMeta.Namespace, containerCtx.Request.PodMeta.Name,
			containerCtx.Request.ContainerMeta.Name, *originalCFSQuota, cfsQuota)
	}

	containerCtx.Response.Resources.CFSQuota = &cfsQuota
	return nil
}

func isPodCPUShare(labels map[string]string, annotations map[string]string) bool {
	if labels == nil { // considered None
		return true
	}

	qosClass := extension.GetQoSClassByAttrs(labels, annotations)
	// consider as LSR if pod is qos=None and has cpuset
	if qosClass == extension.QoSNone && annotations != nil {
		cpuset, _ := util.GetCPUSetFromPod(annotations)
		if len(cpuset) > 0 {
			return false
		}
	}

	return qosClass == extension.QoSLS || qosClass == extension.QoSNone
}
