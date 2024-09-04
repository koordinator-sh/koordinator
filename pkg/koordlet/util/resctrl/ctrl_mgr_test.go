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
	"path/filepath"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

type UpdateFunc func(resource ResctrlUpdater) error

type DefaultTestResctrlProtocolUpdater struct {
	name          string
	hooksProtocol protocol.HooksProtocol
	group         string
	schemata      string
	updateFunc    UpdateFunc
}

func (u DefaultTestResctrlProtocolUpdater) Name() string {
	return u.name
}

func (u DefaultTestResctrlProtocolUpdater) Key() string {
	return u.group
}

func (u DefaultTestResctrlProtocolUpdater) Value() string {
	return u.schemata
}

func (r *DefaultTestResctrlProtocolUpdater) SetKey(group string) {
	r.group = group
}

func (r *DefaultTestResctrlProtocolUpdater) SetValue(schemata string) {
	r.schemata = schemata
}

func (u *DefaultTestResctrlProtocolUpdater) Update() error {
	return u.updateFunc(u)
}

func NewTestCreateResctrlUpdater(podid string) ResctrlUpdater {
	return &DefaultTestResctrlProtocolUpdater{
		name:       podid,
		updateFunc: createResctrlProtocolUpdaterFunc,
	}
}

func NewTestUpdateResctrlUpdater(podid string, schemata string) ResctrlUpdater {
	return &DefaultTestResctrlProtocolUpdater{
		name:       podid,
		schemata:   schemata,
		updateFunc: updateResctrlProtocolUpdaterFunc,
	}
}

func NewTestRemoveResctrlUpdater(group string) ResctrlUpdater {
	return &DefaultTestResctrlProtocolUpdater{
		group:      group,
		updateFunc: removeResctrlUpdaterFunc,
	}
}

func createResctrlProtocolUpdaterFunc(u ResctrlUpdater) error {
	r, ok := u.(*DefaultTestResctrlProtocolUpdater)
	if !ok {
		return fmt.Errorf("not a ResctrlSchemataResourceUpdater")
	}
	r.SetKey(ClosdIdPrefix + r.name)
	return nil
}

func updateResctrlProtocolUpdaterFunc(u ResctrlUpdater) error {
	r, ok := u.(*DefaultTestResctrlProtocolUpdater)
	if !ok {
		return fmt.Errorf("not a ResctrlSchemataResourceUpdater")
	}
	r.SetValue(r.schemata)
	return nil
}

func removeResctrlUpdaterFunc(u ResctrlUpdater) error {
	r, ok := u.(*DefaultTestResctrlProtocolUpdater)
	if !ok {
		return fmt.Errorf("not a ResctrlSchemataResourceUpdater")
	}
	r.SetKey("")
	r.SetValue("")
	return nil
}

func TestControlGroupManager_AddPod(t *testing.T) {
	type fields struct {
		rdtcgs            *gocache.Cache
		reconcileInterval int64
		groupExist        bool
	}
	type args struct {
		podid           string
		schemata        string
		fromNRI         bool
		createUpdater   ResctrlUpdater
		schemataUpdater ResctrlUpdater
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "AddPod from NRI while ctrl group is not exist",
			fields: fields{
				rdtcgs:            gocache.New(time.Duration(ExpirationTime), CleanupInterval),
				reconcileInterval: 0,
				groupExist:        false,
			},
			args: args{
				podid:           "pod1",
				schemata:        "",
				fromNRI:         true,
				createUpdater:   NewTestCreateResctrlUpdater("pod1"),
				schemataUpdater: NewTestUpdateResctrlUpdater("pod1", "testschemata"),
			},
		},
		{
			name: "AddPod from NRI while ctrl group exist",
			fields: fields{
				rdtcgs:            gocache.New(time.Duration(ExpirationTime), CleanupInterval),
				reconcileInterval: 0,
				groupExist:        true,
			},
			args: args{
				podid:           "pod2",
				schemata:        "testschemata",
				fromNRI:         true,
				createUpdater:   NewTestCreateResctrlUpdater("pod2"),
				schemataUpdater: NewTestUpdateResctrlUpdater("pod2", "testschemata"),
			},
		},
		{
			name: "AddPod from reconciler while ctrl group is not exist",
			fields: fields{
				rdtcgs:            gocache.New(time.Duration(ExpirationTime), CleanupInterval),
				reconcileInterval: 0,
				groupExist:        false,
			},
			args: args{
				podid:           "pod3",
				schemata:        "",
				fromNRI:         false,
				createUpdater:   NewTestCreateResctrlUpdater("pod3"),
				schemataUpdater: NewTestUpdateResctrlUpdater("pod3", "testschemata"),
			},
		},
		{
			name: "AddPod from NRI while ctrl group exist",
			fields: fields{
				rdtcgs:            gocache.New(time.Duration(ExpirationTime), CleanupInterval),
				reconcileInterval: 0,
				groupExist:        true,
			},
			args: args{
				podid:           "pod4",
				schemata:        "testschemata",
				fromNRI:         false,
				createUpdater:   NewTestCreateResctrlUpdater("pod4"),
				schemataUpdater: NewTestUpdateResctrlUpdater("pod4", "testschemata"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ControlGroupManager{
				rdtcgs:            tt.fields.rdtcgs,
				reconcileInterval: tt.fields.reconcileInterval,
			}
			// prepare ctrl group if group exist
			if tt.fields.groupExist {
				c.rdtcgs.Set(tt.args.podid, &ControlGroup{
					AppId:       tt.args.podid,
					GroupId:     ClosdIdPrefix + tt.args.podid,
					Schemata:    tt.args.schemata,
					Status:      Add,
					CreatedTime: 0,
				}, -1)
			}
			c.AddPod(tt.args.podid, tt.args.schemata, tt.args.fromNRI, tt.args.createUpdater, tt.args.schemataUpdater)
			p, ok := c.rdtcgs.Get(tt.args.podid)
			assert.Equal(t, true, ok)
			pod := p.(*ControlGroup)
			assert.Equal(t, ClosdIdPrefix+tt.args.podid, pod.GroupId)
			assert.Equal(t, tt.args.schemata, pod.Schemata)
		})
	}
}

func testingPrepareResctrlL3CatPath(t *testing.T, cbmStr, rootSchemataStr string) {
	resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)
	l3CatDir := filepath.Join(resctrlDir, system.RdtInfoDir, system.L3CatDir)
	err := os.MkdirAll(l3CatDir, 0700)
	assert.NoError(t, err)

	cbmPath := filepath.Join(l3CatDir, system.ResctrlCbmMaskName)
	err = os.WriteFile(cbmPath, []byte(cbmStr), 0666)
	assert.NoError(t, err)

	schemataPath := filepath.Join(resctrlDir, system.ResctrlSchemataName)
	err = os.WriteFile(schemataPath, []byte(rootSchemataStr), 0666)
	assert.NoError(t, err)
}

// @schemataData: schemata for pod1, pod2
func testingPrepareResctrlL3CatGroups(t *testing.T, cbmStr, rootSchemataStr string, schemataData ...string) {
	testingPrepareResctrlL3CatPath(t, cbmStr, rootSchemataStr)
	resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)

	pod1SchemataData := []byte("    L3:0=f;1=f\n    MB:0=100;1=100")
	if len(schemataData) >= 1 {
		pod1SchemataData = []byte(schemataData[0])
	}
	pod1SchemataDir := filepath.Join(resctrlDir, "koordlet-pod1")
	err := os.MkdirAll(pod1SchemataDir, 0700)
	assert.NoError(t, err)
	pod1SchemataPath := filepath.Join(pod1SchemataDir, system.ResctrlSchemataName)
	err = os.WriteFile(pod1SchemataPath, pod1SchemataData, 0666)
	assert.NoError(t, err)
	pod1TasksPath := filepath.Join(pod1SchemataDir, system.ResctrlTasksName)
	err = os.WriteFile(pod1TasksPath, []byte{}, 0666)
	assert.NoError(t, err)

	pod2SchemataData := []byte("    L3:0=f;1=f\n    MB:0=100;1=100")
	if len(schemataData) >= 1 {
		pod2SchemataData = []byte(schemataData[1])
	}
	pod2SchemataDir := filepath.Join(resctrlDir, "koordlet-pod2")
	err = os.MkdirAll(pod2SchemataDir, 0700)
	assert.NoError(t, err)
	pod2SchemataPath := filepath.Join(pod2SchemataDir, system.ResctrlSchemataName)
	err = os.WriteFile(pod2SchemataPath, pod2SchemataData, 0666)
	assert.NoError(t, err)
	pod2TasksPath := filepath.Join(pod2SchemataDir, system.ResctrlTasksName)
	err = os.WriteFile(pod2TasksPath, []byte{}, 0666)
	assert.NoError(t, err)
}

func TestControlGroupManager_Init(t *testing.T) {
	type fields struct {
		rdtcgs            *gocache.Cache
		reconcileInterval int64
		schemataData      []string
		mockSchemata      string
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "rdt cgroup Init",
			fields: fields{
				rdtcgs:            gocache.New(time.Duration(ExpirationTime), CleanupInterval),
				reconcileInterval: 0,
				schemataData:      []string{"L3:0=f0\nMB:0=100", "L3:0=fc\nMB:0=100"},
				mockSchemata:      "L3:0=ff\nMB:0=100\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()

			sysFSRootDirName := "ctrlmgr"
			helper.MkDirAll(sysFSRootDirName)
			system.Conf.SysFSRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)
			testingPrepareResctrlL3CatGroups(t, "ff", tt.fields.mockSchemata, tt.fields.schemataData...)
			c := &ControlGroupManager{
				rdtcgs:            tt.fields.rdtcgs,
				reconcileInterval: tt.fields.reconcileInterval,
			}
			c.Init()
			p, ok := c.rdtcgs.Get("pod1")
			assert.Equal(t, true, ok)
			pod1 := p.(*ControlGroup)
			assert.Equal(t, ClosdIdPrefix+"pod1", pod1.GroupId)
			assert.Equal(t, "L3:0=f0\nMB:0=100", pod1.Schemata)

			p, ok = c.rdtcgs.Get("pod2")
			assert.Equal(t, true, ok)
			pod2 := p.(*ControlGroup)
			assert.Equal(t, ClosdIdPrefix+"pod2", pod2.GroupId)
			assert.Equal(t, "L3:0=fc\nMB:0=100", pod2.Schemata)
		})
	}
}

func TestControlGroupManager_RemovePod(t *testing.T) {
	type fields struct {
		rdtcgs            *gocache.Cache
		reconcileInterval int64
	}
	type args struct {
		podid         string
		fromNRI       bool
		removeUpdater ResctrlUpdater
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "RemovePod from NRI while not exist in rdtcgs",
			fields: fields{
				rdtcgs:            gocache.New(time.Duration(ExpirationTime), CleanupInterval),
				reconcileInterval: 0,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ControlGroupManager{
				rdtcgs:            tt.fields.rdtcgs,
				reconcileInterval: tt.fields.reconcileInterval,
			}
			if got := c.RemovePod(tt.args.podid, tt.args.fromNRI, tt.args.removeUpdater); got != tt.want {
				t.Errorf("RemovePod() = %v, want %v", got, tt.want)
			}
		})
	}
}
