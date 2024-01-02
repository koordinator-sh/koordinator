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

package extension

import corev1 "k8s.io/api/core/v1"

type NetQosGlobalConfig struct {
	HwTxBpsMax uint64 `json:"hw_tx_bps_max"`
	HwRxBpsMax uint64 `json:"hw_rx_bps_max"`
	L1TxBpsMin uint64 `json:"l1_tx_bps_min"`
	L1TxBpsMax uint64 `json:"l1_tx_bps_max"`
	L2TxBpsMin uint64 `json:"l2_tx_bps_min"`
	L2TxBpsMax uint64 `json:"l2_tx_bps_max"`
	L1RxBpsMin uint64 `json:"l1_rx_bps_min"`
	L1RxBpsMax uint64 `json:"l1_rx_bps_max"`
	L2RxBpsMin uint64 `json:"l2_rx_bps_min"`
	L2RxBpsMax uint64 `json:"l2_rx_bps_max"`
}

type NetQoSClass string

const (
	NETQoSHigh NetQoSClass = "high_class"
	NETQoSMid  NetQoSClass = "mid_class"
	NETQoSLow  NetQoSClass = "low_class"
	NETQoSNone NetQoSClass = ""

	NETQOSConfigPathForNode = "/var/run/koordinator/net/node"
	NETQOSConfigPathForPod  = "/var/run/koordinator/net/pods"
)

func GetPodNetQoSClassByName(qos string) NetQoSClass {
	q := QoSClass(qos)

	switch q {
	case QoSSystem:
		return NETQoSHigh
	case QoSLSE, QoSLSR, QoSLS:
		return NETQoSMid
	case QoSBE:
		return NETQoSLow
	}

	return NETQoSNone
}

func GetPodNetQoSClass(pod *corev1.Pod) NetQoSClass {
	if pod == nil || pod.Labels == nil {
		return NETQoSNone
	}
	return GetNetQoSClassByAttrs(pod.Labels, pod.Annotations)
}

func GetNetQoSClassByAttrs(labels, annotations map[string]string) NetQoSClass {
	// annotations are for old format adaption reason
	if q, exist := labels[LabelPodQoS]; exist {
		return GetPodNetQoSClassByName(q)
	}
	return NETQoSNone
}
