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
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func loadConfigFromNodeSlo(nodesloSpec *slov1alpha1.NodeSLOSpec) *NetQosGlobalConfig {
	res := NetQosGlobalConfig{}
	var total uint64 = 0
	if nodesloSpec != nil && nodesloSpec.SystemStrategy != nil {
		total = uint64(nodesloSpec.SystemStrategy.TotalNetworkBandwidth.Value())
		res.HwRxBpsMax = total
		res.HwTxBpsMax = total
	}

	if nodesloSpec.ResourceQOSStrategy == nil {
		return &res
	}

	strategy := nodesloSpec.ResourceQOSStrategy
	if strategy.LSClass != nil &&
		strategy.LSClass.NetworkQOS != nil &&
		*strategy.LSClass.NetworkQOS.Enable {
		cur := strategy.LSClass.NetworkQOS
		res.L1RxBpsMin = getBandwidthVal(total, cur.IngressRequest)
		res.L1RxBpsMax = getBandwidthVal(total, cur.IngressLimit)
		res.L1TxBpsMin = getBandwidthVal(total, cur.EgressRequest)
		res.L1TxBpsMax = getBandwidthVal(total, cur.EgressLimit)
	}

	if strategy.BEClass != nil &&
		strategy.BEClass.NetworkQOS != nil &&
		*strategy.BEClass.NetworkQOS.Enable {
		cur := strategy.BEClass.NetworkQOS
		res.L2RxBpsMin = getBandwidthVal(total, cur.IngressRequest)
		res.L2RxBpsMax = getBandwidthVal(total, cur.IngressLimit)
		res.L2TxBpsMin = getBandwidthVal(total, cur.EgressRequest)
		res.L2TxBpsMax = getBandwidthVal(total, cur.EgressLimit)
	}

	return &res
}

func getBandwidthVal(total uint64, intOrPercent *intstr.IntOrString) uint64 {
	if intOrPercent == nil {
		return 0
	}

	switch intOrPercent.Type {
	case intstr.String:
		return getBandwidthByQuantityFormat(intOrPercent.StrVal)
	case intstr.Int:
		return getBandwidthByPercentageFormat(total, intOrPercent.IntValue())
	default:
		return 0
	}
}

func getBandwidthByQuantityFormat(quanityStr string) uint64 {
	val, err := resource.ParseQuantity(quanityStr)
	if err != nil {
		return 0
	}

	return uint64(val.Value())
}

func getBandwidthByPercentageFormat(total uint64, percentage int) uint64 {
	if percentage < 0 || percentage > 100 {
		return 0
	}

	return total * uint64(percentage) / 100
}

func convertToClassId(major, minor int) string {
	return fmt.Sprintf("%d:%d", major, minor)
}

// convertToHexClassId get class id in hex.
func convertToHexClassId(major, minor int) uint32 {
	hexVal := fmt.Sprintf("%d%04d", major, minor)
	decimalVal, _ := strconv.ParseUint(hexVal, 16, 32)
	return uint32(decimalVal)
}

// convertIpToHex convert ip to it's hex format
// 10.211.248.149 => 0ad3f895
func convertIpToHex(ip string) string {
	result := ""
	elems := strings.Split(ip, ".")
	for _, elem := range elems {
		cur, _ := strconv.Atoi(elem)
		hex := fmt.Sprintf("%x", cur)
		// each ip segment takes up two hexadecimal digits, and when it does not take up all the bits,
		// it needs to be filled with 0.
		for i := 0; i < 2-len(hex); i++ {
			hex = "0" + hex
		}
		result += hex
	}

	return result
}
