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
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/golang/mock/gomock"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"

	mockmetriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
)

func newTestTCManager(opt *framework.Options) *TCManager {
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
		speed:             uint64(speedStr) * 1000 * 1000,
		iptablesHandler:   ipt,
		netLinkHandler:    netlink.Handle{},
	}

	return &n
}

func TestTCManager_Init(t *testing.T) {
	manager := newTestTCManager(nil)
	tests := []struct {
		name      string
		preHandle func() error
		wantErr   bool
		endHandle func() error
	}{
		{
			name:      "tc qdisc rules already existed",
			preHandle: manager.EnsureQdisc,
			wantErr:   false,
			endHandle: manager.CleanUp,
		},
		{
			name: "tc class rules already existed",
			preHandle: func() error {
				return errors.NewAggregate([]error{
					manager.EnsureQdisc(),
					manager.EnsureClasses(),
				})
			},
			wantErr:   false,
			endHandle: manager.CleanUp,
		},
		{
			name:      "ipset rules already existed",
			preHandle: manager.EnsureIpset,
			wantErr:   false,
			endHandle: manager.CleanUp,
		},
		{
			name: "iptables rules already existed",
			preHandle: func() error {
				return errors.NewAggregate([]error{
					manager.EnsureIpset(),
					manager.EnsureIptables(),
				})
			},
			wantErr:   false,
			endHandle: manager.CleanUp,
		},
		{
			name:      "all rulues have already been inited",
			preHandle: manager.InitRatledRules,
			wantErr:   false,
			endHandle: manager.CleanUp,
		},
		{
			name:      "cleanup all rules will be used in advance",
			preHandle: manager.CleanUp,
			wantErr:   false,
			endHandle: manager.CleanUp,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.preHandle(); err != nil {
				t.Errorf("failed to run preHandle.err=%v", err)
			}
			if err := manager.InitRatledRules(); (err != nil) != tt.wantErr {
				t.Errorf("Init() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err := tt.endHandle(); err != nil {
				t.Errorf("failed to run endHandle.err=%v", err)
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

func TestTCManager_Reconcile(t *testing.T) {
	pod1 := genPod("pod1", "high_class", "192.168.0.1")
	pod2 := genPod("pod2", "high_class", "192.168.0.2")
	pod3 := genPod("pod3", "mid_class", "192.168.0.3")
	pod4 := genPod("pod4", "low_class", "192.168.0.4")
	pod5 := genPod("pod5", "", "192.168.0.5")
	pod6 := genPod("pod6", "low_class", "192.168.0.6")

	t.Run("net qos manager reconcile", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		statesInformer := mockstatesinformer.NewMockStatesInformer(ctrl)
		statesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{
			pod1,
			pod2,
			pod3,
			pod4,
			pod5}).AnyTimes()

		mockMetricCache := mockmetriccache.NewMockMetricCache(ctrl)
		options := &framework.Options{
			StatesInformer: statesInformer,
			MetricCache:    mockMetricCache,
			Config:         framework.NewDefaultConfig(),
		}
		manager := newTestTCManager(options)
		manager.Reconcile()
		if _, err := manager.checkAllRulesExisted(); err != nil {
			t.Errorf("some necessary rules not existed. err=%v", err)
		}

		ipsetRulesExperied := map[string][]string{
			"high_class": {"192.168.0.1", "192.168.0.2"},
			"mid_class":  {"192.168.0.3"},
			"low_class":  {"192.168.0.4"},
		}

		if !checkIpsetIsRight(ipsetRulesExperied) {
			t.Errorf("ipset rules not the same as expected")
		}

		statesInformer = mockstatesinformer.NewMockStatesInformer(ctrl)
		statesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{
			pod2,
			pod3,
			pod4,
			pod5,
			pod6}).AnyTimes()
		options.StatesInformer = statesInformer
		manager = newTestTCManager(options)
		manager.Reconcile()
		if _, err := manager.checkAllRulesExisted(); err != nil {
			t.Errorf("some necessary rules not existed. err=%v", err)
		}

		ipsetRulesExperied = map[string][]string{
			"high_class": {"192.168.0.2"},
			"mid_class":  {"192.168.0.3"},
			"low_class":  {"192.168.0.4", "192.168.0.6"},
		}
		if !checkIpsetIsRight(ipsetRulesExperied) {
			t.Errorf("ipset rules not the same as expected")
		}

		manager.CleanUp()

	})
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
