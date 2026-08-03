//go:build !linux
// +build !linux

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

import "fmt"

// OOMScoreAdj is a stub for unsupported platforms.
type OOMScoreAdj struct{}

// NewOOMScoreAdj returns a stub that always returns an error on unsupported platforms.
func NewOOMScoreAdj() OOMScoreAdjInterface {
	return &OOMScoreAdj{}
}

func (o *OOMScoreAdj) Get(pid uint32) (int64, error) {
	return 0, fmt.Errorf("unsupported platform")
}

func (o *OOMScoreAdj) Set(pid uint32, val int64) error {
	return fmt.Errorf("unsupported platform")
}
