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

package resctrl

import (
	"fmt"
	"strings"

	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	util "github.com/koordinator-sh/koordinator/pkg/koordlet/util/resctrl"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

const (
	name               = "Resctrl"
	description        = "set resctrl for pod"
	ruleNameForAllPods = name + " (AllPods)"
)

type plugin struct {
	rule           *Rule
	engine         util.ResctrlEngine
	executor       resourceexecutor.ResourceUpdateExecutor
	statesInformer statesinformer.StatesInformer
	EventRecorder  record.EventRecorder
}

var singleton *plugin

func Object() *plugin {
	if singleton == nil {
		singleton = newPlugin()
	}
	return singleton
}

func newPlugin() *plugin {
	return &plugin{
		rule: newRule(),
	}
}

func (p *plugin) Register(op hooks.Options) {
	// skip if host not support resctrl
	if support, err := system.IsSupportResctrl(); err != nil {
		klog.Warningf("check support resctrl failed, err: %s", err)
		return
	} else if !support {
		klog.V(4).Infof("resctrl runtime hook skipped, cpu not support CAT/MBA")
		return
	}

	if vendorID, err := sysutil.GetVendorIDByCPUInfo(sysutil.GetCPUInfoPath()); err == nil {
		// check if the resctrl root and l3_cat feature are enabled correctly
		if err := system.CheckAndTryEnableResctrlCat(); err != nil {
			klog.Errorf("check resctrl cat failed, err: %s", err)
			return
		}

		p.engine, err = util.NewRDTEngine(vendorID)
		if err != nil {
			klog.Errorf("New RDT Engine failed, error is %v", err)
			return
		}
	}
	p.executor = op.Executor
	p.statesInformer = op.StatesInformer
	p.EventRecorder = op.EventRecorder
	p.engine.Rebuild()

	rule.Register(ruleNameForAllPods, description,
		rule.WithParseFunc(statesinformer.RegisterTypeAllPods, p.parseRuleForAllPods),
		rule.WithUpdateCallback(p.ruleUpdateCbForAllPods))

	hooks.Register(rmconfig.PreRunPodSandbox, name, description+" (pod)", p.SetPodResctrlResourcesForHooks)
	hooks.Register(rmconfig.PreCreateContainer, name, description+" (pod)", p.SetContainerResctrlResources)
	hooks.Register(rmconfig.PreRemoveRunPodSandbox, name, description+" (pod)", p.RemovePodResctrlResources)

	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, system.ResctrlSchemata, description+" (pod resctrl schema)", p.SetPodResctrlResourcesForReconciler, reconciler.NoneFilter())
	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, system.ResctrlTasks, description+" (pod resctrl tasks)", p.UpdatePodTaskIds, reconciler.NoneFilter())
	reconciler.RegisterCgroupReconciler4AllPods(reconciler.AllPodsLevel, system.ResctrlRoot, description+" (pod resctl schema)", p.RemoveUnusedResctrlPath, reconciler.PodAnnotationResctrlFilter(), "resctrl")
}

func (p *plugin) SetPodResctrlResourcesForHooks(proto protocol.HooksProtocol) error {
	return p.setPodResctrlResources(proto, true)
}

func (p *plugin) SetPodResctrlResourcesForReconciler(proto protocol.HooksProtocol) error {
	return p.setPodResctrlResources(proto, false)
}

func (p *plugin) setPodResctrlResources(proto protocol.HooksProtocol, fromNRI bool) error {
	podCtx, ok := proto.(*protocol.PodContext)
	if !ok {
		return fmt.Errorf("pod protocol is nil for plugin %v", name)
	}

	if v, ok := podCtx.Request.Annotations[apiext.AnnotationResctrl]; ok {
		app, ok := p.engine.GetApp(podCtx.Request.PodMeta.UID)
		if ok && app.Annotation == v {
			return nil
		}
		updater := NewCreateResctrlProtocolUpdater(proto)
		err := p.engine.RegisterApp(podCtx.Request.PodMeta.UID, v, fromNRI, updater)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *plugin) RemoveUnusedResctrlPath(protos []protocol.HooksProtocol) error {
	currentPods := make(map[string]protocol.HooksProtocol)

	for _, proto := range protos {
		podCtx, ok := proto.(*protocol.PodContext)
		if !ok {
			return fmt.Errorf("pod protocol is nil for plugin %v", name)
		}

		if _, ok := podCtx.Request.Annotations[apiext.AnnotationResctrl]; ok {
			group := podCtx.Request.PodMeta.UID
			currentPods[group] = podCtx
		}
	}

	apps := p.engine.GetApps()
	for k, v := range apps {
		if _, ok := currentPods[k]; !ok {
			updater := NewRemoveResctrlUpdater(v.Closid)
			p.engine.UnRegisterApp(strings.TrimPrefix(v.Closid, util.ClosdIdPrefix), false, updater)
		}
	}
	return nil
}

func (p *plugin) UpdatePodTaskIds(proto protocol.HooksProtocol) error {
	podCtx, ok := proto.(*protocol.PodContext)
	if !ok {
		return fmt.Errorf("pod protocol is nil for plugin %v", name)
	}

	if _, ok := podCtx.Request.Annotations[apiext.AnnotationResctrl]; ok {
		curTaskMaps := map[string]map[int32]struct{}{}
		var err error
		group := podCtx.Request.PodMeta.UID
		curTaskMaps[group], err = system.ReadResctrlTasksMap(util.ClosdIdPrefix + group)
		if err != nil {
			klog.Warningf("failed to read Cat L3 tasks for resctrl group %s, err: %s", group, err)
		}

		newTaskIds := util.GetPodCgroupNewTaskIdsFromPodCtx(podCtx, curTaskMaps[group])
		resctrlInfo := &protocol.Resctrl{
			Closid:     util.ClosdIdPrefix + group,
			NewTaskIds: make([]int32, 0),
		}
		resctrlInfo.NewTaskIds = newTaskIds
		podCtx.Response.Resources.Resctrl = resctrlInfo
	}
	return nil
}

func (p *plugin) SetContainerResctrlResources(proto protocol.HooksProtocol) error {
	containerCtx, ok := proto.(*protocol.ContainerContext)
	if !ok {
		return fmt.Errorf("container protocol is nil for plugin %v", name)
	}

	if _, ok := containerCtx.Request.PodAnnotations[apiext.AnnotationResctrl]; ok {
		containerCtx.Response.Resources.Resctrl = &protocol.Resctrl{
			Schemata:   "",
			Closid:     util.ClosdIdPrefix + containerCtx.Request.PodMeta.UID,
			NewTaskIds: make([]int32, 0),
		}
	}

	return nil
}

func (p *plugin) RemovePodResctrlResources(proto protocol.HooksProtocol) error {
	podCtx, ok := proto.(*protocol.PodContext)
	if !ok {
		return fmt.Errorf("pod protocol is nil for plugin %v", name)
	}

	if _, ok := podCtx.Request.Annotations[apiext.AnnotationResctrl]; ok {
		updater := NewRemoveResctrlProtocolUpdater(proto)
		p.engine.UnRegisterApp(podCtx.Request.PodMeta.UID, true, updater)
	}
	return nil
}
