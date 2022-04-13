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

package resmanager

import (
	"flag"
)

type Config struct {
	ReconcileIntervalSeconds   int
	CPUSuppressIntervalSeconds int
	MemoryEvictIntervalSeconds int
	MemoryEvictCoolTimeSeconds int
}

func NewDefaultConfig() *Config {
	return &Config{
		ReconcileIntervalSeconds:   1,
		CPUSuppressIntervalSeconds: 1,
		MemoryEvictIntervalSeconds: 1,
		MemoryEvictCoolTimeSeconds: 4,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.IntVar(&c.ReconcileIntervalSeconds, "ReconcileIntervalSeconds", c.ReconcileIntervalSeconds, "reconcile be pod cgroup interval by seconds")
	fs.IntVar(&c.CPUSuppressIntervalSeconds, "CPUSuppressIntervalSeconds", c.CPUSuppressIntervalSeconds, "suppress be pod cpu resource interval by seconds")
	fs.IntVar(&c.MemoryEvictIntervalSeconds, "MemoryEvictIntervalSeconds", c.MemoryEvictIntervalSeconds, "evict be pod(memory) interval by seconds")
	fs.IntVar(&c.MemoryEvictCoolTimeSeconds, "MemoryEvictCoolTimeSeconds", c.MemoryEvictCoolTimeSeconds, "cooling time: memory next evict time should after lastEvictTime + MemoryEvictCoolTimeSeconds")

}
