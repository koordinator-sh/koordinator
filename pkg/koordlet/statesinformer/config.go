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

package statesinformer

import "flag"

type Config struct {
	KubeletIPAddr              string
	KubeletHTTPPort            int
	KubeletSyncIntervalSeconds int
	KubeletSyncTimeoutSeconds  int
}

func NewDefaultConfig() *Config {
	return &Config{
		KubeletIPAddr:              "localhost",
		KubeletHTTPPort:            10255,
		KubeletSyncIntervalSeconds: 1,
		KubeletSyncTimeoutSeconds:  3,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.KubeletIPAddr, "KubeletIPAddr", c.KubeletIPAddr, "Kubelet IP Address.")
	fs.IntVar(&c.KubeletHTTPPort, "KubeletHTTPPort", c.KubeletHTTPPort, "Kubelet HTTP httpPort.")
	fs.IntVar(&c.KubeletSyncIntervalSeconds, "KubeletSyncIntervalSeconds", c.KubeletSyncIntervalSeconds, "Kubelet sync interval by seconds.")
	fs.IntVar(&c.KubeletSyncTimeoutSeconds, "KubeletSyncTimeoutSeconds", c.KubeletSyncTimeoutSeconds, "Kubelet sync timeout by seconds.")
}
