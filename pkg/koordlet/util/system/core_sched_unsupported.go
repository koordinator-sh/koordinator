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

type CoreSched struct{}

func NewCoreSched() CoreSchedInterface {
	return &CoreSched{}
}

func NewCoreSchedExtended() CoreSchedExtendedInterface {
	return &CoreSched{}
}

func (c *CoreSched) Lock() {}

func (c *CoreSched) Unlock() {}

func (c *CoreSched) Get(pidType CoreSchedScopeType, pid uint32) (uint64, error) {
	return 0, fmt.Errorf("unsupported platform")
}

func (s *CoreSched) Create(pidType CoreSchedScopeType, pid uint32) error {
	return fmt.Errorf("unsupported platform")
}

func (s *CoreSched) ShareTo(pidType CoreSchedScopeType, pid uint32) error {
	return fmt.Errorf("unsupported platform")
}

func (s *CoreSched) ShareFrom(pidType CoreSchedScopeType, pid uint32) error {
	return fmt.Errorf("unsupported platform")
}

func (s *CoreSched) Clear(pidType CoreSchedScopeType, pid ...uint32) ([]uint32, error) {
	return nil, fmt.Errorf("unsupported platform")
}

func (s *CoreSched) Assign(pidTypeFrom CoreSchedScopeType, pidFrom uint32, pidTypeTo CoreSchedScopeType, pidsTo ...uint32) ([]uint32, error) {
	return nil, fmt.Errorf("unsupported platform")
}
