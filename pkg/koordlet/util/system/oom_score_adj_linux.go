//go:build linux
// +build linux

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
	"strconv"
	"strings"
)

// OOMScoreAdj operates on /proc/<pid>/oom_score_adj.
type OOMScoreAdj struct{}

// NewOOMScoreAdj creates a new OOMScoreAdj that reads/writes the real proc file.
func NewOOMScoreAdj() OOMScoreAdjInterface {
	return &OOMScoreAdj{}
}

func (o *OOMScoreAdj) Get(pid uint32) (int64, error) {
	content, err := os.ReadFile(GetProcPIDOOMScoreAdjPath(pid))
	if err != nil {
		return 0, err
	}
	val, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse oom_score_adj for pid %d: %w", pid, err)
	}
	return val, nil
}

func (o *OOMScoreAdj) Set(pid uint32, val int64) error {
	if val < OOMScoreAdjMin || val > OOMScoreAdjMax {
		return fmt.Errorf("oom_score_adj value %d out of range [%d, %d] for pid %d", val, OOMScoreAdjMin, OOMScoreAdjMax, pid)
	}
	return os.WriteFile(GetProcPIDOOMScoreAdjPath(pid), []byte(strconv.FormatInt(val, 10)), 0640)
}
