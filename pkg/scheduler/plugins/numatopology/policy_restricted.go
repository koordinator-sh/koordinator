/*
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

package numatopology

import "github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"

type restrictedPolicy struct {
	bestEffortPolicy
}

var _ Policy = &restrictedPolicy{}

// PolicyRestricted policy name.
const PolicyRestricted string = "restricted"

// NewRestrictedPolicy returns restricted policy.
func NewRestrictedPolicy(numaNodes []int) Policy {
	return &restrictedPolicy{bestEffortPolicy{numaNodes: numaNodes}}
}

func (p *restrictedPolicy) Name() string {
	return PolicyRestricted
}

func (p *restrictedPolicy) canAdmitPodResult(hint *frameworkext.NUMATopologyHint) bool {
	return hint.Preferred
}

func (p *restrictedPolicy) Merge(providersHints []map[string][]frameworkext.NUMATopologyHint) (frameworkext.NUMATopologyHint, bool) {
	filteredHints := filterProvidersHints(providersHints)
	hint := mergeFilteredHints(p.numaNodes, filteredHints)
	admit := p.canAdmitPodResult(&hint)
	return hint, admit
}
