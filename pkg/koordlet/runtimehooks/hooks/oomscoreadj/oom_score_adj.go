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

package oomscoreadj

import (
	"fmt"

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	name        = "OOMScoreAdj"
	description = "manage oom_score_adj for pod containers via annotation"

	ruleNameForAllPods = name + " (allPods)"
)

// Plugin manages oom_score_adj for containers based on pod annotations.
type Plugin struct {
	rule *Rule

	reader   resourceexecutor.CgroupReader
	executor resourceexecutor.ResourceUpdateExecutor
	ome      sysutil.OOMScoreAdjInterface
}

var singleton *Plugin

// Object returns the singleton Plugin instance.
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

// Register registers the OOMScoreAdj reconcilers and rule callbacks.
func (p *Plugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", name)

	rule.Register(ruleNameForAllPods, description,
		rule.WithParseFunc(statesinformer.RegisterTypeAllPods, p.parseForAllPods),
		rule.WithUpdateCallback(p.ruleUpdateCb))

	// Use NoneFilter so that all pods (including SYSTEM QoS) are reconciled.
	// NOTE: the sandbox (pause) container is deliberately NOT reconciled, since its oom_score_adj is
	// managed by the runtime (e.g. -998) and overriding it may cause the sandbox to be OOM killed.
	reconciler.RegisterCgroupReconciler(reconciler.ContainerLevel, sysutil.VirtualOOMScoreAdj,
		"set oom_score_adj to processes of container specified",
		p.SetContainerOOMScoreAdj, reconciler.NoneFilter())

	p.Setup(op)
}

// Setup initializes the reader, executor and oom_score_adj operator from hook options.
func (p *Plugin) Setup(op hooks.Options) {
	p.reader = op.Reader
	p.executor = op.Executor
	p.ome = sysutil.NewOOMScoreAdj()
}

// SetContainerOOMScoreAdj reconciles oom_score_adj for a single container.
func (p *Plugin) SetContainerOOMScoreAdj(proto protocol.HooksProtocol) error {
	containerCtx := proto.(*protocol.ContainerContext)
	if containerCtx == nil {
		return fmt.Errorf("container protocol is nil for plugin %s", name)
	}
	if !util.IsValidContainerCgroupDir(containerCtx.Request.CgroupParent) {
		return fmt.Errorf("invalid container cgroup parent %s for plugin %s", containerCtx.Request.CgroupParent, name)
	}

	podUID := containerCtx.Request.PodMeta.UID
	if len(podUID) == 0 || len(containerCtx.Request.ContainerMeta.ID) == 0 {
		klog.V(5).Infof("aborted to set oom_score_adj for container %s/%s, empty pod UID %s or container ID %s",
			containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name,
			podUID, containerCtx.Request.ContainerMeta.ID)
		return nil
	}
	// Skip the sandbox (pause) container whose oom_score_adj is managed by the runtime.
	if containerCtx.Request.ContainerMeta.Sandbox {
		return nil
	}

	targetVal, err := getOOMScoreAdjForContainer(containerCtx.Request.PodAnnotations, containerCtx.Request.ContainerMeta.Name)
	if err != nil {
		klog.V(4).Infof("failed to parse oom_score_adj for container %s/%s: %s",
			containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name, err)
		return nil
	}
	if targetVal == nil {
		// No annotation set for this container; nothing to do.
		return nil
	}

	// Validate range before proceeding.
	if *targetVal < sysutil.OOMScoreAdjMin || *targetVal > sysutil.OOMScoreAdjMax {
		klog.Warningf("oom_score_adj value %d out of range [%d, %d] for container %s/%s, skipping",
			*targetVal, sysutil.OOMScoreAdjMin, sysutil.OOMScoreAdjMax,
			containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name)
		return nil
	}

	pids, err := getContainerPIDs(p.reader, containerCtx.Request.CgroupParent)
	if err != nil {
		klog.V(5).Infof("failed to get PIDs for container %s/%s: %s",
			containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name, err)
		return nil
	}
	if len(pids) == 0 {
		klog.V(5).Infof("no PID found for container %s/%s",
			containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name)
		return nil
	}

	updated, skipped := setOOMScoreAdjForPIDs(p.ome, pids, *targetVal)
	if updated > 0 {
		klog.V(4).Infof("set oom_score_adj=%d for container %s/%s, updated %d PIDs, skipped %d",
			*targetVal, containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name, updated, skipped)
	} else {
		klog.V(6).Infof("no PID updated for container %s/%s oom_score_adj=%d (all already match or skipped %d)",
			containerCtx.Request.PodMeta.String(), containerCtx.Request.ContainerMeta.Name, *targetVal, skipped)
	}
	// Record the metric on every reconciliation (including the already-matched case) to keep the
	// GC gauge alive, since the GCGaugeVec expires series which are not refreshed periodically.
	if skipped < len(pids) { // at least one PID holds the target value
		metrics.RecordContainerOOMScoreAdj(
			containerCtx.Request.PodMeta.Namespace,
			containerCtx.Request.PodMeta.Name,
			podUID,
			containerCtx.Request.ContainerMeta.Name,
			*targetVal)
	}

	return nil
}
