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

package tc

import (
	"sync"
)

type tcRule struct {
	lock   sync.RWMutex
	enable bool
	netCfg *NetQosGlobalConfig
	speed  uint64
}

func newRule() *tcRule {
	return &tcRule{
		lock:   sync.RWMutex{},
		enable: false,
		netCfg: nil,
		speed:  0,
	}
}

func (r *tcRule) IsEnabled() bool {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.enable
}

func (r *tcRule) GetNetCfg() *NetQosGlobalConfig {
	return r.netCfg
}
