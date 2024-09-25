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

package groupidentity

import (
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

func (b *bvtPlugin) SetPodBvtValue(p protocol.HooksProtocol) error {
	r := b.prepare()
	if r == nil {
		return nil
	}

	podCtx := p.(*protocol.PodContext)
	req := podCtx.Request
	podQOS := ext.GetQoSClassByAttrs(req.Labels, req.Annotations)
	podKubeQOS := util.GetKubeQoSByCgroupParent(req.CgroupParent)
	podBvt := r.getPodBvtValue(podQOS, podKubeQOS)

	// pod annotatations take precedence to the default CPUQoS which retrieve from getPodBvtValue
	cfg, err := slov1alpha1.GetPodCPUQoSConfigByAttr(req.Labels, req.Annotations)
	if err != nil {
		return err
	}
	// we can change group identity by pod annotations only when QoS=LS
	// because LS=2 and BE=-1 by default
	// we only allow LS change to BE
	// and not allow BE change to LS
	// and do not change LSR/LSE
	if cfg != nil && podQOS == ext.QoSLS {
		if cfg.GroupIdentity != nil {
			podBvt = *cfg.GroupIdentity
		}

		// if disabled, set bvt to zero
		if cfg.Enable == nil || (cfg.Enable != nil && !(*cfg.Enable)) {
			podBvt = 0
		}
	}
	podCtx.Response.Resources.CPUBvt = pointer.Int64(podBvt)
	return nil
}

func (b *bvtPlugin) SetKubeQOSBvtValue(p protocol.HooksProtocol) error {
	r := b.prepare()
	if r == nil {
		return nil
	}
	kubeQOSCtx := p.(*protocol.KubeQOSContext)
	req := kubeQOSCtx.Request
	bvtValue := r.getKubeQOSDirBvtValue(req.KubeQOSClass)
	kubeQOSCtx.Response.Resources.CPUBvt = pointer.Int64(bvtValue)
	return nil
}

func (b *bvtPlugin) SetHostAppBvtValue(p protocol.HooksProtocol) error {
	r := b.prepare()
	if r == nil {
		return nil
	}
	hostQOSCtx := p.(*protocol.HostAppContext)
	req := hostQOSCtx.Request
	bvtValue := r.getHostQOSBvtValue(req.QOSClass)
	hostQOSCtx.Response.Resources.CPUBvt = pointer.Int64(bvtValue)
	return nil
}

func (b *bvtPlugin) prepare() *bvtRule {
	if !b.SystemSupported() {
		klog.V(5).Infof("plugin %s is not supported by system", name)
		return nil
	}
	r := b.getRule()
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}

	isSysctlEnabled, err := b.isSysctlEnabled()
	if err != nil {
		klog.V(4).Infof("failed to check sysctl for plugin %s, err: %s", name, err)
		return nil
	}
	if !r.getEnable() && !isSysctlEnabled { // no need to update cgroups if both rule and sysctl disabled
		klog.V(5).Infof("rule is disabled for plugin %v, no more to do for resources", name)
		return nil
	}

	return r
}
