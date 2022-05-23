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
	"strconv"

	"k8s.io/klog/v2"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	runtimeapi "github.com/koordinator-sh/koordinator/apis/runtime/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/util/system"
)

func (b *bvtPlugin) PreRunPodSandbox(requestIf, responseIf interface{}) error {
	if !b.SystemSupported() {
		klog.V(5).Infof("plugin %s is not supported by system", name)
		return nil
	}
	r := b.getRule()
	req := requestIf.(*runtimeapi.RunPodSandboxHookRequest)
	podQoS := ext.GetQoSClassByLabels(req.Labels)
	podKubeQoS := util.GetKubeQoSByCgroupParent(req.CgroupParent)
	podBvt := r.getPodBvtValue(podQoS, podKubeQoS)
	// CgroupParent e.g. kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod586c1b35_63de_4ee0_9da3_2cebdca672c8.slice
	klog.V(5).Infof("set pod bvt on cgroup parent %v", req.CgroupParent)
	if req.PodMeta != nil {
		audit.V(2).Pod(req.PodMeta.Namespace, req.PodMeta.Name).Reason(name).Message("set bvt to %v", podBvt).Do()
	}
	return sysutil.CgroupFileWrite(req.CgroupParent, sysutil.CPUBVTWarpNs, strconv.FormatInt(podBvt, 10))
}
