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

package system

const (
	GoldNetClsID   = 0xab5a2010 // net_priority(3)
	GoldDSCP       = 18
	SilverNetClsID = 0xab5a2020 // net_priority(5)
	SilverDSCP     = 17
	CopperNetClsID = 0xab5a2030 // net_priority(7)
	CopperDSCP     = 16
)

type BandwidthConfig struct {
	GatewayIfaceName string
	GatewayLimit     uint64

	RootLimit uint64

	GoldRequest uint64
	GoldLimit   uint64
	GoldDSCP    uint64

	SilverRequest uint64
	SilverLimit   uint64
	SilverDSCP    uint64

	CopperRequest uint64
	CopperLimit   uint64
	CopperDSCP    uint64
}
