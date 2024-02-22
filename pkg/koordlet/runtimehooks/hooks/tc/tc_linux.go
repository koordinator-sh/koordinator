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
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"go.uber.org/atomic"
	apierror "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/reconciler"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	name        = "tcPlugin"
	description = "setup tc rules for node"

	ruleNameForNodeSLO = name + " (nodeSLO)"
	ruleNameForAllPods = name + " (allPods)"
)

const (
	MAJOR_ID              = 1
	QDISC_MINOR_ID        = 0
	ROOT_CLASS_MINOR_ID   = 1
	SYSTEM_CLASS_MINOR_ID = 2
	LS_CLASS_MINOR_ID     = 3
	BE_CLASS_MINOR_ID     = 4

	// 0-7, In the round-robin process, classes with the lowest priority field are tried for packets first.
	SYSTEM_CLASS_PRIO = 0
	LS_CLASS_PRIO     = 1
	BE_CLASS_PRIO     = 2

	// Maximum rate this class and all its children are guaranteed. Mandatory.
	// attention: the values below only represent the percentage of bandwidth can be used by different tc classes on the host,
	// the real values need to be calculated based on the physical network bandwidth.
	// eg: eth0: speed:200Mbit => high_clss.rate = 200Mbit * 40 / 100 = 80Mbit
	BE_CLASS_RATE_PERCENTAGE     = 30
	LS_CLASS_RATE_PERCENTAGE     = 30
	SYSTEM_CLASS_RATE_PERCENTAGE = 40

	// Maximum rate at which a class can send, if its parent has bandwidth to spare.  Defaults to the configured rate,
	// which implies no borrowing
	CEIL_PERCENTAGE = 100

	DEFAULT_INTERFACE_NAME = "eth0"
)

var (
	rootClass   = netlink.MakeHandle(MAJOR_ID, ROOT_CLASS_MINOR_ID)
	systemClass = netlink.MakeHandle(MAJOR_ID, SYSTEM_CLASS_MINOR_ID)
	lsClass     = netlink.MakeHandle(MAJOR_ID, LS_CLASS_MINOR_ID)
	beClass     = netlink.MakeHandle(MAJOR_ID, BE_CLASS_MINOR_ID)

	ipsets = []string{string(NETQoSSystem), string(NETQoSLS), string(NETQoSBE)}
)

type tcPlugin struct {
	rule *tcRule

	initialized     *atomic.Bool // whether the cache has been initialized
	allPodsSyncOnce sync.Once    // sync once for AllPods

	// this is the physical NIC on host, default eth0
	interfLink netlink.Link

	// for executing the iptables command.
	iptablesHandler *iptables.IPTables
	// for executing the tc and ipset command.
	netLinkHandler netlink.Handle

	executor resourceexecutor.ResourceUpdateExecutor
}

var singleton *tcPlugin

func Object() *tcPlugin {
	if singleton == nil {
		singleton = newPlugin()
	}
	return singleton
}

func newPlugin() *tcPlugin {
	return &tcPlugin{
		initialized:     atomic.NewBool(false),
		netLinkHandler:  netlink.Handle{},
		allPodsSyncOnce: sync.Once{},
		rule:            newRule(),
	}
}

func (p *tcPlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", name)

	rule.Register(ruleNameForNodeSLO, description,
		rule.WithParseFunc(statesinformer.RegisterTypeNodeSLOSpec, p.parseRuleForNodeSLO),
		rule.WithUpdateCallback(p.ruleUpdateCb),
	)

	rule.Register(ruleNameForAllPods, description,
		rule.WithParseFunc(statesinformer.RegisterTypeAllPods, p.parseForAllPods),
		rule.WithUpdateCallback(p.ruleUpdateCb))

	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, sysutil.NetClsClassId, description+" (pod net class id)",
		p.SetPodNetCls, reconciler.PodHostNetworkFilter(), "true")

	p.executor = op.Executor
}

func (p *tcPlugin) SetPodNetCls(proto protocol.HooksProtocol) error {
	podCtx := proto.(*protocol.PodContext)
	if podCtx == nil {
		return fmt.Errorf("pod protocol is nil for plugin %v", name)
	}

	netQos := NETQoSNone
	if podCtx.Request.Labels != nil {
		netQos = GetNetQoSClassByAttrs(podCtx.Request.Labels, podCtx.Request.Annotations)
	}
	classId := GetClassIdByNetQos(netQos)
	podCtx.Response.Resources.NetClsClassId = pointer.String(classId)

	return nil
}

func (p *tcPlugin) parseRuleForNodeSLO(mergedNodeSLOIf interface{}) (bool, error) {
	mergedNodeSLO := mergedNodeSLOIf.(*slov1alpha1.NodeSLOSpec)
	if mergedNodeSLO == nil {
		return false, nil
	}
	qosStrategy := mergedNodeSLO.ResourceQOSStrategy

	// default policy enables
	isNETQOSPolicyTC := qosStrategy == nil || qosStrategy.Policies == nil || qosStrategy.Policies.NETQOSPolicy == nil ||
		*qosStrategy.Policies.NETQOSPolicy == slov1alpha1.NETQOSPolicyTC

	if isNETQOSPolicyTC {
		if mergedNodeSLO.SystemStrategy == nil {
			return false, nil
		}
		p.rule.enable = true
		p.rule.speed = uint64(mergedNodeSLO.SystemStrategy.TotalNetworkBandwidth.Value())
		p.rule.netCfg = loadConfigFromNodeSlo(mergedNodeSLO)
	} else {
		p.rule.enable = false
	}

	return true, nil
}

func (p *tcPlugin) parseForAllPods(e interface{}) (bool, error) {
	_, ok := e.(*struct{})
	if !ok {
		return false, fmt.Errorf("invalid rule type %T", e)
	}

	needSync := false
	p.allPodsSyncOnce.Do(func() {
		needSync = true
		klog.V(5).Infof("plugin %s callback the first all pods update", name)
	})
	return needSync, nil
}

func (p *tcPlugin) prepare() error {
	linkInfo, err := system.GetLinkInfoByDefaultRoute()
	if err != nil {
		klog.Errorf("failed to get link info by default route. err=%v\n", err)
		return err
	}
	if linkInfo == nil {
		klog.Errorf("link info is nil")
		return errors.New("link info is nil")
	}

	p.interfLink = linkInfo

	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("failed to get iptables handler in those dir(%s). err=%v\n", os.Getenv("PATH"), err)
		return err
	}
	p.iptablesHandler = ipt
	p.initialized = atomic.NewBool(true)

	if err := p.InitRelatedRules(); err != nil {
		klog.Errorf("failed to init some necessary rules. err=%v", err)
		return err
	}

	return nil
}

func (p *tcPlugin) ruleUpdateCb(target *statesinformer.CallbackTarget) error {
	if !p.initialized.Load() {
		if err := p.prepare(); err != nil {
			return err
		}
	}

	if !p.rule.IsEnabled() {
		klog.V(5).Infof("tc plugin is not enabled, ready to cleanup related rules.")
		return p.CleanUp()
	}

	if target == nil {
		return errors.New("callback target is nil")
	}

	podMetas := target.Pods
	if len(podMetas) <= 0 {
		klog.V(5).Infof("plugin %s skipped for rule update, no pod passed from callback", name)
		return nil
	}

	return p.refreshForAllPods(podMetas)
}

func (p *tcPlugin) refreshForAllPods(pods []*statesinformer.PodMeta) error {
	ipInK8S := make(map[string]sets.String)
	ipInIpset := make(map[string]sets.String)

	for _, pod := range pods {
		if pod.Pod.Spec.HostNetwork {
			// pod in host network namespace, network bandwidth can be limited by writing to net_cls.classid.
			continue
		}

		netqos := GetPodNetQoSClass(pod.Pod)
		if netqos == NETQoSNone {
			continue
		}

		if ipInK8S[string(netqos)] == nil {
			ipInK8S[string(netqos)] = sets.NewString()
		}
		ipInK8S[string(netqos)].Insert(pod.Pod.Status.PodIP)
	}

	for _, setName := range ipsets {
		result, err := netlink.IpsetList(setName)
		if err != nil || result == nil {
			klog.Errorf("failed to get ipset.err=%v", err)
			continue
		}

		for _, entry := range result.Entries {
			if ipInIpset[setName] == nil {
				ipInIpset[setName] = sets.NewString()
			}
			ipInIpset[setName].Insert(entry.IP.String())
		}
	}

	for _, setName := range ipsets {
		for ip := range ipInK8S[setName].Difference(ipInIpset[setName]) {
			klog.V(5).Infof("ready to add %s to ipset %s.\n", ip, setName)
			if err := netlink.IpsetAdd(setName, &netlink.IPSetEntry{IP: net.ParseIP(ip).To4()}); err != nil {
				klog.Warningf("failed to write ip %s to ipset %s, err=%v", ip, setName, err)
				return err
			}
		}

		for ip := range ipInIpset[setName].Difference(ipInK8S[setName]) {
			klog.V(5).Infof("ready to del ip %s from ipset %s.\n", ip, setName)
			if err := netlink.IpsetDel(setName, &netlink.IPSetEntry{IP: net.ParseIP(ip).To4()}); err != nil {
				klog.Warningf("failed to write ip %s to ipset %s, err=%v", ip, setName, err)
				return err
			}
		}
	}

	return nil
}

func (p *tcPlugin) InitRelatedRules() error {
	return apierror.NewAggregate([]error{
		p.EnsureQdisc(),
		p.EnsureClasses(),
		p.EnsureIpset(),
		p.EnsureIptables(),
	})
}

func (p *tcPlugin) CleanUp() error {
	return apierror.NewAggregate([]error{
		p.DelQdisc(),
		p.DelIptables(),
		p.DestoryIpset(),
	})
}

func (p *tcPlugin) EnsureQdisc() error {
	klog.V(5).Infoln("start to create qdisc for default net interface")
	attrs := netlink.QdiscAttrs{
		LinkIndex: p.interfLink.Attrs().Index,
		Handle:    netlink.MakeHandle(MAJOR_ID, QDISC_MINOR_ID),
		Parent:    netlink.HANDLE_ROOT,
	}
	htb := netlink.NewHtb(attrs)
	htb.Defcls = SYSTEM_CLASS_MINOR_ID

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

func (p *tcPlugin) DelQdisc() error {
	klog.V(5).Infof("start to delete qdisc crated by tc plugin.")
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

func (p *tcPlugin) EnsureClasses() error {
	klog.V(5).Infof("start to create tc class rules.")
	cfg := p.rule.GetNetCfg()
	if cfg == nil {
		return errors.New("net config is nil")
	}

	return apierror.NewAggregate([]error{
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, netlink.HANDLE_ROOT, rootClass, cfg.HwTxBpsMax, cfg.HwTxBpsMax, 0)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, systemClass, cfg.HwTxBpsMax-cfg.L1TxBpsMin-cfg.L2TxBpsMin, cfg.HwTxBpsMax, SYSTEM_CLASS_PRIO)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, lsClass, cfg.L1TxBpsMin, cfg.L1TxBpsMax, LS_CLASS_PRIO)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, beClass, cfg.L2TxBpsMin, cfg.L2TxBpsMax, BE_CLASS_PRIO)),
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

func (p *tcPlugin) ensureClass(nic netlink.Link, expect *netlink.HtbClass) error {
	classes, err := netlink.ClassList(nic, 0)
	if err != nil {
		return fmt.Errorf("failed to get tc class. err=%v", err)
	}
	var existing *netlink.HtbClass
	for _, class := range classes {
		switch class.(type) {
		case *netlink.HtbClass:
			htbClass := class.(*netlink.HtbClass)
			if htbClass != nil &&
				htbClass.Handle == expect.Handle &&
				htbClass.Parent == expect.Parent {
				existing = htbClass
				break
			}
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

func (p *tcPlugin) checkAllRulesExisted() (bool, error) {
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

func (p *tcPlugin) QdiscExisted() (bool, error) {
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

func (p *tcPlugin) classesExisted() (bool, error) {
	link, err := netlink.LinkByIndex(p.interfLink.Attrs().Index)
	if err != nil {
		return false, err
	}

	maxCeil := p.rule.speed * CEIL_PERCENTAGE / 100
	// other leaf class
	highClassRate := p.rule.speed * SYSTEM_CLASS_RATE_PERCENTAGE / 100
	midClassRate := p.rule.speed * LS_CLASS_RATE_PERCENTAGE / 100
	lowClassRate := p.rule.speed * BE_CLASS_RATE_PERCENTAGE / 100

	errs := apierror.NewAggregate([]error{
		p.classExisted(link, newClass(p.interfLink.Attrs().Index, netlink.HANDLE_ROOT, rootClass, maxCeil, maxCeil, 0)),
		p.classExisted(link, newClass(p.interfLink.Attrs().Index, rootClass, systemClass, highClassRate, maxCeil, SYSTEM_CLASS_PRIO)),
		p.classExisted(link, newClass(p.interfLink.Attrs().Index, rootClass, lsClass, midClassRate, maxCeil, LS_CLASS_PRIO)),
		p.classExisted(link, newClass(p.interfLink.Attrs().Index, rootClass, beClass, lowClassRate, maxCeil, BE_CLASS_PRIO)),
	})

	if errs != nil {
		return false, errs
	}

	return true, nil
}

func (p *tcPlugin) classExisted(nic netlink.Link, expect *netlink.HtbClass) error {
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
