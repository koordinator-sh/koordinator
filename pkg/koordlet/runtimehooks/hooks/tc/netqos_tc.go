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
	"github.com/koordinator-sh/koordinator/apis/extension"
	corev1 "k8s.io/api/core/v1"
)

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
	NETQoSSystem NetQoSClass = "system_class"
	NETQoSLS     NetQoSClass = "ls_class"
	NETQoSBE     NetQoSClass = "be_class"
	NETQoSNone   NetQoSClass = ""
)

func GetPodNetQoSClassByName(qos string) NetQoSClass {
	q := extension.QoSClass(qos)

	switch q {
	case extension.QoSSystem:
		return NETQoSSystem
	case extension.QoSLSE, extension.QoSLSR, extension.QoSLS:
		return NETQoSLS
	case extension.QoSBE:
		return NETQoSBE
	}

	return NETQoSNone
}

func GetClassIdByNetQos(qos NetQoSClass) string {
	m := map[NetQoSClass]string{
		NETQoSSystem: convertToHexClassId(MAJOR_ID, SYSTEM_CLASS_MINOR_ID),
		NETQoSLS:     convertToHexClassId(MAJOR_ID, LS_CLASS_MINOR_ID),
		NETQoSBE:     convertToHexClassId(MAJOR_ID, BE_CLASS_MINOR_ID),
		NETQoSNone:   convertToHexClassId(MAJOR_ID, SYSTEM_CLASS_MINOR_ID),
	}

	return m[qos]
}

func GetPodNetQoSClass(pod *corev1.Pod) NetQoSClass {
	if pod == nil || pod.Labels == nil {
		return NETQoSNone
	}
	return GetNetQoSClassByAttrs(pod.Labels, pod.Annotations)
}

func GetNetQoSClassByAttrs(labels, annotations map[string]string) NetQoSClass {
	// annotations are for old format adaption reason
	if q, exist := labels[extension.LabelPodQoS]; exist {
		return GetPodNetQoSClassByName(q)
	}
	return NETQoSNone
}
