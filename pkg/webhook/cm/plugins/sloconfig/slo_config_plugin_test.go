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
package sloconfig

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrladmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func Test_Validate(t *testing.T) {
	type args struct {
		req          ctrladmission.Request
		oldConfigMap *corev1.ConfigMap
		configMap    *corev1.ConfigMap
		nodeList     []*corev1.Node
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "test delete slo-manager-config",
			args: args{
				req: ctrladmission.Request{
					AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Delete, Resource: metav1.GroupVersionResource{Resource: "configmaps"}}},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
						extension.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						extension.ResourceQOSConfigKey: `
{
  "clusterStrategy": {
    "enable": true,
    "beClass": {
      "cpuQOS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
						extension.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						extension.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				nodeList: []*corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "test-node0",
							Labels: map[string]string{"xxx": "xxx"},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "test-node1",
							Labels: map[string]string{"yyy": "yyy"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test create and config invalid",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Resource: metav1.GroupVersionResource{Resource: "configmaps"}}},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ResourceThresholdConfigKey: "invalid_content",
						extension.ResourceQOSConfigKey:       "invalid_content",
						extension.CPUBurstConfigKey:          "invalid_content",
						extension.SystemConfigKey:            "invalid_content",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "test create and colocation not config",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Resource: metav1.GroupVersionResource{Resource: "configmaps"}}},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						extension.ResourceQOSConfigKey: `
{
  "clusterStrategy": {
    "enable": true,
    "beClass": {
      "cpuQOS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
						extension.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						extension.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test create and colocation metricAggregateDurationSeconds invalid",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Resource: metav1.GroupVersionResource{Resource: "configmaps"}}},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":-1," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
						extension.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						extension.ResourceQOSConfigKey: `
{
  "clusterStrategy": {
    "enable": true,
    "beClass": {
      "cpuQOS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
						extension.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						extension.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "test create and config only clusterStrategy valid",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Resource: metav1.GroupVersionResource{Resource: "configmaps"}}},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
						extension.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						extension.ResourceQOSConfigKey: `
{
  "clusterStrategy": {
    "enable": true,
    "beClass": {
      "cpuQOS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
						extension.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						extension.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				nodeList: []*corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "test-node0",
							Labels: map[string]string{"xxx": "xxx"},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "test-node1",
							Labels: map[string]string{"yyy": "yyy"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test create and config colocation nodeStrategies overlap",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Resource: metav1.GroupVersionResource{Resource: "configmaps"}}},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true,\"name\":\"test1\"},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\",\"yyy\":\"yyy\"}},\"enable\":true,\"name\":\"test2\"}]}",
						extension.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						extension.ResourceQOSConfigKey: `
{
  "clusterStrategy": {
    "enable": true,
    "beClass": {
      "cpuQOS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
						extension.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						extension.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "test create and config colocation nodeStrategies have node conflict",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Resource: metav1.GroupVersionResource{Resource: "configmaps"}}},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true,\"name\":\"test1\"},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"yyy\":\"yyy\"}},\"enable\":true,\"name\":\"test2\"}]}",
						extension.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						extension.ResourceQOSConfigKey: `
{
  "clusterStrategy": {
    "enable": true,
    "beClass": {
      "cpuQOS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
						extension.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						extension.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				nodeList: []*corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "test-node0",
							Labels: map[string]string{"xxx": "xxx", "yyy": "yyy"}, //conflict node
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "test-node1",
							Labels: map[string]string{"yyy": "yyy"},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "test update and config not Changed",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "configmaps"}}},
				oldConfigMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
						extension.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						extension.ResourceQOSConfigKey: `
{
  "clusterStrategy": {
    "enable": true,
    "beClass": {
      "cpuQOS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
						extension.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						extension.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				configMap: &corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ConfigMap",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      sloconfig.SLOCtrlConfigMap,
						Namespace: sloconfig.ConfigNameSpace,
					},
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
						extension.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						extension.ResourceQOSConfigKey: `
{
  "clusterStrategy": {
    "enable": true,
    "beClass": {
      "cpuQOS": {
        "groupIdentity": 0
      }
    }
  }
}
`,
						extension.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						extension.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				nodeList: []*corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "test-node0",
							Labels: map[string]string{"xxx": "xxx"},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "test-node1",
							Labels: map[string]string{"yyy": "yyy"},
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := NewPlugin(nil, fake.NewClientBuilder().Build())
			for _, node := range tt.args.nodeList {
				err := plugin.client.Create(context.Background(), node)
				assert.NoError(t, err)
			}
			gotErr := plugin.Validate(context.Background(), tt.args.req, tt.args.configMap, tt.args.oldConfigMap)
			assert.Equal(t, tt.wantErr, gotErr != nil, "gotErr:%v", gotErr)
		})
	}
}

func Test_parallelizeCheckNode(t *testing.T) {
	type args struct {
		nodeConfigs []extension.NodeCfgProfile
		numNodes    int
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "node config not conflict:5 nodes,strategy1(1:1),strategy2(2:2)",
			args: args{
				nodeConfigs: []extension.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"1": "1",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"2": "2",
							},
						},
					},
				},
				numNodes: 5,
			},
			wantErr: false,
		},
		{
			name: "node config not conflict:500 nodes,strategy1(1:1),strategy2(2:2)",
			args: args{
				nodeConfigs: []extension.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"1": "1",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"2": "2",
							},
						},
					},
				},
				numNodes: 500,
			},
			wantErr: false,
		},
		{
			name: "node config conflict:500 nodes,strategy1(name:30),strategy2(30:30)",
			args: args{
				nodeConfigs: []extension.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"30": "30",
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"name": "30",
							},
						},
					},
				},
				numNodes: 500,
			},
			wantErr: true,
		},
		{
			name: "node config conflict:500 nodes,strategy1(name in (50,51)),strategy2(name:50)",
			args: args{
				nodeConfigs: []extension.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "name",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"50", "51"},
								},
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"name": "50",
							},
						},
					},
				},
				numNodes: 500,
			},
			wantErr: true,
		},
		{
			name: "multi node config conflict:500 nodes,strategy1,strategy2",
			args: args{
				nodeConfigs: []extension.NodeCfgProfile{
					{
						Name: "strategy1",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "name",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"2", "50", "51", "100", "300", "400"},
								},
							},
						},
					},
					{
						Name: "strategy2",
						NodeSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "name",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"2", "50", "51", "100", "300", "400"},
								},
							},
						},
					},
				},
				numNodes: 500,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeList := makeNodes(tt.args.numNodes)
			checker, _ := CreateNodeConfigProfileChecker(extension.ColocationConfigKey, func() []extension.NodeCfgProfile {
				return tt.args.nodeConfigs
			})
			colocationChecker := &ColocationConfigChecker{CommonChecker: CommonChecker{configKey: extension.ColocationConfigKey, NodeConfigProfileChecker: checker}}
			gotErr := parallelizeCheckNode(context.Background(), nodeList, checkers{colocationChecker})
			assert.Equal(t, tt.wantErr, gotErr != nil)
			if gotErr != nil {
				fmt.Println(gotErr)
			}
		})
	}
}

func makeNodes(nodesNum int) []corev1.Node {
	result := make([]corev1.Node, 0, nodesNum)
	for i := 0; i < nodesNum; i++ {
		name := strconv.Itoa(i)
		result = append(result, corev1.Node{ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{name: name, "name": name},
		}})
	}
	return result
}
