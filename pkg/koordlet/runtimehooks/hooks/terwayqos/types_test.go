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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarshalTextWithAllFieldsSet(t *testing.T) {
	node := Node{
		HwTxBpsMax: 100,
		HwRxBpsMax: 200,
		L1TxBpsMin: 300,
		L1TxBpsMax: 400,
		L1RxBpsMin: 500,
		L1RxBpsMax: 600,
		L2TxBpsMin: 700,
		L2TxBpsMax: 800,
		L2RxBpsMin: 900,
		L2RxBpsMax: 1000,
	}

	expected := "hw_tx_bps_max 100\nhw_rx_bps_max 200\noffline_l1_tx_bps_min 300\noffline_l1_tx_bps_max 400\noffline_l1_rx_bps_min 500\noffline_l1_rx_bps_max 600\noffline_l2_tx_bps_min 700\noffline_l2_tx_bps_max 800\noffline_l2_rx_bps_min 900\noffline_l2_rx_bps_max 1000\n"

	result, err := node.MarshalText()

	assert.NoError(t, err)
	assert.Equal(t, expected, string(result))
}

func TestMarshalTextWithNoFieldsSet(t *testing.T) {
	node := Node{}

	expected := "hw_tx_bps_max 0\nhw_rx_bps_max 0\noffline_l1_tx_bps_min 0\noffline_l1_tx_bps_max 0\noffline_l1_rx_bps_min 0\noffline_l1_rx_bps_max 0\noffline_l2_tx_bps_min 0\noffline_l2_tx_bps_max 0\noffline_l2_rx_bps_min 0\noffline_l2_rx_bps_max 0\n"

	result, err := node.MarshalText()

	assert.NoError(t, err)
	assert.Equal(t, expected, string(result))
}

func TestMarshalTextWithSomeFieldsSet(t *testing.T) {
	node := Node{
		HwTxBpsMax: 100,
		L1TxBpsMin: 300,
		L2RxBpsMax: 1000,
	}

	expected := "hw_tx_bps_max 100\nhw_rx_bps_max 0\noffline_l1_tx_bps_min 300\noffline_l1_tx_bps_max 0\noffline_l1_rx_bps_min 0\noffline_l1_rx_bps_max 0\noffline_l2_tx_bps_min 0\noffline_l2_tx_bps_max 0\noffline_l2_rx_bps_min 0\noffline_l2_rx_bps_max 1000\n"

	result, err := node.MarshalText()

	assert.NoError(t, err)
	assert.Equal(t, expected, string(result))
}
