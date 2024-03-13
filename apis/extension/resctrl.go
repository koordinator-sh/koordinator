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
	// AnnotationResctrl describes the resctrl config of pod
	AnnotationResctrl = NodeDomainPrefix + "/resctrl"
)

type Resctrl struct {
	L3 map[int]string
	MB map[int]string
}

type ResctrlConfig struct {
	LLC LLC `json:"llc,omitempty"`
	MB  MB  `json:"mb,omitempty"`
}

type LLC struct {
	Schemata         SchemataConfig           `json:"schemata,omitempty"`
	SchemataPerCache []SchemataPerCacheConfig `json:"schemataPerCache,omitempty"`
}

type MB struct {
	Schemata         SchemataConfig           `json:"schemata,omitempty"`
	SchemataPerCache []SchemataPerCacheConfig `json:"schemataPerCache,omitempty"`
}

type SchemataConfig struct {
	Percent int   `json:"percent,omitempty"`
	Range   []int `json:"range,omitempty"`
}

type SchemataPerCacheConfig struct {
	CacheID        int `json:"cacheID,omitempty"`
	SchemataConfig `json:",inline"`
}

func GetResctrlInfo(annotations map[string]string) (*ResctrlConfig, error) {
	res := &ResctrlConfig{}
	data, ok := annotations[AnnotationResctrl]
	if !ok {
		return res, nil
	}
	err := json.Unmarshal([]byte(data), &res)
	if err != nil {
		return nil, err
	}
	return res, nil
}
