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

package framework

import (
	"flag"
)

type Config struct {
	ReconcileIntervalSeconds    int
	CPUSuppressIntervalSeconds  int
	CPUEvictIntervalSeconds     int
	MemoryEvictIntervalSeconds  int
	MemoryEvictCoolTimeSeconds  int
	CPUEvictCoolTimeSeconds     int
	OnlyEvictByAPI              bool
	EvictByCopilotAgent         bool
	EvictByCopilotEndPoint      string
	EvictByCopilotPodLabelKey   string
	EvictByCopilotPodLabelValue string
	QOSExtensionCfg             *QOSExtensionConfig
}

func NewDefaultConfig() *Config {
	return &Config{
		ReconcileIntervalSeconds:    1,
		CPUSuppressIntervalSeconds:  1,
		CPUEvictIntervalSeconds:     1,
		MemoryEvictIntervalSeconds:  1,
		MemoryEvictCoolTimeSeconds:  4,
		CPUEvictCoolTimeSeconds:     20,
		OnlyEvictByAPI:              false,
		EvictByCopilotAgent:         false,
		EvictByCopilotEndPoint:      "/var/run/yarn-copilot/yarn-copilot.sock",
		EvictByCopilotPodLabelKey:   "app.kubernetes.io/component",
		EvictByCopilotPodLabelValue: "node-manager",
		QOSExtensionCfg:             &QOSExtensionConfig{FeatureGates: map[string]bool{}},
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.IntVar(&c.ReconcileIntervalSeconds, "reconcile-interval-seconds", c.ReconcileIntervalSeconds, "reconcile be pod cgroup interval by seconds")
	fs.IntVar(&c.CPUSuppressIntervalSeconds, "cpu-suppress-interval-seconds", c.CPUSuppressIntervalSeconds, "suppress be pod cpu resource interval by seconds")
	fs.IntVar(&c.CPUEvictIntervalSeconds, "cpu-evict-interval-seconds", c.CPUEvictIntervalSeconds, "evict be pod(cpu) interval by seconds")
	fs.IntVar(&c.MemoryEvictIntervalSeconds, "memory-evict-interval-seconds", c.MemoryEvictIntervalSeconds, "evict be pod(memory) interval by seconds")
	fs.IntVar(&c.MemoryEvictCoolTimeSeconds, "memory-evict-cool-time-seconds", c.MemoryEvictCoolTimeSeconds, "cooling time: memory next evict time should after lastEvictTime + MemoryEvictCoolTimeSeconds")
	fs.IntVar(&c.CPUEvictCoolTimeSeconds, "cpu-evict-cool-time-seconds", c.CPUEvictCoolTimeSeconds, "cooltime: CPU next evict time should after lastEvictTime + CPUEvictCoolTimeSeconds")
	fs.BoolVar(&c.OnlyEvictByAPI, "only-evict-by-api", c.OnlyEvictByAPI, "only evict pod if call eviction api successed")
	fs.BoolVar(&c.EvictByCopilotAgent, "evict-by-copilot-agent", c.EvictByCopilotAgent, "if evict container by copilot agent")
	fs.StringVar(&c.EvictByCopilotEndPoint, "evict-by-copilot-endpoint", c.EvictByCopilotEndPoint, "endpoint of evicting container by copilot agent")
	fs.StringVar(&c.EvictByCopilotPodLabelKey, "evict-by-copilot-pod-label-key", c.EvictByCopilotPodLabelKey, "pod label key of evicting container by copilot agent")
	fs.StringVar(&c.EvictByCopilotPodLabelValue, "evict-by-copilot-pod-label-value", c.EvictByCopilotPodLabelValue, "pod label value of evicting container by copilot agent")
	c.QOSExtensionCfg.InitFlags(fs)
}
