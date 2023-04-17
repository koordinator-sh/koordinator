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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
)

func Test_CreateCheckersChanged_and_CheckContents(t *testing.T) {
	type args struct {
		oldConfig *corev1.ConfigMap
		newConfig *corev1.ConfigMap
	}

	tests := []struct {
		name                 string
		args                 args
		wantCheckersNum      int
		wantCheckContentsErr bool
	}{
		{
			name: "new config empty",
			args: args{
				oldConfig: &corev1.ConfigMap{
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
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{},
				},
			},
			wantCheckersNum:      0,
			wantCheckContentsErr: false,
		},
		{
			name: "config invalid but not changed",
			args: args{
				oldConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ResourceThresholdConfigKey: "invalid_content",
						extension.ResourceQOSConfigKey:       "invalid_content",
						extension.CPUBurstConfigKey:          "invalid_content",
						extension.SystemConfigKey:            "invalid_content",
						extension.ColocationConfigKey:        "invalid_content",
					},
				},
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ResourceThresholdConfigKey: "invalid_content",
						extension.ResourceQOSConfigKey:       "invalid_content",
						extension.CPUBurstConfigKey:          "invalid_content",
						extension.SystemConfigKey:            "invalid_content",
						extension.ColocationConfigKey:        "invalid_content",
					},
				},
			},
			wantCheckersNum:      0,
			wantCheckContentsErr: false,
		},
		{
			name: "config changed but invalid",
			args: args{
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ResourceThresholdConfigKey: "invalid_content",
						extension.ResourceQOSConfigKey:       "invalid_content",
						extension.CPUBurstConfigKey:          "invalid_content",
						extension.SystemConfigKey:            "invalid_content",
						extension.ColocationConfigKey:        "invalid_content",
					},
				},
			},
			wantCheckersNum:      5,
			wantCheckContentsErr: true,
		},
		{
			name: " colocation metricAggregateDurationSeconds invalid",
			args: args{
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":-1," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1}",
					},
				},
			},
			wantCheckersNum:      1,
			wantCheckContentsErr: true,
		},
		{
			name: " colocation nodeStrategies name invalid",
			args: args{
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true}]}",
					},
				},
			},
			wantCheckersNum:      1,
			wantCheckContentsErr: true,
		},
		{
			name: " colocation nodeStrategies overlap",
			args: args{
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true,\"name\":\"test1\"},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\",\"yyy\":\"yyy\"}},\"enable\":true,\"name\":\"test2\"}]}",
					},
				},
			},
			wantCheckersNum:      1,
			wantCheckContentsErr: true,
		},
		{
			name: "config valid and changed",
			args: args{
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":100," +
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
			wantCheckersNum:      5,
			wantCheckContentsErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := CreateCheckersChanged(tt.args.oldConfig, tt.args.newConfig)
			assert.Equal(t, tt.wantCheckersNum, len(cs))
			gotCheckErr := cs.CheckConfigContents()
			assert.Equal(t, tt.wantCheckContentsErr, gotCheckErr != nil, "gotCheckErr:%s", gotCheckErr)
		})
	}
}

func Test_NeedCheckForNodes(t *testing.T) {
	type args struct {
		newConfig *corev1.ConfigMap
	}

	tests := []struct {
		name               string
		args               args
		wantNeedCheckNodes bool
	}{
		{
			name: " only colocation have one nodeStrategy",
			args: args{
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true,\"name\":\"test1\"}]}",
					},
				},
			},
			wantNeedCheckNodes: false,
		},
		{
			name: " colocation nodeStrategies have MultiNodeStrategies",
			args: args{
				newConfig: &corev1.ConfigMap{
					Data: map[string]string{
						extension.ColocationConfigKey: "{\"enable\":true,\"metricAggregateDurationSeconds\":60," +
							"\"cpuReclaimThresholdPercent\":70,\"memoryReclaimThresholdPercent\":80,\"updateTimeThresholdSeconds\":300," +
							"\"degradeTimeMinutes\":5,\"resourceDiffThreshold\":0.1,\"nodeConfigs\":[{\"nodeSelector\":" +
							"{\"matchLabels\":{\"xxx\":\"xxx\"}},\"enable\":true,\"name\":\"test1\"},{\"nodeSelector\":" +
							"{\"matchLabels\":{\"yyy\":\"yyy\"}},\"enable\":true,\"name\":\"test2\"}]}",
					},
				},
			},
			wantNeedCheckNodes: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := CreateCheckersChanged(nil, tt.args.newConfig)
			gotNeedCheckNodes := cs.NeedCheckForNodes()
			assert.Equal(t, tt.wantNeedCheckNodes, gotNeedCheckNodes)
		})
	}
}
