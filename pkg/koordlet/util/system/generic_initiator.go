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

package system

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

/*
https://lkml.org/lkml/2020/9/7/597

Generic Initiators are a new ACPI concept that allows for the
description of proximity domains that contain a device which
performs memory access (such as a network card) but neither
host CPU nor Memory.

This file provides some GI acquisition and parsing logic.
*/

const (
	HasGenericInitiatorPath = "has_generic_initiator"
)

func GetHasGenericInitiatorPath() string {
	return filepath.Join(Conf.SysRootDir, SysNUMASubDir, HasGenericInitiatorPath)
}

func GetNUMANodesHasGI() sets.Set[int32] {
	path := GetHasGenericInitiatorPath()
	rawNUMANodes, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	ids, err := parseIDs(string(rawNUMANodes))
	if err != nil {
		return nil
	}
	return ids
}

func parseIDs(s string) (sets.Set[int32], error) {
	idSets := sets.New[int32]()

	// Handle empty string.
	if s == "" {
		return nil, nil
	}

	// Split CPU list string:
	// "0-5,34,46-48 => ["0-5", "34", "46-48"]
	ranges := strings.Split(s, ",")

	for _, r := range ranges {
		boundaries := strings.Split(r, "-")
		if len(boundaries) == 1 {
			// Handle ranges that consist of only one element like "34".
			elem, err := strconv.ParseInt(boundaries[0], 10, 32) // assert cpu id is in range of int32
			if err != nil {
				return nil, err
			}
			idSets.Insert(int32(elem))
		} else if len(boundaries) == 2 {
			// Handle multi-element ranges like "0-5".
			start, err := strconv.ParseInt(boundaries[0], 10, 32) // assert cpu id is in range of int32
			if err != nil {
				return nil, err
			}
			end, err := strconv.ParseInt(boundaries[1], 10, 32)
			if err != nil {
				return nil, err
			}
			// Add all elements to the result.
			// e.g. "0-5", "46-48" => [0, 1, 2, 3, 4, 5, 46, 47, 48].
			for e := start; e <= end; e++ {
				idSets.Insert(int32(e))
			}
		} else {
			return nil, fmt.Errorf("invalid format: %s", r)
		}
	}
	return idSets, nil
}
