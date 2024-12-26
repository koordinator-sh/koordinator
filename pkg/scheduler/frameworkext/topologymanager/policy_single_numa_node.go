/*
Copyright 2022 The Koordinator Authors.
Copyright 2019 The Kubernetes Authors.

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

package topologymanager

import (
	"fmt"
	"strings"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
)

type singleNumaNodePolicy struct {
	//List of NUMA Nodes available on the underlying machine
	numaNodes []int
}

var _ Policy = &singleNumaNodePolicy{}

// PolicySingleNumaNode policy name.
const PolicySingleNumaNode string = "single-numa-node"

// NewSingleNumaNodePolicy returns single-numa-node policy.
func NewSingleNumaNodePolicy(numaNodes []int) Policy {
	return &singleNumaNodePolicy{numaNodes: numaNodes}
}

func (p *singleNumaNodePolicy) Name() string {
	return PolicySingleNumaNode
}

func (p *singleNumaNodePolicy) canAdmitPodResult(hint *NUMATopologyHint) bool {
	return hint.Preferred
}

// Return hints that have valid bitmasks with exactly one bit set.
func filterSingleNumaHints(allResourcesHints [][]NUMATopologyHint) [][]NUMATopologyHint {
	var filteredResourcesHints [][]NUMATopologyHint
	for _, oneResourceHints := range allResourcesHints {
		var filtered []NUMATopologyHint
		for _, hint := range oneResourceHints {
			if hint.NUMANodeAffinity == nil && hint.Preferred {
				filtered = append(filtered, hint)
			}
			if hint.NUMANodeAffinity != nil && hint.NUMANodeAffinity.Count() == 1 && hint.Preferred {
				filtered = append(filtered, hint)
			}
		}
		filteredResourcesHints = append(filteredResourcesHints, filtered)
	}
	return filteredResourcesHints
}

func (p *singleNumaNodePolicy) Merge(providersHints []map[string][]NUMATopologyHint, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) (NUMATopologyHint, bool, []string) {
	filteredHints, reasons, summary := filterProvidersHints(providersHints)
	if len(reasons) != 0 {
		return NUMATopologyHint{}, false, reasons
	}
	// Filter to only include don't care and hints with a single NUMA node.
	singleNumaHints := filterSingleNumaHints(filteredHints)
	bestHint := mergeFilteredHints(p.numaNodes, singleNumaHints, exclusivePolicy, allNUMANodeStatus)

	defaultAffinity, _ := bitmask.NewBitMask(p.numaNodes...)
	if bestHint.NUMANodeAffinity.IsEqual(defaultAffinity) {
		bestHint = NUMATopologyHint{
			Preferred: bestHint.Preferred,
		}
	}

	admit := p.canAdmitPodResult(&bestHint)
	if !admit {
		return bestHint, false, []string{fmt.Sprintf(ErrNUMAHintCannotAligned, strings.Join(summary, ","))}
	}
	return bestHint, admit, nil
}
