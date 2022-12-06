/*
Copyright 2017 The Kubernetes Authors.

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

package kubelet

import (
	"reflect"
	"sort"
	"testing"

	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
)

func TestCPUAccumulatorFreeSockets(t *testing.T) {
	testCases := []struct {
		description   string
		topo          *topology.CPUTopology
		availableCPUs cpuset.CPUSet
		expect        []int
	}{
		{
			"single socket HT, 1 socket free",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			[]int{0},
		},
		{
			"single socket HT, 0 sockets free",
			topoSingleSocketHT,
			cpuset.NewCPUSet(1, 2, 3, 4, 5, 6, 7),
			[]int{},
		},
		{
			"dual socket HT, 2 sockets free",
			topoDualSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			[]int{0, 1},
		},
		{
			"dual socket HT, 1 socket free",
			topoDualSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 11),
			[]int{1},
		},
		{
			"dual socket HT, 0 sockets free",
			topoDualSocketHT,
			cpuset.NewCPUSet(0, 2, 3, 4, 5, 6, 7, 8, 9, 11),
			[]int{},
		},
		{
			"dual socket, multi numa per socket, HT, 2 sockets free",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "0-79"),
			[]int{0, 1},
		},
		{
			"dual socket, multi numa per socket, HT, 1 sockets free",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "1-79"),
			[]int{1},
		},
		{
			"dual socket, multi numa per socket, HT, 0 sockets free",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "1-78"),
			[]int{},
		},
		{
			"dual numa, multi socket per per socket, HT, 4 sockets free",
			fakeTopoMultiSocketDualSocketPerNumaHT,
			mustParseCPUSet(t, "0-79"),
			[]int{0, 1, 2, 3},
		},
		{
			"dual numa, multi socket per per socket, HT, 3 sockets free",
			fakeTopoMultiSocketDualSocketPerNumaHT,
			mustParseCPUSet(t, "0-19,21-79"),
			[]int{0, 1, 3},
		},
		{
			"dual numa, multi socket per per socket, HT, 2 sockets free",
			fakeTopoMultiSocketDualSocketPerNumaHT,
			mustParseCPUSet(t, "0-59,61-78"),
			[]int{0, 1},
		},
		{
			"dual numa, multi socket per per socket, HT, 1 sockets free",
			fakeTopoMultiSocketDualSocketPerNumaHT,
			mustParseCPUSet(t, "1-19,21-38,41-60,61-78"),
			[]int{1},
		},
		{
			"dual numa, multi socket per per socket, HT, 0 sockets free",
			fakeTopoMultiSocketDualSocketPerNumaHT,
			mustParseCPUSet(t, "0-40,42-49,51-68,71-79"),
			[]int{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			acc := newCPUAccumulator(tc.topo, tc.availableCPUs, 0)
			result := acc.freeSockets()
			sort.Ints(result)
			if !reflect.DeepEqual(result, tc.expect) {
				t.Errorf("expected %v to equal %v", result, tc.expect)

			}
		})
	}
}

func TestCPUAccumulatorFreeSocketsAndNUMANodes(t *testing.T) {
	testCases := []struct {
		description     string
		topo            *topology.CPUTopology
		availableCPUs   cpuset.CPUSet
		expectSockets   []int
		expectNUMANodes []int
	}{
		{
			"dual socket, multi numa per socket, HT, 2 Socket/4 NUMA Node free",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "0-79"),
			[]int{0, 1},
			[]int{0, 1, 2, 3},
		},
		{
			"dual socket, multi numa per socket, HT, 1 Socket/3 NUMA node free",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "1-79"),
			[]int{1},
			[]int{1, 2, 3},
		},
		{
			"dual socket, multi numa per socket, HT, 1 Socket/ 2 NUMA node free",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "1-9,11-79"),
			[]int{1},
			[]int{2, 3},
		},
		{
			"dual socket, multi numa per socket, HT, 0 Socket/ 2 NUMA node free",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "1-59,61-79"),
			[]int{},
			[]int{1, 3},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			acc := newCPUAccumulator(tc.topo, tc.availableCPUs, 0)
			resultNUMANodes := acc.freeNUMANodes()
			if !reflect.DeepEqual(resultNUMANodes, tc.expectNUMANodes) {
				t.Errorf("expected NUMA Nodes %v to equal %v", resultNUMANodes, tc.expectNUMANodes)
			}
			resultSockets := acc.freeSockets()
			if !reflect.DeepEqual(resultSockets, tc.expectSockets) {
				t.Errorf("expected Sockets %v to equal %v", resultSockets, tc.expectSockets)
			}
		})
	}
}

func TestCPUAccumulatorFreeCores(t *testing.T) {
	testCases := []struct {
		description   string
		topo          *topology.CPUTopology
		availableCPUs cpuset.CPUSet
		expect        []int
	}{
		{
			"single socket HT, 4 cores free",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			[]int{0, 1, 2, 3},
		},
		{
			"single socket HT, 3 cores free",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 4, 5, 6),
			[]int{0, 1, 2},
		},
		{
			"single socket HT, 3 cores free (1 partially consumed)",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6),
			[]int{0, 1, 2},
		},
		{
			"single socket HT, 0 cores free",
			topoSingleSocketHT,
			cpuset.NewCPUSet(),
			[]int{},
		},
		{
			"single socket HT, 0 cores free (4 partially consumed)",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3),
			[]int{},
		},
		{
			"dual socket HT, 6 cores free",
			topoDualSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			[]int{0, 2, 4, 1, 3, 5},
		},
		{
			"dual socket HT, 5 cores free (1 consumed from socket 0)",
			topoDualSocketHT,
			cpuset.NewCPUSet(2, 1, 3, 4, 5, 7, 8, 9, 10, 11),
			[]int{2, 4, 1, 3, 5},
		},
		{
			"dual socket HT, 4 cores free (1 consumed from each socket)",
			topoDualSocketHT,
			cpuset.NewCPUSet(2, 3, 4, 5, 8, 9, 10, 11),
			[]int{2, 4, 3, 5},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			acc := newCPUAccumulator(tc.topo, tc.availableCPUs, 0)
			result := acc.freeCores()
			if !reflect.DeepEqual(result, tc.expect) {
				t.Errorf("expected %v to equal %v", result, tc.expect)
			}
		})
	}
}

func TestCPUAccumulatorFreeCPUs(t *testing.T) {
	testCases := []struct {
		description   string
		topo          *topology.CPUTopology
		availableCPUs cpuset.CPUSet
		expect        []int
	}{
		{
			"single socket HT, 8 cpus free",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			[]int{0, 4, 1, 5, 2, 6, 3, 7},
		},
		{
			"single socket HT, 5 cpus free",
			topoSingleSocketHT,
			cpuset.NewCPUSet(3, 4, 5, 6, 7),
			[]int{4, 5, 6, 3, 7},
		},
		{
			"dual socket HT, 12 cpus free",
			topoDualSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			[]int{0, 6, 2, 8, 4, 10, 1, 7, 3, 9, 5, 11},
		},
		{
			"dual socket HT, 11 cpus free",
			topoDualSocketHT,
			cpuset.NewCPUSet(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			[]int{6, 2, 8, 4, 10, 1, 7, 3, 9, 5, 11},
		},
		{
			"dual socket HT, 10 cpus free",
			topoDualSocketHT,
			cpuset.NewCPUSet(1, 2, 3, 4, 5, 7, 8, 9, 10, 11),
			[]int{2, 8, 4, 10, 1, 7, 3, 9, 5, 11},
		},
		{
			"triple socket HT, 12 cpus free",
			topoTripleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 6, 7, 8, 9, 10, 11, 12, 13),
			[]int{12, 13, 0, 1, 2, 3, 6, 7, 8, 9, 10, 11},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			acc := newCPUAccumulator(tc.topo, tc.availableCPUs, 0)
			result := acc.freeCPUs()
			if !reflect.DeepEqual(result, tc.expect) {
				t.Errorf("expected %v to equal %v", result, tc.expect)
			}
		})
	}
}

func TestCPUAccumulatorTake(t *testing.T) {
	testCases := []struct {
		description     string
		topo            *topology.CPUTopology
		availableCPUs   cpuset.CPUSet
		takeCPUs        []cpuset.CPUSet
		numCPUs         int
		expectSatisfied bool
		expectFailed    bool
	}{
		{
			"take 0 cpus from a single socket HT, require 1",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			[]cpuset.CPUSet{cpuset.NewCPUSet()},
			1,
			false,
			false,
		},
		{
			"take 0 cpus from a single socket HT, require 1, none available",
			topoSingleSocketHT,
			cpuset.NewCPUSet(),
			[]cpuset.CPUSet{cpuset.NewCPUSet()},
			1,
			false,
			true,
		},
		{
			"take 1 cpu from a single socket HT, require 1",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			[]cpuset.CPUSet{cpuset.NewCPUSet(0)},
			1,
			true,
			false,
		},
		{
			"take 1 cpu from a single socket HT, require 2",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			[]cpuset.CPUSet{cpuset.NewCPUSet(0)},
			2,
			false,
			false,
		},
		{
			"take 2 cpu from a single socket HT, require 4, expect failed",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2),
			[]cpuset.CPUSet{cpuset.NewCPUSet(0), cpuset.NewCPUSet(1)},
			4,
			false,
			true,
		},
		{
			"take all cpus one at a time from a single socket HT, require 8",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			[]cpuset.CPUSet{
				cpuset.NewCPUSet(0),
				cpuset.NewCPUSet(1),
				cpuset.NewCPUSet(2),
				cpuset.NewCPUSet(3),
				cpuset.NewCPUSet(4),
				cpuset.NewCPUSet(5),
				cpuset.NewCPUSet(6),
				cpuset.NewCPUSet(7),
			},
			8,
			true,
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			acc := newCPUAccumulator(tc.topo, tc.availableCPUs, tc.numCPUs)
			totalTaken := 0
			for _, cpus := range tc.takeCPUs {
				acc.take(cpus)
				totalTaken += cpus.Size()
			}
			if tc.expectSatisfied != acc.isSatisfied() {
				t.Errorf("expected acc.isSatisfied() to be %t", tc.expectSatisfied)
			}
			if tc.expectFailed != acc.isFailed() {
				t.Errorf("expected acc.isFailed() to be %t", tc.expectFailed)
			}
			for _, cpus := range tc.takeCPUs {
				availableCPUs := acc.details.CPUs()
				if cpus.Intersection(availableCPUs).Size() > 0 {
					t.Errorf("expected intersection of taken cpus [%s] and acc.details.CPUs() [%s] to be empty", cpus, availableCPUs)
				}
				if !cpus.IsSubsetOf(acc.result) {
					t.Errorf("expected [%s] to be a subset of acc.result [%s]", cpus, acc.result)
				}
			}
			expNumCPUsNeeded := tc.numCPUs - totalTaken
			if acc.numCPUsNeeded != expNumCPUsNeeded {
				t.Errorf("expected acc.numCPUsNeeded to be %d (got %d)", expNumCPUsNeeded, acc.numCPUsNeeded)
			}
		})
	}
}

type takeByTopologyTestCase struct {
	description   string
	topo          *topology.CPUTopology
	availableCPUs cpuset.CPUSet
	numCPUs       int
	expErr        string
	expResult     cpuset.CPUSet
}

func commonTakeByTopologyTestCases(t *testing.T) []takeByTopologyTestCase {
	return []takeByTopologyTestCase{
		{
			"take more cpus than are available from single socket with HT",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 2, 4, 6),
			5,
			"not enough cpus available to satisfy request",
			cpuset.NewCPUSet(),
		},
		{
			"take zero cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			0,
			"",
			cpuset.NewCPUSet(),
		},
		{
			"take one cpu from single socket with HT",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			1,
			"",
			cpuset.NewCPUSet(0),
		},
		{
			"take one cpu from single socket with HT, some cpus are taken",
			topoSingleSocketHT,
			cpuset.NewCPUSet(1, 3, 5, 6, 7),
			1,
			"",
			cpuset.NewCPUSet(6),
		},
		{
			"take two cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			2,
			"",
			cpuset.NewCPUSet(0, 4),
		},
		{
			"take all cpus from single socket with HT",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			8,
			"",
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
		},
		{
			"take two cpus from single socket with HT, only one core totally free",
			topoSingleSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 6),
			2,
			"",
			cpuset.NewCPUSet(2, 6),
		},
		{
			"take a socket of cpus from dual socket with HT",
			topoDualSocketHT,
			cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			6,
			"",
			cpuset.NewCPUSet(0, 2, 4, 6, 8, 10),
		},
		{
			"take a socket of cpus from dual socket with multi-numa-per-socket with HT",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "0-79"),
			40,
			"",
			mustParseCPUSet(t, "0-19,40-59"),
		},
		{
			"take a NUMA node of cpus from dual socket with multi-numa-per-socket with HT",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "0-79"),
			20,
			"",
			mustParseCPUSet(t, "0-9,40-49"),
		},
		{
			"take a NUMA node of cpus from dual socket with multi-numa-per-socket with HT, with 1 NUMA node already taken",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "10-39,50-79"),
			20,
			"",
			mustParseCPUSet(t, "10-19,50-59"),
		},
		{
			"take a socket and a NUMA node of cpus from dual socket with multi-numa-per-socket with HT",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "0-79"),
			60,
			"",
			mustParseCPUSet(t, "0-29,40-69"),
		},
		{
			"take a socket and a NUMA node of cpus from dual socket with multi-numa-per-socket with HT, a core taken",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "1-39,41-79"), // reserve the first (phys) core (0,40)
			60,
			"",
			mustParseCPUSet(t, "10-39,50-79"),
		},
	}
}

func TestTakeByTopologyNUMAPacked(t *testing.T) {
	testCases := commonTakeByTopologyTestCases(t)
	testCases = append(testCases, []takeByTopologyTestCase{
		{
			"take one cpu from dual socket with HT - core from Socket 0",
			topoDualSocketHT,
			cpuset.NewCPUSet(1, 2, 3, 4, 5, 7, 8, 9, 10, 11),
			1,
			"",
			cpuset.NewCPUSet(2),
		},
		{
			"allocate 4 full cores with 3 coming from the first NUMA node (filling it up) and 1 coming from the second NUMA node",
			topoDualSocketHT,
			mustParseCPUSet(t, "0-11"),
			8,
			"",
			mustParseCPUSet(t, "0,6,2,8,4,10,1,7"),
		},
		{
			"allocate 32 full cores with 30 coming from the first 3 NUMA nodes (filling them up) and 2 coming from the fourth NUMA node",
			topoDualSocketMultiNumaPerSocketHT,
			mustParseCPUSet(t, "0-79"),
			64,
			"",
			mustParseCPUSet(t, "0-29,40-69,30,31,70,71"),
		},
	}...)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result, err := takeByTopologyNUMAPacked(tc.topo, tc.availableCPUs, tc.numCPUs)
			if tc.expErr != "" && err != nil && err.Error() != tc.expErr {
				t.Errorf("expected error to be [%v] but it was [%v]", tc.expErr, err)
			}
			if !result.Equals(tc.expResult) {
				t.Errorf("expected result [%s] to equal [%s]", result, tc.expResult)
			}
		})
	}
}

func mustParseCPUSet(t *testing.T, s string) cpuset.CPUSet {
	cpus, err := cpuset.Parse(s)
	if err != nil {
		t.Errorf("parsing %q: %v", s, err)
	}
	return cpus
}
