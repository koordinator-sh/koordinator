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
	"bytes"
	"fmt"
	"reflect"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

type NetworkQoS struct {
	// IngressLimit and EgressLimit is the bandwidth in bps
	// are used to set bandwidth for Pod. The unit is bps.
	// For example, 10M means 10 megabits per second.
	IngressLimit string `json:"ingressLimit"`
	EgressLimit  string `json:"egressLimit"`
}

type QoS struct {
	IngressRequestBps uint64 `json:"ingressRequestBps"`
	IngressLimitBps   uint64 `json:"ingressLimitBps"`
	EgressRequestBps  uint64 `json:"egressRequestBps"`
	EgressLimitBps    uint64 `json:"egressLimitBps"`
}

type Node struct {
	HwTxBpsMax uint64 `text:"hw_tx_bps_max"`
	HwRxBpsMax uint64 `text:"hw_rx_bps_max"`
	L1TxBpsMin uint64 `text:"offline_l1_tx_bps_min"`
	L1TxBpsMax uint64 `text:"offline_l1_tx_bps_max"`
	L1RxBpsMin uint64 `text:"offline_l1_rx_bps_min"`
	L1RxBpsMax uint64 `text:"offline_l1_rx_bps_max"`
	L2TxBpsMin uint64 `text:"offline_l2_tx_bps_min"`
	L2TxBpsMax uint64 `text:"offline_l2_tx_bps_max"`
	L2RxBpsMin uint64 `text:"offline_l2_rx_bps_min"`
	L2RxBpsMax uint64 `text:"offline_l2_rx_bps_max"`
}

func (n Node) MarshalText() (text []byte, err error) {
	val := reflect.ValueOf(n)
	typ := val.Type()

	var buffer bytes.Buffer

	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		tagValue := field.Tag.Get("text")
		if tagValue == "" {
			continue // Skip fields without text tag
		}

		fieldValue := val.Field(i)
		line := fmt.Sprintf("%s %v\n", tagValue, fieldValue.Interface())
		buffer.WriteString(line)
	}

	return buffer.Bytes(), nil
}

type Pod struct {
	PodName      string    `json:"podName"`
	PodNamespace string    `json:"podNamespace"`
	PodUID       string    `json:"podUID"`
	Prio         int       `json:"prio"`
	CgroupDir    string    `json:"cgroupDir"`
	QoSConfig    QoSConfig `json:"qosConfig"`
}

type QoSConfig struct {
	IngressBandwidth uint64 `json:"ingressBandwidth"`
	EgressBandwidth  uint64 `json:"egressBandwidth"`
}

var prioMapping = map[string]int{
	string(extension.QoSSystem): 0,
	string(extension.QoSLSE):    1,
	string(extension.QoSLSR):    1,
	string(extension.QoSLS):     1,
	string(extension.QoSBE):     2,
}
