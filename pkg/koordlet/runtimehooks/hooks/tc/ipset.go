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

	"github.com/vishvananda/netlink"
	apierror "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
)

func (p *tcPlugin) ipsetExisted() (bool, error) {
	var errs []error
	for _, cur := range ipsets {
		if _, err := netlink.IpsetList(cur); err != nil {
			errs = append(errs, err)
		}
	}

	if apierror.NewAggregate(errs) != nil {
		return false, apierror.NewAggregate(errs)
	}

	return true, nil
}

func (p *tcPlugin) EnsureIpset() error {
	klog.V(5).Infof("start to create ipset.")
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

	return apierror.NewAggregate(errs)
}

func (p *tcPlugin) DestoryIpset() error {
	klog.V(5).Infof("start to delete ipset rules created by tc plugin.")
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

	return apierror.NewAggregate(errs)
}
