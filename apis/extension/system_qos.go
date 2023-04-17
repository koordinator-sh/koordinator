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

package extension

import (
	"encoding/json"
)

const (
	AnnotationNodeSystemQOSResource = NodeDomainPrefix + "/system-qos-resource"
)

type SystemQOSResource struct {
	// CPU cores used for System QoS Pods, format should follow Linux CPU list
	// See: http://man7.org/linux/man-pages/man7/cpuset.7.html#FORMATS
	CPUSet string `json:"cpuset,omitempty"`
	// whether CPU cores for System QoS are exclusive(default = true), which means could not be used by other pods(LS/LSR/BE)
	CPUSetExclusive *bool `json:"cpusetExclusive,omitempty"`
}

func (r *SystemQOSResource) IsCPUSetExclusive() bool {
	// CPUSetExclusive default is true
	return r.CPUSetExclusive == nil || *r.CPUSetExclusive
}

func GetSystemQOSResource(anno map[string]string) (*SystemQOSResource, error) {
	if anno == nil {
		return nil, nil
	}
	systemQOSRes := &SystemQOSResource{}
	data, ok := anno[AnnotationNodeSystemQOSResource]
	if !ok {
		return systemQOSRes, nil
	}
	if err := json.Unmarshal([]byte(data), systemQOSRes); err != nil {
		return nil, err
	}
	return systemQOSRes, nil
}
