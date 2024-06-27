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
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	apierror "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/utils/exec"
	"k8s.io/utils/pointer"

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
	SYSTEM_CLASS_PRIO = 1
	LS_CLASS_PRIO     = 2
	BE_CLASS_PRIO     = 3

	POD_FILTER_PRIO = 4

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
	ruleRWMutex sync.RWMutex
	rule        *tcRule

	// this is the physical NIC on host, default eth0
	interfLink netlink.Link

	// for executing the iptables command.
	iptablesHandler *iptables.IPTables
	// for executing the tc and ipset command.
	netLinkHandler netlink.Handle

	allPodsSyncOnce sync.Once

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
		ruleRWMutex:     sync.RWMutex{},
		netLinkHandler:  netlink.Handle{},
		allPodsSyncOnce: sync.Once{},
		rule:            newRule(),
	}
}

func (p *tcPlugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", name)

	rule.Register(ruleNameForNodeSLO, description,
		rule.WithParseFunc(statesinformer.RegisterTypeNodeSLOSpec, p.parseRuleForNodeSLO),
		rule.WithUpdateCallback(p.ruleUpdateCbForNodeSlo),
	)

	rule.Register(ruleNameForAllPods, description,
		rule.WithParseFunc(statesinformer.RegisterTypeAllPods, p.parseForAllPods),
		rule.WithUpdateCallback(p.ruleUpdateCbForPod))
	// TODO register NRI after there is pod ip in NRI request

	reconciler.RegisterCgroupReconciler(reconciler.PodLevel, sysutil.NetClsClassId, description+" (pod net class id)",
		p.SetPodNetCls, reconciler.NoneFilter())

	p.executor = op.Executor
}

func (p *tcPlugin) SetPodNetCls(proto protocol.HooksProtocol) error {
	podCtx := proto.(*protocol.PodContext)
	if podCtx == nil {
		return fmt.Errorf("pod protocol is nil for plugin %v", name)
	}

	netQos := GetNetQoSClassByAttrs(podCtx.Request.Labels, podCtx.Request.Annotations)
	if netQos == NETQoSNone {
		return nil
	}

	ing, egress, err := getIngressAndEgress(podCtx.Request.Annotations)
	if err != nil {
		klog.Errorf("failed to get net config from annotation in pod(%s/%s/%v)", podCtx.Request.PodMeta.Namespace, podCtx.Request.PodMeta.Name, podCtx.Request.PodMeta.UID)
	}

	r := p.getRule()
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}

	var handle uint32
	needLimitInPodLevel := ing != 0 || egress != 0
	if needLimitInPodLevel {
		if handleId, ok := r.uidToHandle[types.UID(podCtx.Request.PodMeta.UID)]; ok {
			handle = handleId
		}
	} else {
		netqosToHandle := func(qos NetQoSClass) uint32 {
			m := map[NetQoSClass]uint32{
				NETQoSSystem: systemClass,
				NETQoSLS:     lsClass,
				NETQoSBE:     beClass,
			}
			if handle, ok := m[qos]; ok {
				return handle
			}
			return lsClass
		}

		handle = netqosToHandle(netQos)
	}

	podCtx.Response.Resources.NetClsClassId = pointer.Uint32(handle)

	return nil
}

func (p *tcPlugin) createTcRulesForHostPod(rule *tcRule, pod *v1.Pod, egress uint64) error {
	handle, _ := rule.uidToHandle[pod.UID]
	netqos := GetNetQoSClassByAttrs(pod.Labels, pod.Annotations)
	cls := newClass(p.interfLink.Attrs().Index, rootClass, handle, egress, egress, GetPrio(netqos))
	err := p.ensureClass(p.interfLink, cls)
	if err != nil {
		return err
	}

	minorHex := getMinorId(handle)
	minorDecimal, _ := strconv.ParseUint(minorHex, 16, 64)
	genFilterCmd := func(op string) exec.Cmd {
		// tc filter add dev eth0 parent 1: protocol ip prio 2 handle 5: cgroup
		// means to 1:5 class
		return exec.New().Command("tc", "filter", op, "dev", p.interfLink.Attrs().Name,
			"protocol", "ip", "prio", strconv.Itoa(getPrio(p.interfLink.Attrs().Name)),
			"parent", fmt.Sprintf("%d:", ROOT_CLASS_MINOR_ID),
			"handle", fmt.Sprintf("%d:", minorDecimal),
			"cgroup",
		)
	}

	return p.ensureFilter(KeyByHandle(minorHex), genFilterCmd("add"), genFilterCmd("change"))
}

func (p *tcPlugin) delTcRules(handle uint32) error {
	prio := getFilterPrio(p.interfLink.Attrs().Name, handle)
	if prio == "" {
		return nil
	}

	delFilterCmd := exec.New().Command("tc", "filter", "delete", "dev", p.interfLink.Attrs().Name,
		"parent", fmt.Sprintf("%d:", ROOT_CLASS_MINOR_ID), "prio", prio,
	)
	data, err := delFilterCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete tc filter, output: %s, err: %v", string(data), err)
	}

	cls := newClass(p.interfLink.Attrs().Index, rootClass, handle, 0, 0, 0)
	return p.deleteClass(p.interfLink, cls)
}

func getFilterPrio(nic string, handle uint32) string {
	minorHex := getMinorId(handle)
	prio := getPrioForFilter(nic, KeyByHandle(minorHex))
	if prio != "" {
		return prio
	}

	return getPrioForFilter(nic, KeyByFlowId(minorHex))
}

// find filter by a key value, just as "handle 0x**"
func getPrioForFilter(nic string, key string) string {
	// output just like this:
	// filter parent 1: protocol ip pref 1 cgroup chain 0
	// filter parent 1: protocol ip pref 1 cgroup chain 0 handle 0x3
	output, err := exec.New().Command("tc", "filter", "show", "dev", nic).CombinedOutput()
	if err != nil {
		return ""
	}
	strs := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(strs) == 0 {
		return ""
	}

	for _, line := range strs {
		if !strings.Contains(line, key) {
			continue
		}

		params := strings.Fields(line)
		for idx, param := range params {
			if param == "pref" {
				return params[idx+1]
			}
		}
	}

	return ""
}

func (p *tcPlugin) prepare() error {
	linkInfo, err := system.GetLinkInfoByDefaultRoute()
	if err != nil {
		return fmt.Errorf("failed to get link info by default route. err=%v\n", err)
	}
	if linkInfo == nil || linkInfo.Attrs() == nil {
		return fmt.Errorf("link info is nil")
	}

	p.interfLink = linkInfo

	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("failed to get iptables handler in those dir(%s). err=%v\n", os.Getenv("PATH"), err)
		return err
	}
	p.iptablesHandler = ipt

	return nil
}

func getMinorId(num uint32) string {
	minor := num - MAJOR_ID<<16
	return strconv.FormatUint(uint64(minor), 16)
}

func (p *tcPlugin) refreshForAllPods(pods []*statesinformer.PodMeta, rule *tcRule) error {
	ipInK8S := make(map[string]sets.String)
	ipInIpset := make(map[string]sets.String)
	activePods := make(map[types.UID]interface{})

	// handle active pod
	for _, pod := range pods {
		activePods[pod.Pod.UID] = nil
		netqos := GetPodNetQoSClass(pod.Pod)
		if netqos == NETQoSNone {
			continue
		}

		ing, egress, err := getIngressAndEgress(pod.Pod.Annotations)
		if err != nil {
			klog.Errorf("failed to get net config from annotation in pod(%s)", format.Pod(pod.Pod))
		}
		needLimitAtPodLevel := ing != 0 || egress != 0

		if needLimitAtPodLevel {
			// create tc rules
			if err := initHandleId(pod.Pod.UID, rule.handleToUid, rule.uidToHandle); err != nil {
				klog.Errorf(err.Error())
				continue
			}
			p.updateRule(rule)

			if pod.Pod.Spec.HostNetwork {
				// pod in host network namespace, network bandwidth can be limited by net_cls cgroup.
				err = p.createTcRulesForHostPod(rule, pod.Pod, egress)
			} else {
				err = p.createRulesForPod(rule, pod.Pod, netqos, egress)
			}
			if err != nil {
				klog.Errorf("failed to create network rules for pod(uid:%s; ip:%s). err=%v", string(pod.Pod.UID), pod.Pod.Status.PodIP, err)
			}
			continue
		}

		// finally, handled by network rules at the node level
		if ipInK8S[string(netqos)] == nil {
			ipInK8S[string(netqos)] = sets.NewString()
		}
		ipInK8S[string(netqos)].Insert(pod.Pod.Status.PodIP)
	}

	// delete netqos rules for the deleted pods.
	for uid, handle := range rule.uidToHandle {
		_, ok := activePods[uid]
		if ok {
			continue
		}

		if err := p.delTcRules(handle); err != nil {
			klog.Errorf("failed to delete network rules for pod(uid:%s). err=%v", uid, err)
			continue
		}

		delete(rule.uidToHandle, uid)
		delete(rule.handleToUid, handle)
		p.updateRule(rule)
	}

	// handle netqos rules at the node level.
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
			klog.V(5).Infof("ready to add %s to ipset %s.", ip, setName)
			if err := netlink.IpsetAdd(setName, &netlink.IPSetEntry{IP: net.ParseIP(ip).To4()}); err != nil {
				klog.Warningf("failed to write ip %s to ipset %s, err=%v", ip, setName, err)
				return err
			}
		}

		for ip := range ipInIpset[setName].Difference(ipInK8S[setName]) {
			klog.V(5).Infof("ready to del ip %s from ipset %s.", ip, setName)
			if err := netlink.IpsetDel(setName, &netlink.IPSetEntry{IP: net.ParseIP(ip).To4()}); err != nil {
				klog.Warningf("failed to write ip %s to ipset %s, err=%v", ip, setName, err)
				return err
			}
		}
	}

	return nil
}

func (p *tcPlugin) createRulesForPod(rule *tcRule, pod *v1.Pod, netqos NetQoSClass, egress uint64) error {
	klog.V(5).Infof("start to create related rules for pod(uid:%s; ip:%s), anno:%v", pod.UID, pod.Status.PodIP, pod.Annotations)

	handle, _ := rule.uidToHandle[pod.UID]
	cls := newClass(p.interfLink.Attrs().Index, rootClass, handle, egress, egress, GetPrio(netqos))
	err := p.ensureClass(p.interfLink, cls)
	if err != nil {
		klog.Errorf("failed to create class for pod %s, err=%v", string(pod.UID), err)
		return err
	}
	minorHex := getMinorId(handle)
	// tc filter add dev eth0 parent 1:0 protocol ip prio 2 u32 match ip dst 0.0.0.0/0 flowid 1:5
	genFilterCmd := func(op string) exec.Cmd {
		// tc filter add dev br0 parent 1:0 protocol ip prio 2 u32 match ip src 1.2.0.0 classid 1:5
		return exec.New().Command("tc", "filter", op, "dev", p.interfLink.Attrs().Name,
			"parent", fmt.Sprintf("%d:", ROOT_CLASS_MINOR_ID),
			"protocol", "ip", "prio", strconv.Itoa(getPrio(p.interfLink.Attrs().Name)),
			"u32", "match", "ip", "src", pod.Status.PodIP,
			"classid", fmt.Sprintf("%d:%s", ROOT_CLASS_MINOR_ID, minorHex),
		)
	}

	if err := p.ensureFilter(KeyByFlowId(minorHex), genFilterCmd("add"), genFilterCmd("change")); err != nil {
		klog.Errorf("failed to create class for pod %s, err=%v", string(pod.UID), err)
		return err
	}

	return nil
}

// getPrio get next available priority for tc filter.
func getPrio(name string) int {
	// output just like this:
	// filter parent 1: protocol ip pref 1 cgroup chain 0
	// filter parent 1: protocol ip pref 1 cgroup chain 0 handle 0x3
	output, err := exec.New().Command("tc", "filter", "show", "dev", name).CombinedOutput()
	if err != nil {
		return 0
	}
	strs := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(strs) == 0 {
		return 0
	}

	lastLine := ""
	for i := len(strs) - 1; i >= 0; i-- {
		if strings.HasPrefix(strs[i], "filter") {
			lastLine = strs[i]
			break
		}
	}

	params := strings.Fields(lastLine)
	for idx, param := range params {
		if param == "pref" {
			prio, _ := strconv.Atoi(params[idx+1])
			return prio + 1
		}
	}

	return 0
}

// initHandleId get class minor id from pod.uid(last 4 digits).
func initHandleId(uid types.UID, handleToUid map[uint32]types.UID, uidToHandle map[types.UID]uint32) error {
	if _, ok := uidToHandle[uid]; ok {
		return nil
	}

	if len(handleToUid) >= (1<<16)-1 {
		return errors.New("tc class is too much")
	}

	for {
		minorId := rand.Int31n(MAJOR_ID << 16)
		handleId := netlink.MakeHandle(MAJOR_ID, uint16(minorId))
		if _, ok := handleToUid[handleId]; !ok {
			handleToUid[handleId] = uid
			uidToHandle[uid] = handleId
			return nil
		}
	}
}

func (p *tcPlugin) InitRelatedRules() error {
	return apierror.NewAggregate([]error{
		p.EnsureQdisc(),
		p.EnsureClasses(),
		p.EnsureCgroupFilters(),
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
	klog.V(5).Infof("start to delete qdisc created by tc plugin.")
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
	r := p.getRule()
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return nil
	}

	return apierror.NewAggregate([]error{
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, netlink.HANDLE_ROOT, rootClass, r.netCfg.HwTxBpsMax, r.netCfg.HwTxBpsMax, 0)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, systemClass, r.netCfg.HwTxBpsMax-r.netCfg.L1TxBpsMin-r.netCfg.L2TxBpsMin, r.netCfg.HwTxBpsMax, SYSTEM_CLASS_PRIO)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, lsClass, r.netCfg.L1TxBpsMin, r.netCfg.L1TxBpsMax, LS_CLASS_PRIO)),
		p.ensureClass(p.interfLink, newClass(p.interfLink.Attrs().Index, rootClass, beClass, r.netCfg.L2TxBpsMin, r.netCfg.L2TxBpsMax, BE_CLASS_PRIO)),
	})
}

func (p *tcPlugin) EnsureCgroupFilters() error {
	klog.V(5).Infof("start to create tc cgroup filter rules.")
	genFilterCmd := func(op string, prio int, minor uint16) exec.Cmd {
		return exec.New().Command("tc", "filter", op, "dev", p.interfLink.Attrs().Name,
			"protocol", "ip", "prio", fmt.Sprintf("%d", prio),
			"parent", fmt.Sprintf("%d:", ROOT_CLASS_MINOR_ID),
			"handle", fmt.Sprintf("%d:", minor),
			"cgroup",
		)
	}

	return apierror.NewAggregate([]error{
		p.ensureFilter(KeyByHandle(strconv.Itoa(SYSTEM_CLASS_MINOR_ID)), genFilterCmd("add", SYSTEM_CLASS_PRIO, SYSTEM_CLASS_MINOR_ID),
			genFilterCmd("change", SYSTEM_CLASS_PRIO, SYSTEM_CLASS_MINOR_ID)),
		p.ensureFilter(KeyByHandle(strconv.Itoa(LS_CLASS_MINOR_ID)), genFilterCmd("add", LS_CLASS_PRIO, LS_CLASS_MINOR_ID),
			genFilterCmd("change", LS_CLASS_PRIO, LS_CLASS_MINOR_ID)),
		p.ensureFilter(KeyByHandle(strconv.Itoa(BE_CLASS_MINOR_ID)), genFilterCmd("add", BE_CLASS_PRIO, BE_CLASS_MINOR_ID),
			genFilterCmd("change", BE_CLASS_PRIO, BE_CLASS_MINOR_ID)),
	})
}

func KeyByHandle(clsMinorId string) string {
	return "handle 0x" + clsMinorId
}

func KeyByFlowId(clsMinorId string) string {
	return fmt.Sprintf("flowid %d:%s", MAJOR_ID, clsMinorId)
}

func GetPrio(qos NetQoSClass) uint32 {
	m := map[NetQoSClass]uint32{
		NETQoSSystem: SYSTEM_CLASS_PRIO,
		NETQoSLS:     LS_CLASS_PRIO,
		NETQoSBE:     BE_CLASS_PRIO,
		NETQoSNone:   LS_CLASS_PRIO,
	}

	return m[qos]
}

func newClass(index int, parent, handle uint32, rate, ceil uint64, prio uint32) *netlink.HtbClass {
	attr := netlink.ClassAttrs{
		LinkIndex: index,
		Parent:    parent,
		Handle:    handle,
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

func (p *tcPlugin) deleteClass(nic netlink.Link, expect *netlink.HtbClass) error {
	classes, err := netlink.ClassList(nic, 0)
	if err != nil {
		return fmt.Errorf("failed to get tc class. err=%v", err)
	}

	for _, class := range classes {
		switch class.(type) {
		case *netlink.HtbClass:
			htbClass := class.(*netlink.HtbClass)
			if htbClass != nil &&
				htbClass.Handle == expect.Handle &&
				htbClass.Parent == expect.Parent {
				return netlink.ClassDel(expect)
			}
		}
	}

	return nil
}

func (p *tcPlugin) deleteFilter(key string, delFunc exec.Cmd) error {
	matchCmd := exec.New().Command("tc", "filter", "show", "dev", p.interfLink.Attrs().Name)
	data, err := matchCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get tc filter by key:%s, err:%v", key, err)
	}

	if !strings.Contains(string(data), key) {
		return nil
	}

	data, err = delFunc.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete tc filter, output: %s, err: %v", string(data), err)
	}
	klog.V(5).Infof("succeed to delete filter for %s ", p.interfLink.Attrs().Name)

	return nil
}

func (p *tcPlugin) ensureFilter(find string, createCmd, updateCmd exec.Cmd) error {
	matchCmd := exec.New().Command("tc", "filter", "show", "dev", p.interfLink.Attrs().Name)
	data, err := matchCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get tc filter by key:%s, err:%v", find, err)
	}

	if strings.Contains(string(data), find) {
		return nil
	}

	// handled by command because netlink does not support creating filters for cgroup types.
	// creating a tc filter for be netqos pod, just as follows:
	// tc filter add dev eth0 parent 1:0 protocol ip prio 2 match ip src 1.2.0.0 classid 1:5
	data, err = createCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create tc filter, output: %s, err: %v", string(data), err)
	}
	klog.V(5).Infof("%s created filter", p.interfLink.Attrs().Name)

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

	r := p.getRule()
	if r == nil {
		klog.V(5).Infof("hook plugin rule is nil, nothing to do for plugin %v", name)
		return false, nil
	}

	maxCeil := r.speed * CEIL_PERCENTAGE / 100
	// other leaf class
	highClassRate := r.speed * SYSTEM_CLASS_RATE_PERCENTAGE / 100
	midClassRate := r.speed * LS_CLASS_RATE_PERCENTAGE / 100
	lowClassRate := r.speed * BE_CLASS_RATE_PERCENTAGE / 100

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
