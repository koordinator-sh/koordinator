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

package runtimehooks

import (
	"flag"
	"strings"

	"k8s.io/apimachinery/pkg/util/runtime"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/featuregate"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/cpuset"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/groupidentity"
)

const (
	GroupIdentity   featuregate.Feature = "GroupIdentity"
	CPUSetAllocator featuregate.Feature = "CPUSetAllocator"
)

var (
	DefaultMutableRuntimeHooksFG featuregate.MutableFeatureGate = featuregate.NewFeatureGate()
	DefaultRuntimeHooksFG        featuregate.FeatureGate        = DefaultMutableRuntimeHooksFG

	defaultRuntimeHooksFG = map[featuregate.Feature]featuregate.FeatureSpec{
		GroupIdentity:   {Default: false, PreRelease: featuregate.Alpha},
		CPUSetAllocator: {Default: false, PreRelease: featuregate.Alpha},
	}

	runtimeHookPlugins = map[featuregate.Feature]HookPlugin{
		GroupIdentity:   groupidentity.Object(),
		CPUSetAllocator: cpuset.Object(),
	}
)

type Config struct {
	RuntimeHooksNetwork       string
	RuntimeHooksAddr          string
	RuntimeHooksFailurePolicy string
	FeatureGates              map[string]bool
}

func NewDefaultConfig() *Config {
	return &Config{
		RuntimeHooksNetwork:       "tcp",
		RuntimeHooksAddr:          ":9318",
		RuntimeHooksFailurePolicy: "Ignore",
		FeatureGates:              map[string]bool{},
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.RuntimeHooksNetwork, "runtime-hooks-network", c.RuntimeHooksNetwork, "rpc server network type for runtime hooks")
	fs.StringVar(&c.RuntimeHooksAddr, "runtime-hooks-addr", c.RuntimeHooksAddr, "rpc server address for runtime hooks")
	fs.StringVar(&c.RuntimeHooksFailurePolicy, "runtime-hooks-failure-policy", c.RuntimeHooksFailurePolicy, "failure policy for runtime hooks")
	fs.Var(cliflag.NewMapStringBool(&c.FeatureGates), "runtime-hooks",
		"A set of key=value pairs that describe feature gates for runtime hooks alpha/experimental features. "+
			"Options are:\n"+strings.Join(DefaultRuntimeHooksFG.KnownFeatures(), "\n"))
}

func init() {
	runtime.Must(DefaultMutableRuntimeHooksFG.Add(defaultRuntimeHooksFG))
}
