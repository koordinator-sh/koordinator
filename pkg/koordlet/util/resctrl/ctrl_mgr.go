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
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"k8s.io/klog/v2"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	Remove                = "Remove"
	Add                   = "Add"
	ExpirationTime  int64 = 10
	CleanupInterval       = 600 * time.Second
)

type Updater interface {
	Update(string) error
}

type SchemataUpdater interface {
	Update(id, schemata string) error
}

type ControlGroup struct {
	AppId       string
	GroupId     string
	Schemata    string
	Status      string
	CreatedTime int64
}

type ControlGroupManager struct {
	rdtcgs            *gocache.Cache
	reconcileInterval int64
	sync.Mutex
}

func NewControlGroupManager() ControlGroupManager {
	return ControlGroupManager{
		rdtcgs: gocache.New(time.Duration(ExpirationTime), CleanupInterval),
	}
}

func (c *ControlGroupManager) Init() {
	c.Lock()
	defer c.Unlock()

	// get resctrl filesystem root
	root := sysutil.GetResctrlSubsystemDirPath()
	files, err := os.ReadDir(root)
	if err != nil {
		klog.Errorf("read %s failed err is %v", root, err)
		return
	}

	// rebuild c.rdtcgs when restart
	for _, file := range files {
		if file.IsDir() && strings.HasPrefix(file.Name(), ClosdIdPrefix) {
			path := filepath.Join(root, file.Name(), "schemata")
			if _, err := os.Stat(path); err == nil {
				reader, err := os.Open(path)
				if err != nil {
					klog.Errorf("open resctrl file path fail, %v", err)
				}
				content, err := io.ReadAll(reader)
				if err != nil {
					klog.Errorf("read resctrl file path fail, %v", err)
					return
				}
				schemata := string(content)
				podid := strings.TrimPrefix(file.Name(), ClosdIdPrefix)
				c.rdtcgs.Set(podid, &ControlGroup{
					AppId:       podid,
					GroupId:     file.Name(),
					Schemata:    schemata,
					Status:      Add,
					CreatedTime: time.Now().UnixNano(),
				}, -1)
				klog.V(5).Infof("podid is %s, ctrl group is %v", podid, file.Name())
			}
		}
	}
}

func (c *ControlGroupManager) AddPod(podid string, schemata string, fromNRI bool, createUpdater ResctrlUpdater, schemataUpdater ResctrlUpdater) error {
	c.Lock()
	defer c.Unlock()

	p, ok := c.rdtcgs.Get(podid)
	var pod *ControlGroup
	if !ok {
		pod = &ControlGroup{
			AppId:    podid,
			GroupId:  "",
			Schemata: "",
			Status:   Add,
		}
	} else {
		pod = p.(*ControlGroup)
	}

	if pod.Status == Add && pod.GroupId == "" {
		if createUpdater != nil {
			err := createUpdater.Update()
			if err != nil {
				klog.Errorf("create ctrl group error %v", err)
				return err
			} else {
				pod.GroupId = ClosdIdPrefix + podid
				pod.CreatedTime = time.Now().UnixNano()
			}
		}

		if schemataUpdater != nil {
			err := schemataUpdater.Update()
			if err != nil {
				klog.Errorf("updater ctrl group schemata error %v", err)
				return err
			}
			pod.Schemata = schemata
		}

		c.rdtcgs.Set(podid, pod, -1)
	} else {
		if pod.Status == Add && pod.GroupId != "" && !fromNRI {
			// Update Schemata
			if schemataUpdater != nil {
				err := schemataUpdater.Update()
				if err != nil {
					klog.Errorf("updater ctrl group schemata error %v", err)
					return err
				}
				pod.Schemata = schemata
			}
			c.rdtcgs.Set(podid, pod, -1)
		}
	}
	return nil
}

func (c *ControlGroupManager) RemovePod(podid string, fromNRI bool, removeUpdater ResctrlUpdater) bool {
	c.Lock()
	defer c.Unlock()

	p, ok := c.rdtcgs.Get(podid)
	if !ok {
		pod := &ControlGroup{podid, "", "", Remove, -1}
		if removeUpdater != nil {
			err := removeUpdater.Update()
			if err != nil {
				klog.Errorf("remove updater fail %v", err)
				return false
			}
		}

		c.rdtcgs.Set(podid, pod, gocache.DefaultExpiration)
		return true
	}
	pod := p.(*ControlGroup)
	if (fromNRI || time.Now().UnixNano()-pod.CreatedTime >= ExpirationTime*time.Second.Nanoseconds()) && pod.Status == Add {
		pod.Status = Remove
		if removeUpdater != nil {
			err := removeUpdater.Update()
			if err != nil {
				klog.Errorf("remove updater fail %v", err)
				return false
			}
		}

		c.rdtcgs.Set(podid, pod, gocache.DefaultExpiration)
		return true
	}
	return false
}
