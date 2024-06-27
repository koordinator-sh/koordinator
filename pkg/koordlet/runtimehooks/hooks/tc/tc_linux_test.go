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
	"fmt"
	"os"
	"testing"

	"github.com/coreos/go-iptables/iptables"
	"github.com/golang/mock/gomock"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

func TestObject(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		b := Object()
		assert.NotNil(t, b)
		b1 := Object()
		assert.Equal(t, b, b1)
	})
}

func Test_bvtPlugin_Register(t *testing.T) {
	t.Run("register tc plugin", func(t *testing.T) {
		r := &tcPlugin{}
		r.Register(hooks.Options{})
	})
}

func newTestTCPlugin() *tcPlugin {
	klog.Info("start to init net qos manager")
	p := tcPlugin{
		netLinkHandler: netlink.Handle{},
		rule: &tcRule{
			enable: true,
			netCfg: &NetQosGlobalConfig{
				HwTxBpsMax: 1000000000,
				HwRxBpsMax: 1000000000,
				L1TxBpsMin: 500000000,
				L1TxBpsMax: 1000000000,
				L2TxBpsMin: 500000000,
				L2TxBpsMax: 1000000000,
				L1RxBpsMin: 500000000,
				L1RxBpsMax: 1000000000,
				L2RxBpsMin: 500000000,
				L2RxBpsMax: 1000000000,
			},
		},
	}

	linkInfo, err := system.GetLinkInfoByDefaultRoute()
	if err != nil {
		klog.Errorf("failed to get link info by default route. err=%v\n", err)
		return nil
	}
	if linkInfo == nil || linkInfo.Attrs() == nil {
		klog.Errorf("link info is nil")
		return nil
	}

	p.interfLink = linkInfo

	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("failed to get iptables handler in those dir(%s). err=%v\n", os.Getenv("PATH"), err)
		return nil
	}
	p.iptablesHandler = ipt

	return &p
}

func TestTCPlugin_Init(t *testing.T) {
	plugin := newTestTCPlugin()
	if err := plugin.InitRelatedRules(); err != nil {
		return
	}

	tests := []struct {
		name      string
		preHandle func() error
		wantErr   bool
		endHandle func() error
	}{
		{
			name:      "tc qdisc rules already existed",
			preHandle: plugin.EnsureQdisc,
			wantErr:   false,
			endHandle: plugin.CleanUp,
		},
		{
			name: "tc class rules already existed",
			preHandle: func() error {
				return errors.NewAggregate([]error{
					plugin.EnsureQdisc(),
					plugin.EnsureClasses(),
				})
			},
			wantErr:   false,
			endHandle: plugin.CleanUp,
		},
		{
			name:      "ipset rules already existed",
			preHandle: plugin.EnsureIpset,
			wantErr:   false,
			endHandle: plugin.CleanUp,
		},
		{
			name: "iptables rules already existed",
			preHandle: func() error {
				return errors.NewAggregate([]error{
					plugin.EnsureIpset(),
					plugin.EnsureIptables(),
				})
			},
			wantErr:   false,
			endHandle: plugin.CleanUp,
		},
		{
			name:      "all rulues have already been inited",
			preHandle: plugin.InitRelatedRules,
			wantErr:   false,
			endHandle: plugin.CleanUp,
		},
		{
			name:      "cleanup all rules will be used in advance",
			preHandle: plugin.CleanUp,
			wantErr:   false,
			endHandle: plugin.CleanUp,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.preHandle(); err != nil {
				t.Errorf("failed to run preHandle.err=%v", err)
				return
			}
			if err := plugin.InitRelatedRules(); (err != nil) != tt.wantErr {
				t.Errorf("Init() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err := tt.endHandle(); err != nil {
				t.Errorf("failed to run endHandle.err=%v", err)
				return
			}
		})
	}
}

func genPod(podName, netqos, ip string) *statesinformer.PodMeta {
	return &statesinformer.PodMeta{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: podName,
				Labels: map[string]string{
					"koordinator.sh/netQoSClass": netqos,
				},
			},
			Status: corev1.PodStatus{
				PodIP: ip,
			},
		},
	}
}

func TestTCPlugin_Callback(t *testing.T) {
	pod1 := genPod("pod1", "high_class", "192.168.0.1")
	pod2 := genPod("pod2", "high_class", "192.168.0.2")
	pod3 := genPod("pod3", "mid_class", "192.168.0.3")
	pod4 := genPod("pod4", "low_class", "192.168.0.4")
	pod5 := genPod("pod5", "", "192.168.0.5")
	pod6 := genPod("pod6", "low_class", "192.168.0.6")

	plugin := newTestTCPlugin()
	if err := plugin.InitRelatedRules(); err != nil {
		klog.Errorf("failed to init some necessary info tc plugin.")
		return
	}
	defer plugin.CleanUp()

	type args struct {
		targets *statesinformer.CallbackTarget
	}

	tests := []struct {
		name          string
		args          args
		wantFields    *int64
		ipsetExpected map[string][]string
	}{
		{
			name: "",
			args: args{
				targets: &statesinformer.CallbackTarget{
					Pods: []*statesinformer.PodMeta{
						pod1, pod2, pod3, pod4, pod5,
					},
				},
			},
			wantFields: nil,
			ipsetExpected: map[string][]string{
				"high_class": {"192.168.0.1", "192.168.0.2"},
				"mid_class":  {"192.168.0.3"},
				"low_class":  {"192.168.0.4"},
			},
		},
		{
			name: "",
			args: args{
				targets: &statesinformer.CallbackTarget{
					Pods: []*statesinformer.PodMeta{
						pod2, pod3, pod4, pod5, pod6,
					},
				},
			},
			wantFields: nil,
			ipsetExpected: map[string][]string{
				"high_class": {"192.168.0.2"},
				"mid_class":  {"192.168.0.3"},
				"low_class":  {"192.168.0.4", "192.168.0.6"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			if err := plugin.ruleUpdateCbForPod(tt.args.targets); err != nil {
				klog.Errorf("failed to process ruleUpdateCb, err=%v", err)
				return
			}
			if _, err := plugin.checkAllRulesExisted(); err != nil {
				t.Errorf("some necessary rules not existed. err=%v", err)
				return
			}
			if !checkIpsetIsRight(tt.ipsetExpected) {
				t.Errorf("ipset rules not the same as expected")
				return
			}
		})
	}
}

func checkIpsetIsRight(rules map[string][]string) bool {
	for setName, ips := range rules {
		for _, ip := range ips {
			if !ipsetEntryExisted(setName, ip) {
				fmt.Printf("%s:%s ipset rules not the same as expected\n", setName, ip)
				return false
			}
		}
	}

	return true
}

func ipsetEntryExisted(setName, ip string) bool {
	result, err := netlink.IpsetList(setName)
	if err != nil || result == nil {
		return false
	}

	for _, entry := range result.Entries {
		if entry.IP.String() == ip {
			return true
		}
	}

	return false
}

func Test_getMinorId(t *testing.T) {
	type args struct {
		num uint32
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		// TODO: Add test cases.
		{
			name: "demo",
			args: args{num: 115914},
			want: "c4ca",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, getMinorId(tt.args.num), "getMinorId(%v)", tt.args.num)
		})
	}
}
