//go:build !linux
// +build !linux

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
