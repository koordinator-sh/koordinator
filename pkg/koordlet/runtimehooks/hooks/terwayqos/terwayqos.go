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

package terwayqos

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/rule"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

const (
	rootPath   = "/host-var-lib/terway/qos"
	podConfig  = "pod.json"
	nodeConfig = "global_bps_config"
)

const (
	name        = "TerwayQoS"
	description = "network qos management"

	ruleNameForNodeQoS = name + " (nodeQoS)"
	ruleNameForAllPods = name + " (allPods)"
)

type Plugin struct {
	executor resourceexecutor.ResourceUpdateExecutor

	lock    sync.RWMutex
	enabled *bool
	node    *Node
	pods    map[string]*Pod

	podFilePath, nodeFilePath string

	syncChan chan struct{}
}

func (p *Plugin) Register(op hooks.Options) {
	klog.V(5).Infof("register hook %v", "terwqy qos configure generator")
	rule.Register(ruleNameForNodeQoS, description,
		rule.WithParseFunc(statesinformer.RegisterTypeNodeSLOSpec, p.parseRuleForNodeSLO),
		rule.WithUpdateCallback(p.update))
	rule.Register(ruleNameForAllPods, description,
		rule.WithParseFunc(statesinformer.RegisterTypeAllPods, p.parseForAllPods),
		rule.WithUpdateCallback(p.update))

	p.executor = op.Executor

	err := os.MkdirAll(rootPath, os.ModeDir)
	if err != nil {
		klog.Fatal("create terway qos dir failed, err: %v", err)
	}

	go p.run()
}

func (p *Plugin) parseRuleForNodeSLO(mergedNodeSLOIf interface{}) (bool, error) {
	mergedNodeSLO := mergedNodeSLOIf.(*slov1alpha1.NodeSLOSpec)
	enabled := false

	p.lock.Lock()
	defer p.lock.Unlock()

	if mergedNodeSLO.ResourceQOSStrategy != nil &&
		mergedNodeSLO.ResourceQOSStrategy.Policies != nil &&
		mergedNodeSLO.ResourceQOSStrategy.Policies.NETQOSPolicy != nil &&
		*mergedNodeSLO.ResourceQOSStrategy.Policies.NETQOSPolicy == slov1alpha1.NETQOSPolicyTerwayQos {
		enabled = true

		n := &Node{}
		err := parseNetQoS(mergedNodeSLO, n)
		if err != nil {
			klog.Errorf("parse net qos failed, err: %v", err)
			return false, err
		}

		p.node = n
		p.enabled = &enabled
	} else {
		p.enabled = &enabled
	}
	klog.Infof("terway qos enabled %v", enabled)

	select {
	case p.syncChan <- struct{}{}:
	default:
	}

	return true, nil
}

func (p *Plugin) parseForAllPods(e interface{}) (bool, error) {
	_, ok := e.(*struct{})
	if !ok {
		return false, fmt.Errorf("invalid rule type %T", e)
	}

	return true, nil
}

func (p *Plugin) run() {
	for {
		select {
		case <-p.syncChan:
			p.syncAll()
		}
	}
}

func (p *Plugin) syncAll() {
	err := p.syncNodeConfig()
	if err != nil {
		klog.Errorf("sync node config failed, err: %v", err)
		return
	}

	err = p.syncPodConfig()
	if err != nil {
		klog.Errorf("sync pod config failed, err: %v", err)
		return
	}
}

func (p *Plugin) update(target *statesinformer.CallbackTarget) error {
	if target == nil {
		return fmt.Errorf("callback target is nil")
	}

	pods := make(map[string]*Pod)
	for _, meta := range target.Pods {
		if meta.Pod == nil {
			continue
		}

		ing, egress, err := getPodQoS(meta.Pod.Annotations)
		if err != nil {
			klog.Errorf("get pod qos failed, err: %v", err)
			continue
		}
		pods[string(meta.Pod.UID)] = &Pod{
			PodName:      meta.Pod.Name,
			PodNamespace: meta.Pod.Namespace,
			PodUID:       string(meta.Pod.UID),
			Prio:         getPodPrio(meta.Pod),
			CgroupDir:    filepath.Join("/sys/fs/cgroup/net_cls", meta.CgroupDir),
			QoSConfig: QoSConfig{
				IngressBandwidth: ing,
				EgressBandwidth:  egress,
			},
		}
	}

	p.lock.Lock()
	p.pods = pods
	p.lock.Unlock()

	select {
	case p.syncChan <- struct{}{}:
	default:
	}
	return nil
}

func (p *Plugin) syncPodConfig() error {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.enabled == nil {
		return nil
	}

	if !*p.enabled {
		_ = os.Remove(p.podFilePath)
		return nil
	}

	err := ensureConfig(p.podFilePath)
	if err != nil {
		return err
	}

	outPods, err := json.Marshal(p.pods)
	if err != nil {
		return err
	}

	pEvent := audit.V(3).Node().Reason("netqos reconcile").Message("update pod to : %v", string(outPods))
	pRes, err := resourceexecutor.NewCommonDefaultUpdater(p.podFilePath, p.podFilePath, string(outPods), pEvent)
	if err != nil {
		return err
	}

	p.executor.UpdateBatch(true, pRes)
	return nil
}

func (p *Plugin) syncNodeConfig() error {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.enabled == nil {
		return nil
	}

	if !*p.enabled {
		_ = os.Remove(p.nodeFilePath)
		return nil
	}

	err := ensureConfig(p.nodeFilePath)
	if err != nil {
		return err
	}

	outNode, err := p.node.MarshalText()
	if err != nil {
		return err
	}

	nEvent := audit.V(3).Node().Reason("netqos reconcile").Message("update node to : %v", string(outNode))
	nRes, err := resourceexecutor.NewCommonDefaultUpdater(p.nodeFilePath, p.nodeFilePath, string(outNode), nEvent)
	if err != nil {
		return err
	}

	p.executor.UpdateBatch(true, nRes)
	return nil
}

func ensureConfig(file string) error {
	_, err := os.Stat(file)
	if err != nil {
		_, err = os.Create(file)
		if err != nil {
			return fmt.Errorf("create terway qos file %s failed, err: %v", file, err)
		}
	}
	return nil
}

// parseNetQoS only LS and BE is taken into account
func parseNetQoS(slo *slov1alpha1.NodeSLOSpec, node *Node) error {
	if slo.SystemStrategy == nil {
		return nil
	}
	if slo.SystemStrategy.TotalNetworkBandwidth.Value() < 0 {
		return fmt.Errorf("invalid total network bandwidth %d", slo.SystemStrategy.TotalNetworkBandwidth.Value())
	}
	total := uint64(slo.SystemStrategy.TotalNetworkBandwidth.Value())

	// to Byte/s
	node.HwTxBpsMax = BitsToBytes(total)
	node.HwRxBpsMax = BitsToBytes(total)

	qos := slo.ResourceQOSStrategy
	if qos == nil {
		return nil
	}

	if qos.LSClass != nil && qos.LSClass.NetworkQOS != nil && qos.LSClass.NetworkQOS.Enable != nil && *qos.LSClass.NetworkQOS.Enable {
		q, err := parseQoS(qos.LSClass.NetworkQOS, total)
		if err != nil {
			return err
		}
		node.L1RxBpsMin = q.IngressRequestBps
		node.L1RxBpsMax = q.IngressLimitBps
		node.L1TxBpsMin = q.EgressRequestBps
		node.L1TxBpsMax = q.EgressLimitBps
	}

	if qos.BEClass != nil && qos.BEClass.NetworkQOS != nil && qos.BEClass.NetworkQOS.Enable != nil && *qos.BEClass.NetworkQOS.Enable {
		q, err := parseQoS(qos.BEClass.NetworkQOS, total)
		if err != nil {
			return err
		}
		node.L2RxBpsMin = q.IngressRequestBps
		node.L2RxBpsMax = q.IngressLimitBps
		node.L2TxBpsMin = q.EgressRequestBps
		node.L2TxBpsMax = q.EgressLimitBps
	}

	return nil
}

func parseQoS(qos *slov1alpha1.NetworkQOSCfg, total uint64) (QoS, error) {
	q := QoS{
		IngressRequestBps: 0,
		IngressLimitBps:   0,
		EgressRequestBps:  0,
		EgressLimitBps:    0,
	}
	if qos.Enable == nil {
		return q, nil
	}
	if !*qos.Enable {
		return q, nil
	}

	var err error
	q.IngressRequestBps, err = parseQuantity(qos.IngressRequest, total)
	if err != nil {
		return QoS{}, err
	}

	q.IngressLimitBps, err = parseQuantity(qos.IngressLimit, total)
	if err != nil {
		return QoS{}, err
	}

	q.EgressRequestBps, err = parseQuantity(qos.EgressRequest, total)
	if err != nil {
		return QoS{}, err
	}
	q.EgressLimitBps, err = parseQuantity(qos.EgressLimit, total)
	if err != nil {
		return QoS{}, err
	}

	return q, nil
}

func parseQuantity(v *intstr.IntOrString, total uint64) (uint64, error) {
	if v == nil {
		return 0, nil
	}
	if v.Type == intstr.String {
		val, err := resource.ParseQuantity(v.String())
		if err != nil {
			return 0, err
		}
		r := BitsToBytes(uint64(val.Value()))
		if r > total {
			return 0, fmt.Errorf("quantity %s is larger than total %d", v.String(), total)
		}

		return r, nil
	} else {
		return uint64(v.IntValue()) * total / 100, nil
	}
}

func getPodQoS(anno map[string]string) (uint64, uint64, error) {
	var ingress, egress uint64

	if anno[extension.AnnotationNetworkQOS] != "" {
		nqos := &NetworkQoS{}
		err := json.Unmarshal([]byte(anno[extension.AnnotationNetworkQOS]), nqos)
		if err != nil {
			return 0, 0, err
		}
		ing, err := resource.ParseQuantity(nqos.IngressLimit)
		if err != nil {
			return 0, 0, err
		}
		ingress = BitsToBytes(uint64(ing.Value()))

		eg, err := resource.ParseQuantity(nqos.EgressLimit)
		if err != nil {
			return 0, 0, err
		}
		egress = BitsToBytes(uint64(eg.Value()))
	}

	return ingress, egress, nil
}

func getPodPrio(pod *corev1.Pod) int {
	prio, ok := prioMapping[pod.Labels[extension.LabelPodQoS]]
	if ok {
		return prio
	}
	switch pod.Status.QOSClass {
	case corev1.PodQOSGuaranteed, corev1.PodQOSBurstable:
		return 1
	case corev1.PodQOSBestEffort:
		return 2
	}
	return 0
}

func newPlugin() *Plugin {
	return &Plugin{
		executor:     resourceexecutor.NewResourceUpdateExecutor(),
		podFilePath:  filepath.Join(rootPath, podConfig),
		nodeFilePath: filepath.Join(rootPath, nodeConfig),
		node:         &Node{},
		pods:         make(map[string]*Pod),
		syncChan:     make(chan struct{}, 1),
	}
}

var singleton *Plugin
var once sync.Once

func Object() *Plugin {
	once.Do(func() {
		singleton = newPlugin()
	})
	return singleton
}

func BitsToBytes[T uint64 | float64 | int](bits T) T {
	return bits / 8
}
