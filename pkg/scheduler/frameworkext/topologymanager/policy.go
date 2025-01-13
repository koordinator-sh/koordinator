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
	"sort"
	"strings"

	"k8s.io/klog/v2"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/bitmask"
)

type Policy interface {
	// Name returns Policy Name
	Name() string
	// Merge returns a merged NUMATopologyHint based on input from hint providers
	Merge(providersHints []map[string][]NUMATopologyHint, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) (NUMATopologyHint, bool, []string)
}

// NUMATopologyHint is a struct containing the NUMANodeAffinity for a Container
type NUMATopologyHint struct {
	NUMANodeAffinity bitmask.BitMask
	// Unsatisfied is set to true when the NUMANodeAffinity is insufficient to satisfy the resource request.
	Unsatisfied bool
	// Preferred is set to true when the NUMANodeAffinity encodes a preferred
	// allocation for the Pod. It is set to false otherwise.
	Preferred bool
	// Score is the weight of this hint. For the same Affinity,
	// the one with higher weight will be used first.
	Score int64
}

// IsEqual checks if NUMATopologyHint are equal
func (th *NUMATopologyHint) IsEqual(topologyHint NUMATopologyHint) bool {
	if th.Preferred == topologyHint.Preferred {
		if th.NUMANodeAffinity == nil || topologyHint.NUMANodeAffinity == nil {
			return th.NUMANodeAffinity == topologyHint.NUMANodeAffinity
		}
		return th.NUMANodeAffinity.IsEqual(topologyHint.NUMANodeAffinity)
	}
	return false
}

// LessThan checks if NUMATopologyHint `a` is less than NUMATopologyHint `b`
// this means that either `a` is a preferred hint and `b` is not
// or `a` NUMANodeAffinity attribute is narrower than `b` NUMANodeAffinity attribute.
func (th *NUMATopologyHint) LessThan(other NUMATopologyHint) bool {
	if th.Preferred != other.Preferred {
		return th.Preferred
	}
	return th.NUMANodeAffinity.IsNarrowerThan(other.NUMANodeAffinity)
}

// Check if the affinity match the exclusive policy, return true if match or false otherwise.
func checkExclusivePolicy(affinity NUMATopologyHint, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) bool {
	// check bestHint again if default hint is the best
	if affinity.NUMANodeAffinity == nil {
		return false
	}
	if exclusivePolicy == apiext.NumaTopologyExclusiveRequired {
		if affinity.NUMANodeAffinity.Count() > 1 {
			// we should make sure no numa is in single state
			for _, nodeid := range affinity.NUMANodeAffinity.GetBits() {
				if allNUMANodeStatus[nodeid] == apiext.NumaNodeStatusSingle {
					return false
				}
			}
		} else {
			if allNUMANodeStatus[affinity.NUMANodeAffinity.GetBits()[0]] == apiext.NumaNodeStatusShared {
				return false
			}
		}
	}
	return true
}

// Merge a TopologyHints permutation to a single hint by performing a bitwise-AND
// of their affinity masks. The hint shall be preferred if all hits in the permutation
// are preferred.
func mergePermutation(numaNodes []int, permutation []NUMATopologyHint) NUMATopologyHint {
	// Get the NUMANodeAffinity from each hint in the permutation and see if any
	// of them encode unpreferred allocations.
	preferred := true
	defaultAffinity, _ := bitmask.NewBitMask(numaNodes...)
	maxNUMANodeNum := 0
	satisfied := true
	var numaAffinities []bitmask.BitMask
	for _, hint := range permutation {
		// Only consider hints that have an actual NUMANodeAffinity set.
		if hint.NUMANodeAffinity != nil {
			numaAffinities = append(numaAffinities, hint.NUMANodeAffinity)
			// Only mark preferred if all affinities are equal.
			if !hint.NUMANodeAffinity.IsEqual(numaAffinities[0]) {
				preferred = false
			}
			if maxNUMANodeNum < hint.NUMANodeAffinity.Count() {
				maxNUMANodeNum = hint.NUMANodeAffinity.Count()
			}
		}
		// Only mark preferred if all affinities are preferred.
		if !hint.Preferred {
			preferred = false
		}
		if hint.Unsatisfied {
			satisfied = false
		}
	}

	// Merge the affinities using a bitwise-and operation.
	mergedAffinity := bitmask.And(defaultAffinity, numaAffinities...)
	satisfied = ((len(numaAffinities) == 0) || maxNUMANodeNum == mergedAffinity.Count()) && satisfied
	// Build a mergedHint from the merged affinity mask, indicating if an
	// preferred allocation was used to generate the affinity mask or not.
	return NUMATopologyHint{
		NUMANodeAffinity: mergedAffinity,
		Unsatisfied:      !satisfied,
		Preferred:        preferred,
	}
}

func filterProvidersHints(providersHints []map[string][]NUMATopologyHint) ([][]NUMATopologyHint, []string, []string) {
	// Loop through all hint providers and save an accumulated list of the
	// hints returned by each hint provider. If no hints are provided, assume
	// that provider has no preference for topology-aware allocation.
	var allProviderHints [][]NUMATopologyHint
	var reasons []string
	var summary []string
	for _, hints := range providersHints {
		// If hints is nil, insert a single, preferred any-numa hint into allProviderHints.
		if len(hints) == 0 {
			klog.V(5).Infof("[topologymanager] Hint Provider has no preference for NUMA affinity with any resource")
			allProviderHints = append(allProviderHints, []NUMATopologyHint{{
				Preferred: true,
			}})
			continue
		}

		// Otherwise, accumulate the hints for each resource type into allProviderHints.
		for resource := range hints {
			if hints[resource] == nil {
				klog.V(5).Infof("[topologymanager] Hint Provider has no preference for NUMA affinity with resource '%s'", resource)
				allProviderHints = append(allProviderHints, []NUMATopologyHint{{
					Preferred: true,
				}})
				continue
			}

			if len(hints[resource]) == 0 {
				klog.V(5).Infof("[topologymanager] Hint Provider has no possible NUMA affinities for resource '%s'", resource)
				allProviderHints = append(allProviderHints, []NUMATopologyHint{{
					Unsatisfied: true,
					Preferred:   false,
				}})
				reasons = append(reasons, fmt.Sprintf(ErrUnsatisfiedNUMAResource, resource))
				continue
			}
			summary = append(summary, getSummaryForResource(resource, hints[resource]))
			allProviderHints = append(allProviderHints, hints[resource])
		}
	}
	sort.Strings(summary)
	return allProviderHints, reasons, summary
}

func getSummaryForResource(resource string, hints []NUMATopologyHint) string {
	var preferredHints, possibleHints []string
	for _, hint := range hints {
		if hint.Unsatisfied {
			continue
		}
		rawNUMAAffinity := fmt.Sprintf("%v", hint.NUMANodeAffinity)
		if hint.Preferred {
			preferredHints = append(preferredHints, rawNUMAAffinity)
		}
		possibleHints = append(possibleHints, rawNUMAAffinity)
	}
	return fmt.Sprintf("%v prefer [%v] among [%v]", resource, strings.Join(preferredHints, " "), strings.Join(possibleHints, " "))
}

func mergeFilteredHints(numaNodes []int, filteredHints [][]NUMATopologyHint, exclusivePolicy apiext.NumaTopologyExclusive, allNUMANodeStatus []apiext.NumaNodeStatus) NUMATopologyHint {
	// Set the default affinity as an any-numa affinity containing the list
	// of NUMA Nodes available on this machine.
	defaultAffinity, _ := bitmask.NewBitMask(numaNodes...)

	// Set the bestHint to return from this function as {nil false}.
	// This will only be returned if no better hint can be found when
	// merging hints from each hint provider.
	bestHint := NUMATopologyHint{
		NUMANodeAffinity: defaultAffinity,
	}
	iterateAllProviderTopologyHints(filteredHints, func(permutation []NUMATopologyHint) {
		// Get the NUMANodeAffinity from each hint in the permutation and see if any
		// of them encode unpreferred allocations.
		mergedHint := mergePermutation(numaNodes, permutation)

		// Only consider mergedHints that result in a NUMANodeAffinity > 0 to
		// replace the current bestHint.
		if mergedHint.NUMANodeAffinity.Count() == 0 {
			return
		}
		if !checkExclusivePolicy(mergedHint, exclusivePolicy, allNUMANodeStatus) {
			mergedHint.Preferred = false
		}

		for _, v := range permutation {
			if v.NUMANodeAffinity != nil && mergedHint.NUMANodeAffinity.IsEqual(v.NUMANodeAffinity) {
				mergedHint.Score += v.Score
			}
		}

		// If the current bestHint is non-preferred and the new mergedHint is
		// preferred, always choose the preferred hint over the non-preferred one.
		if mergedHint.Preferred && !bestHint.Preferred {
			bestHint = mergedHint
			return
		}

		// If the current bestHint is preferred and the new mergedHint is
		// non-preferred, never update bestHint, regardless of mergedHint's
		// narowness.
		if !mergedHint.Preferred && bestHint.Preferred {
			return
		}

		// If mergedHint and bestHint has the same preference, only consider
		// mergedHints that have a narrower NUMANodeAffinity than the
		// NUMANodeAffinity in the current bestHint.
		if !mergedHint.NUMANodeAffinity.IsNarrowerThan(bestHint.NUMANodeAffinity) {
			if mergedHint.NUMANodeAffinity.Count() == bestHint.NUMANodeAffinity.Count() {
				if mergedHint.Score > bestHint.Score {
					bestHint = mergedHint
				}
			}
			return
		}

		// In all other cases, update bestHint to the current mergedHint
		bestHint = mergedHint
	})

	return bestHint
}

// Iterate over all permutations of hints in 'allProviderHints [][]TopologyHint'.
//
// This procedure is implemented as a recursive function over the set of hints
// in 'allproviderHints[i]'. It applies the function 'callback' to each
// permutation as it is found. It is the equivalent of:
//
// for i := 0; i < len(providerHints[0]); i++
//
//	for j := 0; j < len(providerHints[1]); j++
//	    for k := 0; k < len(providerHints[2]); k++
//	        ...
//	        for z := 0; z < len(providerHints[-1]); z++
//	            permutation := []TopologyHint{
//	                providerHints[0][i],
//	                providerHints[1][j],
//	                providerHints[2][k],
//	                ...
//	                providerHints[-1][z]
//	            }
//	            callback(permutation)
func iterateAllProviderTopologyHints(allProviderHints [][]NUMATopologyHint, callback func([]NUMATopologyHint)) {
	// Internal helper function to accumulate the permutation before calling the callback.
	var iterate func(i int, accum []NUMATopologyHint)
	iterate = func(i int, accum []NUMATopologyHint) {
		// Base case: we have looped through all providers and have a full permutation.
		if i == len(allProviderHints) {
			callback(accum)
			return
		}

		// Loop through all hints for provider 'i', and recurse to build the
		// the permutation of this hint with all hints from providers 'i++'.
		for j := range allProviderHints[i] {
			iterate(i+1, append(accum, allProviderHints[i][j]))
		}
	}
	iterate(0, []NUMATopologyHint{})
}
