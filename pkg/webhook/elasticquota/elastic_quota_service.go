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

package elasticquota

import (
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
)

type QuotaTopologySummary struct {
	QuotaInfoMap       map[string]*core.QuotaInfoSummary
	QuotaHierarchyInfo map[string][]string
}

func NewQuotaTopologyForMarshal() *QuotaTopologySummary {
	return &QuotaTopologySummary{
		QuotaInfoMap:       make(map[string]*core.QuotaInfoSummary),
		QuotaHierarchyInfo: make(map[string][]string),
	}
}

func (qt *quotaTopology) getQuotaTopologyInfo() *QuotaTopologySummary {
	result := NewQuotaTopologyForMarshal()

	qt.lock.Lock()
	defer qt.lock.Unlock()

	for key, value := range qt.quotaInfoMap {
		result.QuotaInfoMap[key] = value.GetQuotaSummary()
	}

	for key, value := range qt.quotaHierarchyInfo {
		childQuotas := make([]string, 0, len(value))
		for name := range value {
			childQuotas = append(childQuotas, name)
		}
		result.QuotaHierarchyInfo[key] = childQuotas
	}
	return result
}
