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
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	util "github.com/koordinator-sh/koordinator/pkg/koordlet/util/resctrl"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

const (
	name        = "Resctrl"
	description = "set resctrl for pod"
)

// TODO:@Bowen choose parser there or in engine, should we init with some parameters?
type plugin struct {
	engine         util.ResctrlEngine
	executor       resourceexecutor.ResourceUpdateExecutor
	statesInformer statesinformer.StatesInformer
	app            map[string]util.App
}

var singleton *plugin

func Object() *plugin {
	if singleton == nil {
		singleton = newPlugin()
	}
	return singleton
}

func newPlugin() *plugin {
	return &plugin{}
}

func (p *plugin) init(apps map[string]util.App) {
	podsMeta := p.statesInformer.GetAllPods()
	currentPods := make(map[string]*corev1.Pod)
	for _, podMeta := range podsMeta {
		pod := podMeta.Pod
		if _, ok := podMeta.Pod.Annotations[apiext.AnnotationResctrl]; ok {
			group := string(podMeta.Pod.UID)
			currentPods[group] = pod
		}
	}

	for k, v := range apps {
		if _, ok := currentPods[k]; !ok {
			if err := os.Remove(system.GetResctrlGroupRootDirPath(v.Closid)); err != nil {
				klog.Errorf("cannot remove ctrl group, err: %w", err)
				if os.IsNotExist(err) {
					p.engine.UnRegisterApp(strings.TrimPrefix(v.Closid, util.ClosdIdPrefix))
				}
			} else {
				p.engine.UnRegisterApp(strings.TrimPrefix(v.Closid, util.ClosdIdPrefix))
			}
		}
	}
}

func (p *plugin) Register(op hooks.Options) {
	// skip if host not support resctrl
	if support, err := system.IsSupportResctrl(); err != nil {
		klog.Warningf("check support resctrl failed, err: %s", err)
		return
	} else if !support {
		klog.V(5).Infof("resctrl runtime hook skipped, cpu not support CAT/MBA")
		return
	}

	if vendorID, err := sysutil.GetVendorIDByCPUInfo(sysutil.GetCPUInfoPath()); err == nil && vendorID == sysutil.INTEL_VENDOR_ID {
		p.engine, err = util.NewRDTEngine()
		if err != nil {
			klog.Errorf("New RDT Engine failed, error is %v", err)
			return
		}
	} else {
		//TODO: add AMD resctrl engine
		klog.Warningf("AMD resctrl engine not implemented")
		return
	}

	hooks.Register(rmconfig.PreRunPodSandbox, name, description+" (pod)", p.SetPodResctrlResources)
	hooks.Register(rmconfig.PreCreateContainer, name, description+" (pod)", p.SetContainerResctrlResources)
	hooks.Register(rmconfig.PreRemoveRunPodSandbox, name, description+" (pod)", p.RemovePodResctrlResources)
	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, system.ResctrlSchemata, description+" (pod resctrl schema)", p.SetPodResctrlResources, reconciler.NoneFilter())
	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, system.ResctrlRoot, description+" (pod resctrl schema)", p.RemovePodResctrlResources, reconciler.NoneFilter())
	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, system.ResctrlTasks, description+" (pod resctrl tasks)", p.UpdatePodTaskIds, reconciler.NoneFilter())
	reconciler.RegisterCgroupReconciler4AllPods(reconciler.AllPodsLevel, system.ResctrlRoot, description+" (pod resctl schema)", p.RemoveUnusedResctrlPath, reconciler.PodAnnotationResctrlFilter(), "resctrl")

	p.engine.Rebuild()
	apps := p.engine.GetApps()
	p.executor = op.Executor
	p.statesInformer = op.StatesInformer
	p.init(apps)
}

func (p *plugin) SetPodResctrlResources(proto protocol.HooksProtocol) error {
	podCtx, ok := proto.(*protocol.PodContext)
	if !ok {
		return fmt.Errorf("pod protocol is nil for plugin %v", name)
	}

	if v, ok := podCtx.Request.Annotations[apiext.AnnotationResctrl]; ok {
		resctrlInfo := &protocol.Resctrl{
			NewTaskIds: make([]int32, 0),
		}
		err := p.engine.RegisterApp(podCtx.Request.PodMeta.UID, v)
		if err != nil {
			return err
		}

		app, err := p.engine.GetApp(podCtx.Request.PodMeta.UID)
		if err != nil {
			return err
		}
		items := []string{}
		for _, item := range []struct {
			validFunc func() (bool, string)
			value     func() string
		}{
			{validFunc: app.Resctrl.ValidateL3, value: app.Resctrl.L3String},
			{validFunc: app.Resctrl.ValidateMB, value: app.Resctrl.MBString},
		} {
			if valid, _ := item.validFunc(); valid {
				items = append(items, item.value())
			}
		}
		schemataStr := strings.Join(items, "")
		resctrlInfo.Schemata = schemataStr
		resctrlInfo.Closid = app.Closid
		podCtx.Response.Resources.Resctrl = resctrlInfo
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
			if err := os.Remove(system.GetResctrlGroupRootDirPath(v.Closid)); err != nil {
				klog.Errorf("cannot remove ctrl group, err: %v", err)
				if os.IsNotExist(err) {
					p.engine.UnRegisterApp(strings.TrimPrefix(v.Closid, util.ClosdIdPrefix))
				}
			} else {
				p.engine.UnRegisterApp(strings.TrimPrefix(v.Closid, util.ClosdIdPrefix))
			}
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
			Hook:       "",
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
		resctrlInfo := &protocol.Resctrl{
			NewTaskIds: make([]int32, 0),
		}

		resctrlInfo.Closid = util.ClosdIdPrefix + podCtx.Request.PodMeta.UID
		podCtx.Response.Resources.Resctrl = resctrlInfo
		p.engine.UnRegisterApp(podCtx.Request.PodMeta.UID)
	}
	return nil
}
