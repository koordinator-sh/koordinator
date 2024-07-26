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

	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	util "github.com/koordinator-sh/koordinator/pkg/koordlet/util/resctrl"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type UpdateFunc func(resource util.ResctrlUpdater) error

type DefaultResctrlProtocolUpdater struct {
	hooksProtocol protocol.HooksProtocol
	group         string
	schemata      string
	updateFunc    UpdateFunc
}

func (u DefaultResctrlProtocolUpdater) Name() string {
	return "default"
}

func (u DefaultResctrlProtocolUpdater) Key() string {
	return u.group
}

func (u DefaultResctrlProtocolUpdater) Value() string {
	return u.schemata
}

func (r *DefaultResctrlProtocolUpdater) SetKey(key string) {
	r.group = key
}

func (r *DefaultResctrlProtocolUpdater) SetValue(val string) {
	r.schemata = val
}

func (u *DefaultResctrlProtocolUpdater) Update() error {
	return u.updateFunc(u)
}

type Updater func(u DefaultResctrlProtocolUpdater) error

func NewCreateResctrlProtocolUpdater(hooksProtocol protocol.HooksProtocol) util.ResctrlUpdater {
	return &DefaultResctrlProtocolUpdater{
		hooksProtocol: hooksProtocol,
		updateFunc:    CreateResctrlProtocolUpdaterFunc,
	}
}

func NewRemoveResctrlProtocolUpdater(hooksProtocol protocol.HooksProtocol) util.ResctrlUpdater {
	return &DefaultResctrlProtocolUpdater{
		hooksProtocol: hooksProtocol,
		updateFunc:    RemoveResctrlProtocolUpdaterFunc,
	}
}

func NewRemoveResctrlUpdater(group string) util.ResctrlUpdater {
	return &DefaultResctrlProtocolUpdater{
		group:      group,
		updateFunc: RemoveResctrlUpdaterFunc,
	}
}

func CreateResctrlProtocolUpdaterFunc(u util.ResctrlUpdater) error {
	r, ok := u.(*DefaultResctrlProtocolUpdater)
	if !ok {
		return fmt.Errorf("not a ResctrlSchemataResourceUpdater")
	}

	podCtx, ok := r.hooksProtocol.(*protocol.PodContext)
	if !ok {
		return fmt.Errorf("pod protocol is nil for plugin %v", name)
	}
	if podCtx.Response.Resources.Resctrl != nil {
		podCtx.Response.Resources.Resctrl.Schemata = r.Value()
		podCtx.Response.Resources.Resctrl.Closid = r.Key()
	} else {
		resctrlInfo := &protocol.Resctrl{
			NewTaskIds: make([]int32, 0),
		}
		resctrlInfo.Schemata = r.Value()
		resctrlInfo.Closid = r.Key()
		podCtx.Response.Resources.Resctrl = resctrlInfo
	}
	return nil
}

func RemoveResctrlProtocolUpdaterFunc(u util.ResctrlUpdater) error {
	r, ok := u.(*DefaultResctrlProtocolUpdater)
	if !ok {
		return fmt.Errorf("not a ResctrlSchemataResourceUpdater")
	}
	resctrlInfo := &protocol.Resctrl{
		NewTaskIds: make([]int32, 0),
	}
	podCtx, ok := r.hooksProtocol.(*protocol.PodContext)
	if !ok {
		return fmt.Errorf("pod protocol is nil for plugin %v", name)
	}
	resctrlInfo.Closid = util.ClosdIdPrefix + podCtx.Request.PodMeta.UID
	podCtx.Response.Resources.Resctrl = resctrlInfo
	return nil
}

func RemoveResctrlUpdaterFunc(u util.ResctrlUpdater) error {
	r, ok := u.(*DefaultResctrlProtocolUpdater)
	if !ok {
		return fmt.Errorf("not a ResctrlSchemataResourceUpdater")
	}
	if err := os.Remove(system.GetResctrlGroupRootDirPath(r.group)); err != nil {
		return err
	} else {
		klog.V(5).Infof("successfully remove ctrl group %s", r.group)
	}
	return nil
}
