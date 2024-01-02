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

package tc

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

const (
	TCReconcileName = "TCReconcile"

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

var _ framework.QOSStrategy = &TCManager{}

type TCManager struct {
	reconcileInterval time.Duration
	statesInformer    statesinformer.StatesInformer
	metricCache       metriccache.MetricCache
	executor          resourceexecutor.ResourceUpdateExecutor

	// this is the physical NIC on host, default eth0
	interfLink netlink.Link
	speed      uint64

	// for executing the iptables command.
	iptablesHandler *iptables.IPTables
	// for executing the tc and ipset command.
	netLinkHandler netlink.Handle
}

func New(opt *framework.Options) framework.QOSStrategy {
	klog.Info("start to init net qos manager")
	linkInfo, err := system.GetLinkInfoByDefaultRoute()
	if err != nil || linkInfo == nil {
		klog.Errorf("failed to get link info by default route. err=%v\n", err)
		return nil
	}
	speedStr, err := system.GetSpeed(linkInfo.Attrs().Name)
	if err != nil {
		klog.Errorf("failed to get speed by interface(%s). err=%v\n", linkInfo.Attrs().Name, err)
		return nil
	}

	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("failed to get iptables handler in those dir(%s). err=%v\n", os.Getenv("PATH"), err)
		return nil
	}

	n := TCManager{
		reconcileInterval: time.Duration(opt.Config.ReconcileIntervalSeconds) * time.Second,
		statesInformer:    opt.StatesInformer,
		metricCache:       opt.MetricCache,
		executor:          resourceexecutor.NewResourceUpdateExecutor(),
		interfLink:        linkInfo,
		speed:             uint64(speedStr * 1000 * 1000),
		iptablesHandler:   ipt,
		netLinkHandler:    netlink.Handle{},
	}

	return &n
}

func (p *TCManager) Enabled() bool {
	return features.DefaultKoordletFeatureGate.Enabled(features.NetQOSByTC) && p.reconcileInterval > 0
}

func (p *TCManager) Setup(*framework.Context) {

}

func (p *TCManager) Run(stopCh <-chan struct{}) {
	p.init(stopCh)
	go wait.Until(p.Reconcile, p.reconcileInterval, stopCh)
}

func (p *TCManager) init(stopCh <-chan struct{}) {
	p.executor.Run(stopCh)
}

func (p *TCManager) InitRatledRules() error {
	return errors.NewAggregate([]error{
		p.EnsureQdisc(),
		p.EnsureClasses(),
		p.EnsureIpset(),
		p.EnsureIptables(),
	})
}

func (p *TCManager) CleanUp() error {
	return errors.NewAggregate([]error{
		p.DelQdisc(),
		p.DelIptables(),
		p.DestoryIpset(),
	})
}

func (p *TCManager) Reconcile() {
	klog.Info("start to reconcile some linux rules for net qos")
	if p == nil {
		klog.Errorf("qos manager is nil, ignore to create related rules")
		return
	}

	if err := p.InitRatledRules(); err != nil {
		klog.Errorf("failed to init some necessary rules. err=%v", err)
		return
	}
	IPInK8S := make(map[string]sets.String)
	IPInIpset := make(map[string]sets.String)
	pods := p.statesInformer.GetAllPods()
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

func (p *TCManager) EnsureQdisc() error {
	klog.Infoln("start to create qdisc for default net interface")
	attrs := netlink.QdiscAttrs{
		LinkIndex: p.interfLink.Attrs().Index,
		Handle:    netlink.MakeHandle(MAJOR_ID, QDISC_MINOR_ID),
		Parent:    netlink.HANDLE_ROOT,
	}
	htb := netlink.NewHtb(attrs)

	qdiscs, err := p.netLinkHandler.QdiscList(p.interfLink)
	if err != nil {
		return fmt.Errorf("failed to get qdisc. err=%v", err)
	}

	for _, qdisc := range qdiscs {
		if qdisc.Type() != "htb" {
			continue
		}
		if qdisc.Attrs().Handle == htb.Handle {
			return nil
		}
		if err := netlink.QdiscDel(htb); err != nil {
			return fmt.Errorf("failed to delete old qidsc on %s, err=%v", p.interfLink.Attrs().Name, err)
		}
	}

	return p.netLinkHandler.QdiscAdd(htb)
}

func (p *TCManager) DelQdisc() error {
	attrs := netlink.QdiscAttrs{
		LinkIndex: p.interfLink.Attrs().Index,
		Handle:    netlink.MakeHandle(MAJOR_ID, QDISC_MINOR_ID),
		Parent:    netlink.HANDLE_ROOT,
	}
	htb := netlink.NewHtb(attrs)

	qdiscs, err := p.netLinkHandler.QdiscList(p.interfLink)
	if err != nil {
		return err
	}

	for _, qdisc := range qdiscs {
		if qdisc.Type() == "htb" && qdisc.Attrs().Handle == htb.Handle {
			if err := netlink.QdiscDel(htb); err != nil {
				return fmt.Errorf("failed to delete old qidsc on %s, err=%v", p.interfLink.Attrs().Name, err)
			}
		}
	}

	return nil
}

func loadConfigFromFile(file string) (*extension.NetQosGlobalConfig, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	cfg := extension.NetQosGlobalConfig{}
	err = json.Unmarshal(data, &cfg)

	return &cfg, err
}

func (p *TCManager) EnsureClasses() error {
	cfg, err := loadConfigFromFile(extension.NETQOSConfigPathForNode)
	if err != nil {
		return err
	}

	return errors.NewAggregate([]error{
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, netlink.HANDLE_ROOT, rootClass, cfg.HwTxBpsMax, cfg.HwTxBpsMax, 0)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, highClass, cfg.HwTxBpsMax-cfg.L1TxBpsMin-cfg.L2TxBpsMin, cfg.HwTxBpsMax, HIGH_CLASS_PRIO)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, midClass, cfg.L1TxBpsMin, cfg.L1TxBpsMax, MID_CLASS_PRIO)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, lowClass, cfg.L2TxBpsMin, cfg.L2TxBpsMax, LOW_CLASS_PRIO)),
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
	htbClass := NewHtbClass(attr, classAttr)
	if htbClass.Cbuffer < 200 {
		htbClass.Cbuffer = 200
	}
	if htbClass.Buffer < 200 {
		htbClass.Buffer = 200
	}

	return htbClass
}

// NewHtbClass NOTE: function is in here because it uses other linux functions
func NewHtbClass(attrs netlink.ClassAttrs, cattrs netlink.HtbClassAttrs) *netlink.HtbClass {
	mtu := 1600
	rate := cattrs.Rate / 8
	ceil := cattrs.Ceil / 8
	buffer := cattrs.Buffer
	cbuffer := cattrs.Cbuffer

	if ceil == 0 {
		ceil = rate
	}

	if buffer == 0 {
		buffer = uint32(float64(rate)/netlink.Hz() + float64(mtu))
		klog.V(2).Infof("buffer[%v]=rate[%v]/hz[%v]+mtu[%v]\n", buffer, rate, netlink.Hz(), mtu)
	}
	dstBuffer := netlink.Xmittime(rate, buffer)
	klog.V(2).Infof("buffer[%v]=(1000000*(srcBuffer[%v]/rate[%v]))/tick[%v]\n", dstBuffer, buffer, rate, netlink.TickInUsec())

	if cbuffer == 0 {
		cbuffer = uint32(float64(ceil)/netlink.Hz() + float64(mtu))
		klog.V(2).Infof("cbuffer[%v]=ceil[%v]/hz[%v]+mtu[%v]\n", cbuffer, ceil, netlink.Hz(), mtu)
	}
	dstCbuffer := netlink.Xmittime(ceil, cbuffer)
	klog.V(2).Infof("cbuffer[%v]=(1000000*(srcCbuffer[%v]/ceil[%v]))/tick[%v]", dstCbuffer, cbuffer, ceil, netlink.TickInUsec())

	return &netlink.HtbClass{
		ClassAttrs: attrs,
		Rate:       rate,
		Ceil:       ceil,
		Buffer:     buffer,
		Cbuffer:    cbuffer,
		Level:      0,
		Prio:       cattrs.Prio,
		Quantum:    cattrs.Quantum,
	}
}

func (p *TCManager) ensureClass(nic netlink.Link, expect *netlink.HtbClass) error {
	classes, err := netlink.ClassList(nic, 0)
	if err != nil {
		return fmt.Errorf("failed to get tc class. err=%v", err)
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
			expect.Prio == existing.Prio &&
			expect.Buffer == existing.Buffer &&
			expect.Cbuffer == existing.Cbuffer {
			return nil
		}
		if err := netlink.ClassChange(expect); err != nil {
			return fmt.Errorf("failed to change class from %v to %v on interface %s. err=: %v", existing, expect, p.interfLink.Attrs().Name, err)
		}
		klog.Infof("succeed to changed htb class from %v to %v on interface %s.", existing, expect, p.interfLink.Attrs().Name)
		return nil
	}
	if err := netlink.ClassAdd(expect); err != nil {
		return fmt.Errorf("failed to create htb class %v: %v on interface %s. err=%v", expect, err, p.interfLink.Attrs().Name, err)
	}

	klog.V(2).Infof("succed to creat htb class: %v on interface %s\n", expect, p.interfLink.Attrs().Name)
	return nil
}

func (p *TCManager) EnsureIpset() error {
	var errs []error
	for _, cur := range ipsets {
		result, err := netlink.IpsetList(cur)
		if err == nil && result != nil {
			continue
		}

		err = netlink.IpsetCreate(cur, "hash:ip", netlink.IpsetCreateOptions{})
		if err != nil {
			err = fmt.Errorf("failed to create ipset. err=%v", err)
			errs = append(errs, err)
		}
	}

	return errors.NewAggregate(errs)
}

func (p *TCManager) DestoryIpset() error {
	var errs []error
	for _, cur := range ipsets {
		result, err := netlink.IpsetList(cur)
		if err == nil && result != nil {
			if err := netlink.IpsetDestroy(cur); err != nil {
				err = fmt.Errorf("failed to destroy ipset. err=%v", err)
				errs = append(errs, err)
			}
		}
	}

	return errors.NewAggregate(errs)
}

func (p *TCManager) EnsureIptables() error {
	if p.iptablesHandler == nil {
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
		rules, err := p.iptablesHandler.List("mangle", "POSTROUTING")
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
		err = p.iptablesHandler.Append("mangle", "POSTROUTING",
			"-m", "set", "--match-set", string(ipsetName), "src",
			"-j", "CLASSIFY", "--set-class", classid)
		if err != nil {
			klog.Errorf("ipt append err=%v", err)
			return err
		}
	}

	return nil
}

func (p *TCManager) DelIptables() error {
	if p.iptablesHandler == nil {
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
		rules, err := p.iptablesHandler.List("mangle", "POSTROUTING")
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
			err = p.iptablesHandler.Delete("mangle", "POSTROUTING",
				"-m", "set", "--match-set", string(ipsetName), "src",
				"-j", "CLASSIFY", "--set-class", classid)
			errs = append(errs, err)
		}
	}

	return errors.NewAggregate(errs)
}

func (p *TCManager) checkAllRulesExisted() (bool, error) {
	if _, err := p.QdiscExisted(); err != nil {
		return false, err
	}

	if _, err := p.classesExisted(); err != nil {
		return false, err
	}

	if _, err := p.ipsetExisted(); err != nil {
		return false, err
	}

	if _, err := p.iptablesExisted(); err != nil {
		return false, err
	}

	return true, nil
}

func (p *TCManager) QdiscExisted() (bool, error) {
	attrs := netlink.QdiscAttrs{
		LinkIndex: p.interfLink.Attrs().Index,
		Handle:    netlink.MakeHandle(MAJOR_ID, QDISC_MINOR_ID),
		Parent:    netlink.HANDLE_ROOT,
	}
	htb := netlink.NewHtb(attrs)

	qdiscs, err := p.netLinkHandler.QdiscList(p.interfLink)
	if err != nil || qdiscs == nil {
		return false, err
	}

	if len(qdiscs) == 1 && qdiscs[0].Type() == "htb" &&
		qdiscs[0].Attrs().Handle == htb.Handle {
		return true, nil
	}

	return false, fmt.Errorf("qdisc not found")
}

func (p *TCManager) classesExisted() (bool, error) {
	link, err := netlink.LinkByIndex(p.interfLink.Attrs().Index)
	if err != nil {
		return false, err
	}

	maxCeil := p.speed * CEIL_PERCENTAGE / 100
	// other leaf class
	highClassRate := p.speed * HIGH_CLASS_RATE_PERCENTAGE / 100
	midClassRate := p.speed * MID_CLASS_RATE_PERCENTAGE / 100
	lowClassRate := p.speed * LOW_CLASS_RATE_PERCENTAGE / 100

	errs := errors.NewAggregate([]error{
		p.classExisted(link, newClass(p.interfLink.Attrs().Index, netlink.HANDLE_ROOT, rootClass, maxCeil, maxCeil, 0)),
		p.classExisted(link, newClass(p.interfLink.Attrs().Index, rootClass, highClass, highClassRate, maxCeil, HIGH_CLASS_PRIO)),
		p.classExisted(link, newClass(p.interfLink.Attrs().Index, rootClass, midClass, midClassRate, maxCeil, MID_CLASS_PRIO)),
		p.classExisted(link, newClass(p.interfLink.Attrs().Index, rootClass, lowClass, lowClassRate, maxCeil, LOW_CLASS_PRIO)),
	})

	if errs != nil {
		return false, errs
	}

	return true, nil
}

func (p *TCManager) classExisted(nic netlink.Link, expect *netlink.HtbClass) error {
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

func (p *TCManager) iptablesExisted() (bool, error) {
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
		rules, err := p.iptablesHandler.List("mangle", "POSTROUTING")
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

func (p *TCManager) ipsetExisted() (bool, error) {
	var errs []error
	for _, cur := range ipsets {
		if _, err := netlink.IpsetList(cur); err != nil {
			errs = append(errs, err)
		}
	}

	if errors.NewAggregate(errs) != nil {
		return false, errors.NewAggregate(errs)
	}

	return true, nil
}
