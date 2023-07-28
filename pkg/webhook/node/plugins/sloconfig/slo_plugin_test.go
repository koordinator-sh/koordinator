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
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrladmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func Test_Validate(t *testing.T) {
	type args struct {
		req       ctrladmission.Request
		configMap *corev1.ConfigMap
		oldNode   *corev1.Node
		node      *corev1.Node
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "test skip :update node, config invalid",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ResourceThresholdConfigKey: "invalid_content",
						configuration.ResourceQOSConfigKey:       "invalid_content",
						configuration.CPUBurstConfigKey:          "invalid_content",
						configuration.SystemConfigKey:            "invalid_content",
					},
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test not conflict:update node, config colocation not config",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						configuration.ResourceQOSConfigKey: `
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
						configuration.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						configuration.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test not conflict:update node, config have no nodeStrategies",
			args: args{
				req: ctrladmission.Request{
					AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
						configuration.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						configuration.ResourceQOSConfigKey: `
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
						configuration.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						configuration.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test not conflict:update node,cpuBurst empty,other config have no nodeStrategies",
			args: args{
				req: ctrladmission.Request{
					AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
						configuration.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						configuration.ResourceQOSConfigKey: `
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
						configuration.SystemConfigKey: "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test not conflict:update node, cpuburst is empty and other config have nodeStrategies but not conflict",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"yyy\":\"yyy\"}},\"enable\":true}]}",
						configuration.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						configuration.ResourceQOSConfigKey: `
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
						configuration.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						configuration.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx"}, // node
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test conflict:update node, config have nodeStrategies conflict",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"yyy\":\"yyy\"}},\"enable\":true}]}",
						configuration.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						configuration.ResourceQOSConfigKey: `
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
						configuration.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						configuration.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx", "yyy": "yyy"}, //conflict node
					},
				},
			},
			wantErr: true,
		},
		{
			name: "test conflict:update node, cpuburst invalid ,other config have nodeStrategies conflict",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"yyy\":\"yyy\"}},\"enable\":true}]}",
						configuration.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						configuration.ResourceQOSConfigKey: `
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
						configuration.SystemConfigKey: "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx", "yyy": "yyy"}, //conflict node
					},
				},
			},
			wantErr: true,
		},
		{
			name: "test conflict:update node, node labels not changed",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"yyy\":\"yyy\"}},\"enable\":true}]}",
						configuration.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						configuration.ResourceQOSConfigKey: `
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
						configuration.CPUBurstConfigKey: "{\"clusterStrategy\":{\"cfsQuotaBurstPeriodSeconds\":60}}",
						configuration.SystemConfigKey:   "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				oldNode: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx", "yyy": "yyy"},
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx", "yyy": "yyy"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "test just log conflict for create node, cpuburst invalid ,other config have nodeStrategies conflict",
			args: args{
				req: ctrladmission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Resource: metav1.GroupVersionResource{Resource: "nodes"}}},
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
						configuration.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"yyy\":\"yyy\"}},\"enable\":true}]}",
						configuration.ResourceThresholdConfigKey: "{\"clusterStrategy\":{\"enable\":true,\"cpuSuppressThresholdPercent\":60}}",
						configuration.ResourceQOSConfigKey: `
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
						configuration.SystemConfigKey: "{\"clusterStrategy\":{\"minFreeKbytesFactor\":150}}",
					},
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-node0",
						Labels: map[string]string{"xxx": "xxx", "yyy": "yyy"}, //conflict node
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := NewPlugin(nil, fake.NewClientBuilder().Build())
			err := plugin.client.Create(context.Background(), tt.args.configMap)
			assert.NoError(t, err)
			gotErr := plugin.Validate(context.Background(), tt.args.req, tt.args.node, tt.args.oldNode)
			fmt.Println(gotErr)
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}
