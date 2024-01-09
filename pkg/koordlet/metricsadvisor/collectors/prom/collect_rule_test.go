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

package prom

import (
	"os"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestLoadCollectRuleFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	tests := []struct {
		name      string
		prepareFn func(helper *system.FileTestUtil)
		arg       string
		want      *CollectRule
		wantErr   bool
	}{
		{
			name:    "file not exist",
			arg:     "/unknown-path-xxx",
			wantErr: true,
		},
		{
			name: "file parse correctly",
			prepareFn: func(helper *system.FileTestUtil) {
				_ = os.WriteFile(tmpDir+"/test-path", []byte(`
---
enable: true
jobs:
  - name: job-0
    enable: true
    address: "127.0.0.1"
    port: 8000
    path: /metrics
    scrapeInterval: 20s
    scrapeTimeout: 5s
    metrics:
    - sourceName: metric_a
      targetName: metric_a
      targetType: "node"
    - sourceName: metric_b
      sourceLabels:
        bbb: aaa
      targetName: metric_b
      targetType: "node"
  - name: job-1
    enable: true
    address: "127.0.0.1"
    port: 9316
    path: /metrics
    scrapeInterval: 30s
    scrapeTimeout: 10s
    metrics:
    - sourceName: metric_c
      targetName: metric_c
      targetType: "pod"
  - name: job-2
    enable: false
    address: "127.0.0.1"
    port: 9090
    path: /default-metrics
    scrapeInterval: 30s
    scrapeTimeout: 10s
    metrics:
    - sourceName: metric_d
      targetName: metric_d
      targetType: "pod"
`), 0644)
			},
			arg:     tmpDir + "/test-path",
			wantErr: false,
			want: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:           "job-0",
						Enable:         true,
						Address:        "127.0.0.1",
						Port:           8000,
						Path:           "/metrics",
						ScrapeInterval: 20 * time.Second,
						ScrapeTimeout:  5 * time.Second,
						Metrics: []MetricRule{
							{
								SourceName: "metric_a",
								TargetName: "metric_a",
								TargetType: MetricTypeNode,
							},
							{
								SourceName: "metric_b",
								SourceLabels: model.LabelSet{
									"bbb": "aaa",
								},
								TargetName: "metric_b",
								TargetType: MetricTypeNode,
							},
						},
					},
					{
						Name:           "job-1",
						Enable:         true,
						Address:        "127.0.0.1",
						Port:           9316,
						Path:           "/metrics",
						ScrapeInterval: 30 * time.Second,
						ScrapeTimeout:  10 * time.Second,
						Metrics: []MetricRule{
							{
								SourceName: "metric_c",
								TargetName: "metric_c",
								TargetType: MetricTypePod,
							},
						},
					},
					{
						Name:           "job-2",
						Enable:         false,
						Address:        "127.0.0.1",
						Port:           9090,
						Path:           "/default-metrics",
						ScrapeInterval: 30 * time.Second,
						ScrapeTimeout:  10 * time.Second,
						Metrics: []MetricRule{
							{
								SourceName: "metric_d",
								TargetName: "metric_d",
								TargetType: MetricTypePod,
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.prepareFn != nil {
				tt.prepareFn(helper)
			}
			got, gotErr := LoadCollectRuleFromFile(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCollectRule(t *testing.T) {
	tests := []struct {
		name    string
		arg     *CollectRule
		wantErr bool
		want    []*ScraperConfig
	}{
		{
			name:    "missing job rule",
			arg:     &CollectRule{},
			wantErr: true,
		},
		{
			name: "missing job rule name",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Enable: true,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "missing job address",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:   "job-0",
						Enable: true,
						Port:   9316,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "missing metric rule target name",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_a",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "missing metric rule source name",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								TargetName: "metric_a",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "missing metric target type",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s",
								TargetName: "metric_t",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "unsupported metric target type",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s",
								TargetName: "metric_t",
								TargetType: "unknown",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid pod uid metric rule",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s",
								TargetName: "metric_t",
								TargetType: "node",
							},
							{
								SourceName: "metric_s_1",
								TargetName: "metric_t_1",
								TargetType: "container",
							},
							{
								SourceName: "metric_s_2",
								TargetName: "metric_t_2",
								TargetType: "pod_uid",
								TargetLabels: model.LabelSet{
									"unknown_label": "unknown",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid container metric rule",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s",
								TargetName: "metric_t",
								TargetType: "node",
							},
							{
								SourceName: "metric_s_2",
								TargetName: "metric_t_2",
								TargetType: "pod_uid",
							},
							{
								SourceName: "metric_s_1",
								TargetName: "metric_t_1",
								TargetType: "container",
								TargetLabels: model.LabelSet{
									"unknown_label": "unknown",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid container id metric rule",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s",
								TargetName: "metric_t",
								TargetType: "node",
							},
							{
								SourceName: "metric_s_2",
								TargetName: "metric_t_2",
								TargetType: "pod_uid",
							},
							{
								SourceName: "metric_s_1",
								TargetName: "metric_t_1",
								TargetType: "container_id",
								TargetLabels: model.LabelSet{
									"unknown_label": "unknown",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate metric target name",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s",
								TargetName: "metric_t",
								TargetType: "node",
							},
						},
					},
					{
						Name:    "job-1",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9317,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s_1",
								TargetName: "metric_t",
								TargetType: "node",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid rule",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s",
								TargetName: "metric_t",
								TargetType: "node",
							},
							{
								SourceName: "metric_s_1",
								SourceLabels: model.LabelSet{
									"label_0": "value_0",
								},
								TargetName: "metric_t_1",
								TargetType: "pod",
								TargetLabels: model.LabelSet{
									"pod":       "pod_name",
									"namespace": "pod_namespace",
								},
							},
						},
					},
					{
						Name:           "job-1",
						Enable:         true,
						Address:        "127.0.0.1",
						Port:           9317,
						Path:           "/metrics",
						ScrapeInterval: 60 * time.Second,
						ScrapeTimeout:  10 * time.Second,
						Metrics: []MetricRule{
							{
								SourceName: "metric_s_2",
								TargetName: "metric_t_2",
								TargetType: "node",
							},
						},
					},
					{
						Name:    "job-2",
						Enable:  false,
						Address: "127.0.0.1",
						Port:    9000,
						Path:    "",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s_3",
								TargetName: "metric_t_3",
								TargetType: "node",
							},
						},
					},
				},
			},
			wantErr: false,
			want: []*ScraperConfig{
				{
					Enable:         true,
					JobName:        "job-0",
					ScrapeInterval: DefaultScrapeInterval,
					Client: &ClientConfig{
						Timeout:     DefaultScrapeTimeout,
						InsecureTLS: true,
						Address:     "127.0.0.1",
						Port:        9316,
						Path:        "/metrics",
					},
					Decoders: []*DecodeConfig{
						{
							TargetMetric:   "metric_t",
							TargetType:     "node",
							SourceMetric:   "metric_s",
							ValidTimeRange: DefaultScrapeInterval / 2,
						},
						{
							TargetMetric:   "metric_t_1",
							TargetType:     "pod",
							TargetLabels:   model.LabelSet{"pod": "pod_name", "namespace": "pod_namespace"},
							SourceMetric:   "metric_s_1",
							SourceLabels:   model.LabelSet{"label_0": "value_0"},
							ValidTimeRange: DefaultScrapeInterval / 2,
						},
					},
				},
				{
					Enable:         true,
					JobName:        "job-1",
					ScrapeInterval: 60 * time.Second,
					Client: &ClientConfig{
						Timeout:     10 * time.Second,
						InsecureTLS: true,
						Address:     "127.0.0.1",
						Port:        9317,
						Path:        "/metrics",
					},
					Decoders: []*DecodeConfig{
						{
							TargetMetric:   "metric_t_2",
							TargetType:     "node",
							SourceMetric:   "metric_s_2",
							ValidTimeRange: 30 * time.Second,
						},
					},
				},
				{
					Enable:         false,
					JobName:        "job-2",
					ScrapeInterval: DefaultScrapeInterval,
					Client: &ClientConfig{
						Timeout:     DefaultScrapeTimeout,
						InsecureTLS: true,
						Address:     "127.0.0.1",
						Port:        9000,
						Path:        "",
					},
					Decoders: []*DecodeConfig{
						{
							TargetMetric:   "metric_t_3",
							TargetType:     "node",
							SourceMetric:   "metric_s_3",
							ValidTimeRange: DefaultScrapeInterval / 2,
						},
					},
				},
			},
		},
		{
			name: "valid rule 1",
			arg: &CollectRule{
				Enable: true,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s_1",
								SourceLabels: model.LabelSet{
									"label_0": "value_0",
								},
								TargetName: "metric_t_1",
								TargetType: "pod",
								TargetLabels: model.LabelSet{
									"pod":       "pod_name",
									"namespace": "pod_namespace",
								},
							},
						},
					},
					{
						Name:           "job-1",
						Enable:         true,
						Address:        "127.0.0.1",
						Port:           9317,
						Path:           "/metrics",
						ScrapeInterval: 3 * time.Second,
						ScrapeTimeout:  1 * time.Second,
						Metrics: []MetricRule{
							{
								SourceName: "metric_s_2",
								TargetName: "metric_t_2",
								TargetType: "node",
								TargetLabels: model.LabelSet{
									"type": "node",
								},
							},
						},
					},
					{
						Name:    "job-2",
						Enable:  false,
						Address: "127.0.0.1",
						Port:    9000,
						Path:    "",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s_3",
								TargetName: "metric_t_3",
								TargetType: "qos",
								TargetLabels: model.LabelSet{
									"qos": "qos_class",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			want: []*ScraperConfig{
				{
					Enable:         true,
					JobName:        "job-0",
					ScrapeInterval: DefaultScrapeInterval,
					Client: &ClientConfig{
						Timeout:     DefaultScrapeTimeout,
						InsecureTLS: true,
						Address:     "127.0.0.1",
						Port:        9316,
						Path:        "/metrics",
					},
					Decoders: []*DecodeConfig{
						{
							TargetMetric:   "metric_t_1",
							TargetType:     "pod",
							TargetLabels:   model.LabelSet{"pod": "pod_name", "namespace": "pod_namespace"},
							SourceMetric:   "metric_s_1",
							SourceLabels:   model.LabelSet{"label_0": "value_0"},
							ValidTimeRange: DefaultScrapeInterval / 2,
						},
					},
				},
				{
					Enable:         true,
					JobName:        "job-1",
					ScrapeInterval: 3 * time.Second,
					Client: &ClientConfig{
						Timeout:     1 * time.Second,
						InsecureTLS: true,
						Address:     "127.0.0.1",
						Port:        9317,
						Path:        "/metrics",
					},
					Decoders: []*DecodeConfig{
						{
							TargetMetric:   "metric_t_2",
							TargetType:     "node",
							TargetLabels:   model.LabelSet{"type": "node"},
							SourceMetric:   "metric_s_2",
							ValidTimeRange: MinValidMetricTimeRange,
						},
					},
				},
				{
					Enable:         false,
					JobName:        "job-2",
					ScrapeInterval: DefaultScrapeInterval,
					Client: &ClientConfig{
						Timeout:     DefaultScrapeTimeout,
						InsecureTLS: true,
						Address:     "127.0.0.1",
						Port:        9000,
						Path:        "",
					},
					Decoders: []*DecodeConfig{
						{
							TargetMetric:   "metric_t_3",
							TargetType:     "qos",
							TargetLabels:   model.LabelSet{"qos": "qos_class"},
							SourceMetric:   "metric_s_3",
							ValidTimeRange: DefaultScrapeInterval / 2,
						},
					},
				},
			},
		},
		{
			name: "rule disabled",
			arg: &CollectRule{
				Enable: false,
				Jobs: []JobRule{
					{
						Name:    "job-0",
						Enable:  true,
						Address: "127.0.0.1",
						Port:    9316,
						Path:    "/metrics",
						Metrics: []MetricRule{
							{
								SourceName: "metric_s_1",
								SourceLabels: model.LabelSet{
									"label_0": "value_0",
								},
								TargetName: "metric_t_1",
								TargetType: "pod",
								TargetLabels: model.LabelSet{
									"pod":       "pod_name",
									"namespace": "pod_namespace",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := tt.arg.ToConfigs()
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			assert.Equal(t, tt.want, got)
		})
	}
}
