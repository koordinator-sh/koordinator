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
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

const (
	name        = "GroupIdentity"
	description = "set bvt value by priority and qos class"
)

type bvtPlugin struct {
	rule        *bvtRule
	ruleRWMutex sync.RWMutex

	sysSupported             *bool
	hasKernelEnabled         *bool // whether kernel is configurable for enabling bvt (via `kernel.sched_group_identity_enabled`)
	coreSchedSysctlSupported *bool // whether core sched is supported by the sysctl

	executor resourceexecutor.ResourceUpdateExecutor
}

func (b *bvtPlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", name)
	hooks.Register(rmconfig.PreRunPodSandbox, name, description, b.SetPodBvtValue)
	rule.Register(name, description,
		rule.WithParseFunc(statesinformer.RegisterTypeNodeSLOSpec, b.parseRule),
		rule.WithUpdateCallback(b.ruleUpdateCb),
		rule.WithSystemSupported(b.SystemSupported))
	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, sysutil.CPUBVTWarpNs, "reconcile pod level cpu bvt value",
		b.SetPodBvtValue, reconciler.NoneFilter())
	reconciler.RegisterCgroupReconciler(reconciler.KubeQOSLevel, sysutil.CPUBVTWarpNs, "reconcile kubeqos level cpu bvt value",
		b.SetKubeQOSBvtValue, reconciler.NoneFilter())
	reconciler.RegisterHostAppReconciler(sysutil.CPUBVTWarpNs, "reconcile host application cpu bvt value",
		b.SetHostAppBvtValue, &reconciler.ReconcilerOption{})
	b.executor = op.Executor
}

func (b *bvtPlugin) SystemSupported() bool {
	if b.sysSupported == nil {
		isBVTSupported, msg := false, "resource not found"
		bvtResource, err := sysutil.GetCgroupResource(sysutil.CPUBVTWarpNsName)
		if err == nil {
			isBVTSupported, msg = bvtResource.IsSupported(util.GetPodQoSRelativePath(corev1.PodQOSGuaranteed))
		}
		sysSupported := isBVTSupported || sysutil.IsGroupIdentitySysctlSupported()
		b.sysSupported = pointer.Bool(sysSupported)
		klog.Infof("update system supported info to %v for plugin %v, supported msg %s",
			*b.sysSupported, name, msg)
	}
	return *b.sysSupported
}

func (b *bvtPlugin) hasKernelEnable() bool {
	if b.hasKernelEnabled == nil {
		b.hasKernelEnabled = pointer.Bool(sysutil.IsGroupIdentitySysctlSupported())
	}
	return *b.hasKernelEnabled
}

// tryDisableCoreSched tries disabling the core scheduling via sysctl to safely enable the group identity.
func (b *bvtPlugin) tryDisableCoreSched() error {
	if b.coreSchedSysctlSupported == nil {
		b.coreSchedSysctlSupported = pointer.Bool(sysutil.IsCoreSchedSysctlSupported())
	}
	if !*b.coreSchedSysctlSupported {
		return nil
	}

	return sysutil.SetSchedCore(false)
}

// initSysctl initializes the sysctl for the group identity.
// It returns whether cgroups need to set, e.g. if the sysctl config is not disabled.
func (b *bvtPlugin) initSysctl() error {
	if !b.hasKernelEnable() { // kernel does not support bvt sysctl
		return nil
	}

	// NOTE: Currently the kernel feature core scheduling is strictly excluded with the group identity's
	//       bvt=-1. So we have to check if the CoreSched can be disabled before enabling group identity.
	err := b.tryDisableCoreSched()
	if err != nil {
		return err
	}

	// try to set bvt kernel enabled via sysctl when the sysctl config is disabled or unknown
	// https://github.com/koordinator-sh/koordinator/pull/1172
	err = sysutil.SetSchedGroupIdentity(true)
	if err != nil {
		return fmt.Errorf("cannot enable kernel sysctl for bvt, err: %w", err)
	}

	return nil
}

// isSysctlEnabled checks if the sysctl configuration for the bvt (group identity) is enabled.
// It returns whether cgroups need to set, e.g. if the sysctl config is not disabled.
func (b *bvtPlugin) isSysctlEnabled() (bool, error) {
	// NOTE: bvt (group identity) is supported and can be initialized in the system if:
	// 1. anolis os kernel (<26.4): cgroup cpu.bvt_warp_ns exists but sysctl kernel.sched_group_identity_enabled no exist,
	//    the bvt feature is enabled by default, no need to set sysctl.
	// 2. anolis os kernel (>=26.4): both cgroup cpu.bvt_warp_ns and sysctl kernel.sched_group_identity_enabled exist,
	//    the bvt feature is enabled when kernel.sched_group_identity_enabled is set as `1`.
	if !b.hasKernelEnable() { // consider enabled when kernel does not support bvt sysctl
		return true, nil
	}

	isSysEnabled, err := sysutil.GetSchedGroupIdentity()
	if err != nil {
		return true, fmt.Errorf("cannot get sysctl group identity, err: %v", err)
	}
	return isSysEnabled, nil
}

var singleton *bvtPlugin

func Object() *bvtPlugin {
	if singleton == nil {
		singleton = &bvtPlugin{rule: &bvtRule{}}
	}
	return singleton
}
