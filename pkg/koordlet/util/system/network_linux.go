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
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
)

var (
	QosctlKModsRegex = []*regexp.Regexp{
		regexp.MustCompile(`sch_sfq\s`),
		regexp.MustCompile(`sch_dsmark\s`),
		regexp.MustCompile(`sch_htb\s`),
	}
)

const (
	QosctlPackageName = "aqos"
	QosctlBinaryPath  = "/home/admin/aqos/qosctl"
)

var EnsureQosctlReady = ensureQosctlReadyFn

// TODO:
// - 'rpm' and 'lsmod' command not found
func ensureQosctlReadyFn(defaultIface *net.Interface) error {
	// if RPM is not installed, return false
	if _, _, err := ExecCmdOnHost([]string{"rpm", "-q", QosctlPackageName}); err != nil {
		return fmt.Errorf("qosctl.rpm not installed: %v", err)
	}

	// if KMods are loaded, return true
	// else try to install lazy-loading KMods
	out, _, err := ExecCmdOnHost([]string{"lsmod"})
	if err != nil {
		return err
	}
	if isKModLoaded := func(sysKMods string) bool {
		for _, reg := range QosctlKModsRegex {
			if !reg.MatchString(sysKMods) {
				klog.Warningf("Qosctl need KMod %s", reg.String())
				return false
			}
		}
		return true
	}(string(out)); isKModLoaded {
		return nil
	}
	klog.Infof("Qosctl KMod is lazy-loading, try to install it")

	if HasBandwidthCtrlSetup(defaultIface) {
		return fmt.Errorf("bandwidth control has already setup, but KMods are not loaded")
	}
	return nil
}

// for unit test
var (
	netLinkByIndexFn   = netlink.LinkByIndex
	netLinkQdiscListFn = netlink.QdiscList
	netLinkQdiscDelFn  = netlink.QdiscDel
	netLinkRouteListFn = netlink.RouteList
)

// htb * 1 + dsmark * 3 + sfq * 3
const BandwidthCtrlQdiscCount = 7

// TODO: 'aqos' add sub command to check itself setup
func HasBandwidthCtrlSetup(iface *net.Interface) bool {
	ifLink, err := netLinkByIndexFn(iface.Index)
	if err != nil {
		klog.Errorf("iface(%s) not exist: %v", iface.Name, err)
		return false
	}
	qdiscs, err := netLinkQdiscListFn(ifLink)
	if err != nil {
		klog.Errorf("qdisc of iface(%s) not exist: %v", iface.Name, err)
		return false
	}
	return len(qdiscs) == BandwidthCtrlQdiscCount
}

func ResetBandwidthCtrl(iface *net.Interface) {
	defaultLink, err := netLinkByIndexFn(iface.Index)
	if err != nil {
		klog.Errorf("iface(%s) not exist: %v", iface.Name, err)
		return
	}
	qdiscs, err := netLinkQdiscListFn(defaultLink)
	if err != nil {
		klog.Errorf("qdisc of iface(%s) not exist: %v", iface.Name, err)
		return
	}
	for _, qdisc := range qdiscs {
		if qdisc.Attrs().Parent == netlink.HANDLE_ROOT {
			if err := netLinkQdiscDelFn(qdisc); err != nil {
				klog.Errorf("del root qdisc of iface(%s) failed: %v", iface.Name, err)
			}
			break
		}
	}
}

var SetupBandwidthCtrl = setupBandwidthCtrlFn

func setupBandwidthCtrlFn(cfg *BandwidthConfig) error {
	_, _, err := ExecCmdOnHost([]string{
		QosctlBinaryPath, "init", "--dev", cfg.GatewayIfaceName,
		"--dmb", fmt.Sprintf("%d", cfg.GatewayLimit), "--fmb", fmt.Sprintf("%d", cfg.RootLimit),
		"--cb", fmt.Sprintf("%d,%d,%d", cfg.GoldRequest, cfg.SilverRequest, cfg.CopperRequest),
		"--cc", fmt.Sprintf("%d,%d,%d", cfg.GoldLimit, cfg.SilverLimit, cfg.CopperLimit),
		"--cd", fmt.Sprintf("%d,%d,%d", cfg.GoldDSCP, cfg.SilverDSCP, cfg.CopperDSCP),
	})
	return err
}

var GetHostDefaultIface = getHostDefaultIfaceFn

// find NIC in default route.
// MUST executed in host network namespace
func getHostDefaultIfaceFn() *net.Interface {
	routes, err := netLinkRouteListFn(nil, syscall.AF_INET)
	if err != nil {
		klog.Errorf("Command('ip route show') failed: %v", err)
		return nil
	}

	for _, route := range routes {
		if route.Dst == nil || route.Dst.String() == "0.0.0.0/0" {
			if route.LinkIndex <= 0 {
				klog.Errorf("Find default route, but interface index <= 0")
				return nil
			}
			if iface, err := net.InterfaceByIndex(route.LinkIndex); err != nil {
				klog.Errorf("Find default route, but interface index is invalid: %v", err)
				return nil
			} else {
				return iface
			}
		}
	}
	return nil
}

const (
	// 1000 Mbit/s
	DefaultIfaceBandwidth = 1000
)

var GetIfaceSpeedMbit = getIfaceSpeedMbitFn

// /sys/class/net/${iface}/speed = xxx Mbit/s.
// Kernel version >= v2.6.x
func getIfaceSpeedMbitFn(iface *net.Interface) uint64 {
	speedCmds := []string{"cat", path.Join(Conf.SysRootDir, "/class/net", iface.Name, "/speed")}
	speedOut, _, err := ExecCmdOnHost(speedCmds)
	if err != nil {
		klog.Errorf("Command('%s') failed: %v", strings.Join(speedCmds, " "), err)
		return DefaultIfaceBandwidth
	}
	speedStr := strings.TrimSpace(string(speedOut))
	speed, err := strconv.ParseInt(speedStr, 10, 64)
	if err != nil || speed < 0 {
		klog.Errorf("Invalid speed(%s): %v", speedStr, err)
		return DefaultIfaceBandwidth
	}
	return uint64(speed)
}
