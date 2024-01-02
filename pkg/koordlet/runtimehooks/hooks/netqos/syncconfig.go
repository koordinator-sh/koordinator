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

package netbandwidth

import (
	"encoding/json"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

// SyncNetQosCfgFromNodeSlo sync net qos configuration from nodeslo cr.
func SyncNetQosCfgFromNodeSlo(nodeslo *v1alpha1.NodeSLO) error {
	linkInfo, err := system.GetLinkInfoByDefaultRoute()
	if err != nil || linkInfo == nil {
		klog.Errorf("failed to get link info by default route. err=%v\n", err)
		return nil
	}
	speed, err := system.GetSpeed(linkInfo.Attrs().Name)
	if err != nil {
		klog.Errorf("failed to get speed by interface(%s). err=%v\n", linkInfo.Attrs().Name, err)
		return nil
	}
	// the value is Mbps, transfer to bps
	speed = speed * 1000 * 1000

	getNetSpeed := func(val string) uint64 {
		var speed uint64 = 0

		if '0' <= val[len(val)-1] && val[len(val)-1] <= '9' {
			if percent, err := strconv.ParseUint(val, 10, 64); err == nil {
				speed = speed * percent / 100
			}
		} else {
			res := resource.MustParse(val)
			speed = uint64(res.Value())
		}

		return speed
	}

	maxCeil := uint64(speed)
	cfg := extension.NetQosGlobalConfig{
		HwTxBpsMax: maxCeil,
		HwRxBpsMax: maxCeil,
		L1TxBpsMin: getNetSpeed(nodeslo.Spec.ResourceQOSStrategy.LSClass.NetworkQOS.EgressRequest.String()),
		L1TxBpsMax: maxCeil,
		L2TxBpsMin: getNetSpeed(nodeslo.Spec.ResourceQOSStrategy.BEClass.NetworkQOS.EgressRequest.String()),
		L2TxBpsMax: maxCeil,
		L1RxBpsMin: getNetSpeed(nodeslo.Spec.ResourceQOSStrategy.LSClass.NetworkQOS.IngressRequest.String()),
		L1RxBpsMax: maxCeil,
		L2RxBpsMin: getNetSpeed(nodeslo.Spec.ResourceQOSStrategy.BEClass.NetworkQOS.IngressRequest.String()),
		L2RxBpsMax: maxCeil,
	}

	configData, _ := json.MarshalIndent(cfg, "", "\t")
	if err := system.CommonFileWrite(extension.NETQOSConfigPathForNode, string(configData)); err != nil {
		return err
	}

	return nil
}
