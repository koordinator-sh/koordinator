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

import (
	"fmt"
	"net"
)

var EnsureQosctlReady = ensureQosctlReadyFn

func ensureQosctlReadyFn(defaultIface *net.Interface) error {
	return fmt.Errorf("only support linux")
}

func HasBandwidthCtrlSetup(iface *net.Interface) bool {
	return false
}

func ResetBandwidthCtrl(iface *net.Interface) {
}

var SetupBandwidthCtrl = setupBandwidthCtrlFn

func setupBandwidthCtrlFn(cfg *BandwidthConfig) error {
	return fmt.Errorf("only support linux")
}

var GetHostDefaultIface = getHostDefaultIfaceFn

func getHostDefaultIfaceFn() *net.Interface {
	return &net.Interface{
		Name: "eth0",
	}
}

var GetIfaceSpeedMbit = getIfaceSpeedMbitFn

func getIfaceSpeedMbitFn(iface *net.Interface) uint64 {
	return 0
}
