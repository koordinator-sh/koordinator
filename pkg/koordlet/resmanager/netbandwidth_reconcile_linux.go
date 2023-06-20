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

package resmanager

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

const (
	MAJOR_ID            = 1
	QDISC_MINOR_ID      = 0
	ROOT_CLASS_MINOR_ID = 1
	HIGH_CLASS_MINOR_ID = 2
	MID_CLASS_MINOR_ID  = 3
	LOW_CLASS_MINOR_ID  = 4

	// 0-7, In the round-robin process, classes with the lowest priority field are tried for packets first.
	HIGH_CLASS_PRIO = 0
	MID_CLASS_PRIO  = 1
	LOW_CLASS_PRIO  = 2

	// Maximum rate this class and all its children are guaranteed. Mandatory.
	// attention: the values below only represent the percentage of bandwidth can be used by different tc classes on the host,
	// the real values need to be calculated based on the physical network bandwidth.
	// eg: eth0: speed:200Mbit => high_clss.rate = 200Mbit * 40 / 100 = 80Mbit
	HIGH_CLASS_RATE_PERCENTAGE = 40
	MID_CLASS_RATE_PERCENTAGE  = 30
	LOW_CLASS_RATE_PERCENTAGE  = 30

	// Maximum rate at which a class can send, if its parent has bandwidth to spare.  Defaults to the configured rate,
	// which implies no borrowing
	CEIL_PERCENTAGE = 100

	DEFAULT_INTERFACE_NAME = "eth0"
)

var (
	rootClass = netlink.MakeHandle(MAJOR_ID, ROOT_CLASS_MINOR_ID)
	highClass = netlink.MakeHandle(MAJOR_ID, HIGH_CLASS_MINOR_ID)
	midClass  = netlink.MakeHandle(MAJOR_ID, MID_CLASS_MINOR_ID)
	lowClass  = netlink.MakeHandle(MAJOR_ID, LOW_CLASS_MINOR_ID)

	ipsets = []string{"high_class", "mid_class", "low_class"}
)

type NetQosManager struct {
	resmanager *resmanager

	// this is the physical NIC on host, default eth0
	interfName string
	interfID   int
	interfLink netlink.Link
	speed      uint64

	// for executing the iptables command.
	iptablesHandler *iptables.IPTables
	// for executing the tc and ipset command.
	netLinkHandler netlink.Handle
}

func NewNetQosManager(resmanager *resmanager) *NetQosManager {
	linkInfo, err := getLinkInfoByDefaultRoute()
	if err != nil || linkInfo == nil {
		klog.Errorf("failed to get link info by default route. err=%v\n", err)
		return nil
	}
	speedStr, err := getSpeed(linkInfo.Attrs().Name)
	if err != nil {
		klog.Errorf("failed to get speed by interface(%s). err=%v\n", linkInfo.Attrs().Name, err)
		return nil
	}

	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("failed to get iptables handler in those dir(%s). err=%v\n", os.Getenv("PATH"), err)
		return nil
	}

	n := NetQosManager{
		resmanager:      resmanager,
		interfName:      linkInfo.Attrs().Name,
		interfID:        linkInfo.Attrs().Index,
		interfLink:      linkInfo,
		speed:           uint64(speedStr) * 1000 * 1000,
		iptablesHandler: ipt,
		netLinkHandler:  netlink.Handle{},
	}

	return &n
}

func (n *NetQosManager) Init() error {
	return errors.NewAggregate([]error{
		n.EnsureQdisc(),
		n.EnsureClasses(),
		n.EnsureIpset(),
		n.EnsureIptables(),
	})
}

func (n *NetQosManager) CleanUp() error {
	return errors.NewAggregate([]error{
		n.DelQdisc(),
		n.DelIptables(),
		n.DestoryIpset(),
	})
}

func (n *NetQosManager) Reconcile() {
	if n == nil {
		klog.Errorf("qos manager is nil, ignore to create related rules")
		return
	}

	if err := n.Init(); err != nil {
		klog.Errorf("failed to init some necessary rules. err=%v", err)
		return
	}

	IPInK8S := make(map[string]sets.String)
	IPInIpset := make(map[string]sets.String)
	pods := n.resmanager.statesInformer.GetAllPods()
	for _, pod := range pods {
		netqos := extension.GetPodNetQoSClass(pod.Pod)
		if netqos == extension.NETQoSNone {
			continue
		}

		if IPInK8S[string(netqos)] == nil {
			IPInK8S[string(netqos)] = sets.NewString()
		}
		IPInK8S[string(netqos)].Insert(pod.Pod.Status.PodIP)
	}

	for _, setName := range ipsets {
		result, err := netlink.IpsetList(setName)
		if err != nil || result == nil {
			klog.Errorf("failed to get ipset.err=%v", err)
			continue
		}

		for _, entry := range result.Entries {
			if IPInIpset[setName] == nil {
				IPInIpset[setName] = sets.NewString()
			}
			IPInIpset[setName].Insert(entry.IP.String())
		}
	}

	for _, setName := range ipsets {
		for ip := range IPInK8S[setName].Difference(IPInIpset[setName]) {
			if err := netlink.IpsetAdd(setName, &netlink.IPSetEntry{IP: net.ParseIP(ip).To4()}); err != nil {
				klog.Warningf("failed to write ip %s to ipset(%s), err=%v", ip, setName, err)
			}
		}

		for ip := range IPInIpset[setName].Difference(IPInK8S[setName]) {
			if err := netlink.IpsetDel(setName, &netlink.IPSetEntry{IP: net.ParseIP(ip).To4()}); err != nil {
				klog.Warningf("failed to write ip %s to ipset(%s), err=%v", ip, setName, err)
			}
		}
	}
}

func getLinkInfoByDefaultRoute() (netlink.Link, error) {
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{}, netlink.RT_FILTER_DST)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("not find route info by dst ip=%s", net.IPv4zero.String())
	}

	linkInfo, err := netlink.LinkByIndex(routes[0].LinkIndex)
	if err != nil {
		return nil, err
	}

	return linkInfo, nil
}

// getSpeed get speed of ifName from /sys/class/net/$ifName/speed
func getSpeed(ifName string) (int, error) {
	file := path.Join("/sys/class/net/", ifName, "/speed")
	speedStr, err := ioutil.ReadFile(file)
	if err != nil {
		return 0, err
	}
	value := strings.Replace(string(speedStr), "\n", "", -1)
	return strconv.Atoi(value)
}

func (n *NetQosManager) EnsureQdisc() error {
	attrs := netlink.QdiscAttrs{
		LinkIndex: n.interfID,
		Handle:    netlink.MakeHandle(MAJOR_ID, QDISC_MINOR_ID),
		Parent:    netlink.HANDLE_ROOT,
	}
	htb := netlink.NewHtb(attrs)

	qdiscs, err := n.netLinkHandler.QdiscList(n.interfLink)
	if err != nil {
		return err
	}

	if len(qdiscs) == 1 && qdiscs[0].Type() == "htb" {
		if qdiscs[0].Attrs().Handle == htb.Handle {
			return nil
		}
		if err := netlink.QdiscDel(htb); err != nil {
			return fmt.Errorf("failed to delete old qidsc on %s, err=%v", n.interfName, err)
		}
	}

	return n.netLinkHandler.QdiscAdd(htb)
}

func (n *NetQosManager) DelQdisc() error {
	attrs := netlink.QdiscAttrs{
		LinkIndex: n.interfID,
		Handle:    netlink.MakeHandle(MAJOR_ID, QDISC_MINOR_ID),
		Parent:    netlink.HANDLE_ROOT,
	}
	htb := netlink.NewHtb(attrs)

	qdiscs, err := n.netLinkHandler.QdiscList(n.interfLink)
	if err != nil {
		return err
	}

	if len(qdiscs) == 1 && qdiscs[0].Type() == "htb" && qdiscs[0].Attrs().Handle == htb.Handle {
		if err := netlink.QdiscDel(htb); err != nil {
			return fmt.Errorf("failed to delete old qidsc on %s, err=%v", n.interfName, err)
		}
	}

	return nil
}

func (n *NetQosManager) EnsureClasses() error {
	link, err := netlink.LinkByIndex(n.interfID)
	if err != nil {
		return err
	}

	maxCeil := n.speed * CEIL_PERCENTAGE / 100
	// other leaf class
	highClassRate := n.speed * HIGH_CLASS_RATE_PERCENTAGE / 100
	midClassRate := n.speed * MID_CLASS_RATE_PERCENTAGE / 100
	lowClassRate := n.speed * LOW_CLASS_RATE_PERCENTAGE / 100

	return errors.NewAggregate([]error{
		n.ensureClass(link, newClass(n.interfID, netlink.HANDLE_ROOT, rootClass, maxCeil, maxCeil, 0)),
		n.ensureClass(link, newClass(n.interfID, rootClass, highClass, highClassRate, maxCeil, HIGH_CLASS_PRIO)),
		n.ensureClass(link, newClass(n.interfID, rootClass, midClass, midClassRate, maxCeil, MID_CLASS_PRIO)),
		n.ensureClass(link, newClass(n.interfID, rootClass, lowClass, lowClassRate, maxCeil, LOW_CLASS_PRIO)),
	})
}

func newClass(index int, parent, minor uint32, rate, ceil uint64, prio uint32) *netlink.HtbClass {
	attr := netlink.ClassAttrs{
		LinkIndex: index,
		Parent:    parent,
		Handle:    minor,
	}
	classAttr := netlink.HtbClassAttrs{
		Rate: rate,
		Ceil: ceil,
		Prio: prio,
	}
	htbClass := netlink.NewHtbClass(attr, classAttr)

	return htbClass
}

func (n *NetQosManager) ensureClass(nic netlink.Link, expect *netlink.HtbClass) error {
	classes, err := netlink.ClassList(nic, 0)
	if err != nil {
		return err
	}
	var existing *netlink.HtbClass
	for _, class := range classes {
		htbClass := class.(*netlink.HtbClass)
		if class.Type() == "htb" &&
			htbClass.Handle == expect.Handle &&
			htbClass.Parent == expect.Parent {
			existing = htbClass
			break
		}
	}
	if existing != nil {
		if expect.Rate == existing.Rate &&
			expect.Ceil == existing.Ceil &&
			expect.Prio == existing.Prio {
			return nil
		}
		if err := netlink.ClassChange(expect); err != nil {
			return fmt.Errorf("failed to change class from %v to %v on interface %s. err=: %v", existing, expect, n.interfName, err)
		}
		klog.Infof("succeed to changed htb class from %v to %v on interface %s.", existing, expect, n.interfName)
		return nil
	}
	if err := netlink.ClassAdd(expect); err != nil {
		return fmt.Errorf("failed to create htb class %v: %v on interface %s. err=%v", expect, err, n.interfName, err)
	}

	klog.Infof("succed to creat htb class: %v on interface %s", expect, n.interfName)
	return nil
}

func (n *NetQosManager) EnsureIpset() error {
	var errs []error
	for _, cur := range ipsets {
		result, err := netlink.IpsetList(cur)
		if err == nil && result != nil {
			continue
		}

		err = netlink.IpsetCreate(cur, "hash:ip", netlink.IpsetCreateOptions{})
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.NewAggregate(errs)
}

func (n *NetQosManager) DestoryIpset() error {
	var errs []error
	for _, cur := range ipsets {
		result, err := netlink.IpsetList(cur)
		if err == nil && result != nil {
			if err := netlink.IpsetDestroy(cur); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.NewAggregate(errs)
}

func (n *NetQosManager) EnsureIptables() error {
	if n.iptablesHandler == nil {
		return fmt.Errorf("can't create tc iptables rulues,because qos manager is nil")
	}
	ipsetToClassid := map[extension.NetQoSClass]string{
		extension.NETQoSHigh: "0001:0002",
		extension.NETQoSMid:  "0001:0003",
		extension.NETQoSLow:  "0001:0004",
	}

	for ipsetName, classid := range ipsetToClassid {
		exp := fmt.Sprintf("-A POSTROUTING -m set --match-set %s src -j CLASSIFY --set-class %s", string(ipsetName), classid)
		existed := false

		// looks like this one:
		// -A POSTROUTING -m set --match-set mid_class src -j CLASSIFY --set-class 0001:0003
		rules, err := n.iptablesHandler.List("mangle", "POSTROUTING")
		if err == nil {
			for _, rule := range rules {
				if rule == exp {
					existed = true
					break
				}
			}
		}

		if existed {
			continue
		}
		err = n.iptablesHandler.Append("mangle", "POSTROUTING",
			"-m", "set", "--match-set", string(ipsetName), "src",
			"-j", "CLASSIFY", "--set-class", classid)
		if err != nil {
			klog.Errorf("ipt append err=%v", err)
			return err
		}
	}

	return nil
}

func (n *NetQosManager) DelIptables() error {
	if n.iptablesHandler == nil {
		return fmt.Errorf("can't create tc iptables rulues,because qos manager is nil")
	}
	ipsetToClassid := map[extension.NetQoSClass]string{
		extension.NETQoSHigh: "0001:0002",
		extension.NETQoSMid:  "0001:0003",
		extension.NETQoSLow:  "0001:0004",
	}
	var errs []error

	for ipsetName, classid := range ipsetToClassid {
		exp := fmt.Sprintf("-A POSTROUTING -m set --match-set %s src -j CLASSIFY --set-class %s", string(ipsetName), classid)
		existed := false

		// looks like this one:
		// -A POSTROUTING -m set --match-set mid_class src -j CLASSIFY --set-class 0001:0003
		rules, err := n.iptablesHandler.List("mangle", "POSTROUTING")
		if err != nil || rules == nil {
			continue
		}
		for _, rule := range rules {
			if rule == exp {
				existed = true
				break
			}
		}

		if existed {
			err = n.iptablesHandler.Delete("mangle", "POSTROUTING",
				"-m", "set", "--match-set", string(ipsetName), "src",
				"-j", "CLASSIFY", "--set-class", classid)
			errs = append(errs, err)
		}
	}

	return errors.NewAggregate(errs)
}

func (n *NetQosManager) checkAllRulesExisted() (bool, error) {
	if _, err := n.QdiscExisted(); err != nil {
		return false, err
	}

	if _, err := n.classesExisted(); err != nil {
		return false, err
	}

	if _, err := n.ipsetExisted(); err != nil {
		return false, err
	}

	if _, err := n.iptablesExisted(); err != nil {
		return false, err
	}

	return true, nil
}

func (n *NetQosManager) QdiscExisted() (bool, error) {
	attrs := netlink.QdiscAttrs{
		LinkIndex: n.interfID,
		Handle:    netlink.MakeHandle(MAJOR_ID, QDISC_MINOR_ID),
		Parent:    netlink.HANDLE_ROOT,
	}
	htb := netlink.NewHtb(attrs)

	qdiscs, err := n.netLinkHandler.QdiscList(n.interfLink)
	if err != nil || qdiscs == nil {
		return false, err
	}

	if len(qdiscs) == 1 && qdiscs[0].Type() == "htb" &&
		qdiscs[0].Attrs().Handle == htb.Handle {
		return true, nil
	}

	return false, fmt.Errorf("qdisc not found")
}

func (n *NetQosManager) classesExisted() (bool, error) {
	link, err := netlink.LinkByIndex(n.interfID)
	if err != nil {
		return false, err
	}

	maxCeil := n.speed * CEIL_PERCENTAGE / 100
	// other leaf class
	highClassRate := n.speed * HIGH_CLASS_RATE_PERCENTAGE / 100
	midClassRate := n.speed * MID_CLASS_RATE_PERCENTAGE / 100
	lowClassRate := n.speed * LOW_CLASS_RATE_PERCENTAGE / 100

	errs := errors.NewAggregate([]error{
		n.classExisted(link, newClass(n.interfID, netlink.HANDLE_ROOT, rootClass, maxCeil, maxCeil, 0)),
		n.classExisted(link, newClass(n.interfID, rootClass, highClass, highClassRate, maxCeil, HIGH_CLASS_PRIO)),
		n.classExisted(link, newClass(n.interfID, rootClass, midClass, midClassRate, maxCeil, MID_CLASS_PRIO)),
		n.classExisted(link, newClass(n.interfID, rootClass, lowClass, lowClassRate, maxCeil, LOW_CLASS_PRIO)),
	})

	if errs != nil {
		return false, errs
	}

	return true, nil
}

func (n *NetQosManager) classExisted(nic netlink.Link, expect *netlink.HtbClass) error {
	classes, err := netlink.ClassList(nic, 0)
	if err != nil {
		return err
	}

	for _, class := range classes {
		htbClass := class.(*netlink.HtbClass)
		if class.Type() == "htb" &&
			htbClass.Handle == expect.Handle &&
			htbClass.Parent == expect.Parent {
			return nil
		}
	}

	return fmt.Errorf("class(classid:%d) not find", expect.Handle)
}

func (n *NetQosManager) iptablesExisted() (bool, error) {
	ipsetToClassid := map[extension.NetQoSClass]string{
		extension.NETQoSHigh: "0001:0002",
		extension.NETQoSMid:  "0001:0003",
		extension.NETQoSLow:  "0001:0004",
	}

	for ipsetName, classid := range ipsetToClassid {
		exp := fmt.Sprintf("-A POSTROUTING -m set --match-set %s src -j CLASSIFY --set-class %s", string(ipsetName), classid)
		existed := false

		// looks like this one:
		// -A POSTROUTING -m set --match-set mid_class src -j CLASSIFY --set-class 0001:0003
		rules, err := n.iptablesHandler.List("mangle", "POSTROUTING")
		if err == nil {
			for _, rule := range rules {
				if rule == exp {
					existed = true
					break
				}
			}
		}

		if !existed {
			return false, fmt.Errorf("iptables for matching ipset(%s) not found", ipsetName)
		}
	}

	return true, nil
}

func (n *NetQosManager) ipsetExisted() (bool, error) {
	var errs []error
	for _, cur := range ipsets {
		_, err := netlink.IpsetList(cur)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if errors.NewAggregate(errs) != nil {
		return false, errors.NewAggregate(errs)
	}

	return true, nil
}
