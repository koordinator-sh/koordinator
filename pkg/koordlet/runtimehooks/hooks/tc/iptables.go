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

	apierror "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
)

func (p *tcPlugin) EnsureIptables() error {
	klog.V(5).Infof("start to create iptables.")
	if p.iptablesHandler == nil {
		return fmt.Errorf("can't create tc iptables rulues,because qos manager is nil")
	}

	ipsetToClassid := map[NetQoSClass]string{
		NETQoSSystem: convertToClassId(ROOT_CLASS_MINOR_ID, SYSTEM_CLASS_MINOR_ID),
		NETQoSLS:     convertToClassId(ROOT_CLASS_MINOR_ID, LS_CLASS_MINOR_ID),
		NETQoSBE:     convertToClassId(ROOT_CLASS_MINOR_ID, BE_CLASS_MINOR_ID),
	}

	for ipsetName, classid := range ipsetToClassid {
		err := p.iptablesHandler.AppendUnique("mangle", "POSTROUTING",
			"-m", "set", "--match-set", string(ipsetName), "src",
			"-j", "CLASSIFY", "--set-class", classid)
		if err != nil {
			klog.Errorf("ipt append err=%v", err)
			return err
		}
	}

	return nil
}

func (p *tcPlugin) DelIptables() error {
	klog.V(5).Infof("start to delete iptables rules created by tc plugin.")
	if p.iptablesHandler == nil {
		return fmt.Errorf("can't create tc iptables rulues,because qos manager is nil")
	}
	ipsetToClassid := map[NetQoSClass]string{
		NETQoSSystem: convertToClassId(ROOT_CLASS_MINOR_ID, SYSTEM_CLASS_MINOR_ID),
		NETQoSLS:     convertToClassId(ROOT_CLASS_MINOR_ID, LS_CLASS_MINOR_ID),
		NETQoSBE:     convertToClassId(ROOT_CLASS_MINOR_ID, BE_CLASS_MINOR_ID),
	}
	var errs []error

	for ipsetName, classid := range ipsetToClassid {
		err := p.iptablesHandler.DeleteIfExists("mangle", "POSTROUTING",
			"-m", "set", "--match-set", string(ipsetName), "src",
			"-j", "CLASSIFY", "--set-class", classid)
		errs = append(errs, err)
	}

	return apierror.NewAggregate(errs)
}

func (p *tcPlugin) iptablesExisted() (bool, error) {
	ipsetToClassid := map[NetQoSClass]string{
		NETQoSSystem: convertToClassId(ROOT_CLASS_MINOR_ID, SYSTEM_CLASS_MINOR_ID),
		NETQoSLS:     convertToClassId(ROOT_CLASS_MINOR_ID, LS_CLASS_MINOR_ID),
		NETQoSBE:     convertToClassId(ROOT_CLASS_MINOR_ID, BE_CLASS_MINOR_ID),
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
