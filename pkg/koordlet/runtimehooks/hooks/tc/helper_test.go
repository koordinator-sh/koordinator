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
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func Test_convertToHexClassId(t *testing.T) {
	type args struct {
		major int
		minor int
	}
	tests := []struct {
		name string
		args args
		want uint32
	}{
		// TODO: Add test cases.
		{
			name: "",
			args: args{
				major: 11,
				minor: 2,
			},
			want: 1114114,
		},
		{
			name: "",
			args: args{
				major: 1,
				minor: 2222,
			},
			want: 74274,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, convertToHexClassId(tt.args.major, tt.args.minor), "convertToHexClassId(%v, %v)", tt.args.major, tt.args.minor)
		})
	}
}

func genVal(in intstr.IntOrString) *intstr.IntOrString {
	return &in
}

func Test_loadConfigFromNodeSlo(t *testing.T) {
	type args struct {
		nodesloSpec *slov1alpha1.NodeSLOSpec
	}

	tests := []struct {
		name string
		args args
		want *NetQosGlobalConfig
	}{
		// TODO: Add test cases.
		{
			name: "nodeslo.spec is nil",
			args: args{
				nodesloSpec: &slov1alpha1.NodeSLOSpec{},
			},
			want: &NetQosGlobalConfig{},
		},
		{
			name: "network qos is nil",
			args: args{
				nodesloSpec: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								Enable: pointer.Bool(true),
							},
						},
					},
				},
			},
			want: &NetQosGlobalConfig{},
		},
		{
			name: "network config not enable to be set",
			args: args{
				nodesloSpec: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								Enable: pointer.Bool(false),
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: genVal(intstr.FromInt(10)),
								},
							},
						},
					},
					SystemStrategy: &slov1alpha1.SystemStrategy{
						TotalNetworkBandwidth: resource.MustParse("100M"),
					},
				},
			},
			want: &NetQosGlobalConfig{
				HwTxBpsMax: 100000000,
				HwRxBpsMax: 100000000,
			},
		},
		{
			name: "total network bandwidth not been set",
			args: args{
				nodesloSpec: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								Enable: pointer.Bool(true),
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: genVal(intstr.FromInt(10)),
								},
							},
						},
					},
				},
			},
			want: &NetQosGlobalConfig{},
		},
		{
			name: "get network config from a int value",
			args: args{
				nodesloSpec: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								Enable: pointer.Bool(true),
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: genVal(intstr.FromInt(10)),
								},
							},
						},
					},
					SystemStrategy: &slov1alpha1.SystemStrategy{
						TotalNetworkBandwidth: resource.MustParse("100M"),
					},
				},
			},
			want: &NetQosGlobalConfig{
				HwTxBpsMax: 100000000,
				HwRxBpsMax: 100000000,
				L1RxBpsMin: 10000000,
			},
		},
		{
			name: "get network config from a string value",
			args: args{
				nodesloSpec: &slov1alpha1.NodeSLOSpec{
					ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
						LSClass: &slov1alpha1.ResourceQOS{
							NetworkQOS: &slov1alpha1.NetworkQOSCfg{
								Enable: pointer.Bool(true),
								NetworkQOS: slov1alpha1.NetworkQOS{
									IngressRequest: genVal(intstr.FromString("10M")),
								},
							},
						},
					},
					SystemStrategy: &slov1alpha1.SystemStrategy{
						TotalNetworkBandwidth: resource.MustParse("100M"),
					},
				},
			},
			want: &NetQosGlobalConfig{
				HwTxBpsMax: 100000000,
				HwRxBpsMax: 100000000,
				L1RxBpsMin: 10000000,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, loadConfigFromNodeSlo(tt.args.nodesloSpec), "loadConfigFromNodeSlo(%v)", tt.args.nodesloSpec)
		})
	}
}

func Test_convertIpToHex(t *testing.T) {
	type args struct {
		ip string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		// TODO: Add test cases.
		{
			name: "legal ip",
			args: args{
				ip: "10.211.248.149",
			},
			want: "0ad3f895",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, convertIpToHex(tt.args.ip), "convertIpToHex(%v)", tt.args.ip)
		})
	}
}

func Test_getBandwidthVal(t *testing.T) {
	type args struct {
		total        uint64
		intOrPercent *intstr.IntOrString
	}
	tests := []struct {
		name string
		args args
		want uint64
	}{
		// TODO: Add test cases.
		{
			name: "input is nil",
			args: args{
				total:        1000,
				intOrPercent: nil,
			},
			want: 0,
		},
		{
			name: "percentage value less than 0",
			args: args{
				total:        1000,
				intOrPercent: genVal(intstr.FromInt(-1)),
			},
			want: 0,
		},
		{
			name: "percentage value is zero",
			args: args{
				total:        1000,
				intOrPercent: genVal(intstr.FromInt(0)),
			},
			want: 0,
		},
		{
			name: "percentage value over 100",
			args: args{
				total:        1000,
				intOrPercent: genVal(intstr.FromInt(200)),
			},
			want: 0,
		},
		{
			name: "valid percentage",
			args: args{
				total:        1000,
				intOrPercent: genVal(intstr.FromInt(10)),
			},
			want: 100,
		},
		{
			name: "invalid quantity format",
			args: args{
				total:        1000,
				intOrPercent: genVal(intstr.FromString("aaa")),
			},
			want: 0,
		},
		{
			name: "quantity string is nil",
			args: args{
				total:        1000,
				intOrPercent: genVal(intstr.FromString("")),
			},
			want: 0,
		},
		{
			name: "valid string format",
			args: args{
				total:        10000,
				intOrPercent: genVal(intstr.FromString("2k")),
			},
			want: 2000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, getBandwidthVal(tt.args.total, tt.args.intOrPercent), "getBandwidthVal(%v, %v)", tt.args.total, tt.args.intOrPercent)
		})
	}
}
