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

import (
	"flag"

	corev1 "k8s.io/api/core/v1"
)

type Config struct {
	KubeletPreferredAddressType string
	KubeletSyncIntervalSeconds  int
	KubeletSyncTimeoutSeconds   int
}

func NewDefaultConfig() *Config {
	return &Config{
		KubeletPreferredAddressType: string(corev1.NodeInternalIP),
		KubeletSyncIntervalSeconds:  30,
		KubeletSyncTimeoutSeconds:   3,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.KubeletPreferredAddressType, "KubeletPreferredAddressType", c.KubeletPreferredAddressType, "The node address types to use when determining which address to use to connect to a particular node.")
	fs.IntVar(&c.KubeletSyncIntervalSeconds, "KubeletSyncIntervalSeconds", c.KubeletSyncIntervalSeconds, "The interval at which Koordlet will retain datas from Kubelet.")
	fs.IntVar(&c.KubeletSyncTimeoutSeconds, "KubeletSyncTimeoutSeconds", c.KubeletSyncTimeoutSeconds, "The length of time to wait before giving up on a single request to Kubelet.")
}
