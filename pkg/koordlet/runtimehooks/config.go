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
	"time"

	"k8s.io/apimachinery/pkg/util/runtime"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/featuregate"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/batchresource"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/coresched"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/cpunormalization"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/cpuset"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/gpu"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/groupidentity"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	// GroupIdentity sets pod cpu group identity(bvt) according to QoS.
	//
	// owner: @zwzhang0107 @saintube
	// alpha: v0.3
	// beta: v1.1
	GroupIdentity featuregate.Feature = "GroupIdentity"

	// CPUSetAllocator sets container cpuset according to allocate result from koord-scheduler for LSR/LS pods.
	//
	// owner: @saintube @zwzhang0107
	// alpha: v0.3
	// beta: v1.1
	CPUSetAllocator featuregate.Feature = "CPUSetAllocator"

	// GPUEnvInject injects gpu allocated env info according to allocate result from koord-scheduler.
	//
	// owner: @ZYecho @jasonliu747
	// alpha: v0.3
	// beta: v1.1
	GPUEnvInject featuregate.Feature = "GPUEnvInject"

	// BatchResource sets request and limits of cpu and memory on cgroup file according batch resources.
	//
	// owner: @saintube @zwzhang0107
	// alpha: v1.1
	BatchResource featuregate.Feature = "BatchResource"

	// CPUNormalization adjusts cpu cgroups value for cpu normalized LS pod.
	//
	// owner: @saintube @zwzhang0107
	// alpha: v1.4
	CPUNormalization featuregate.Feature = "CPUNormalization"

	// CoreSched manages Linux Core Scheduling cookies for containers who enable the core sched.
	// NOTE: CoreSched is an alternative policy of the CPU QoS, and it is exclusive to the Group Identity feature.
	//
	// owner: @saintube @zwzhang0107
	// alpha: v1.4
	CoreSched featuregate.Feature = "CoreSched"
)

var (
	defaultRuntimeHooksFG = map[featuregate.Feature]featuregate.FeatureSpec{
		GroupIdentity:    {Default: true, PreRelease: featuregate.Beta},
		CPUSetAllocator:  {Default: true, PreRelease: featuregate.Beta},
		GPUEnvInject:     {Default: false, PreRelease: featuregate.Alpha},
		BatchResource:    {Default: true, PreRelease: featuregate.Beta},
		CPUNormalization: {Default: false, PreRelease: featuregate.Alpha},
		CoreSched:        {Default: false, PreRelease: featuregate.Alpha},
	}

	runtimeHookPlugins = map[featuregate.Feature]HookPlugin{
		GroupIdentity:    groupidentity.Object(),
		CPUSetAllocator:  cpuset.Object(),
		GPUEnvInject:     gpu.Object(),
		BatchResource:    batchresource.Object(),
		CPUNormalization: cpunormalization.Object(),
		CoreSched:        coresched.Object(),
	}
)

type Config struct {
	RuntimeHooksNetwork             string
	RuntimeHooksAddr                string
	RuntimeHooksFailurePolicy       string
	RuntimeHooksPluginFailurePolicy string
	RuntimeHookConfigFilePath       string
	RuntimeHookHostEndpoint         string
	RuntimeHookDisableStages        []string
	RuntimeHooksNRI                 bool
	RuntimeHooksNRISocketPath       string
	RuntimeHookReconcileInterval    time.Duration
}

func NewDefaultConfig() *Config {
	return &Config{
		RuntimeHooksNetwork:             "unix",
		RuntimeHooksAddr:                "/host-var-run-koordlet/koordlet.sock",
		RuntimeHooksFailurePolicy:       "Ignore",
		RuntimeHooksPluginFailurePolicy: "Ignore",
		RuntimeHookConfigFilePath:       system.Conf.RuntimeHooksConfigDir,
		RuntimeHookHostEndpoint:         "/var/run/koordlet/koordlet.sock",
		RuntimeHookDisableStages:        []string{},
		RuntimeHooksNRI:                 true,
		RuntimeHooksNRISocketPath:       "nri/nri.sock",
		RuntimeHookReconcileInterval:    10 * time.Second,
	}
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.RuntimeHooksNetwork, "runtime-hooks-network", c.RuntimeHooksNetwork, "rpc server network type for runtime hooks")
	fs.StringVar(&c.RuntimeHooksAddr, "runtime-hooks-addr", c.RuntimeHooksAddr, "rpc server address for runtime hooks")
	fs.StringVar(&c.RuntimeHooksFailurePolicy, "runtime-hooks-failure-policy", c.RuntimeHooksFailurePolicy, "failure policy for runtime hooks")
	fs.StringVar(&c.RuntimeHooksPluginFailurePolicy, "runtime-hooks-plugin-failure-policy", c.RuntimeHooksPluginFailurePolicy, "stop running other hooks once someone failed")
	fs.StringVar(&c.RuntimeHookConfigFilePath, "runtime-hooks-config-path", c.RuntimeHookConfigFilePath, "config file path for runtime hooks")
	fs.StringVar(&c.RuntimeHookHostEndpoint, "runtime-hooks-host-endpoint", c.RuntimeHookHostEndpoint, "host endpoint of runtime proxy")
	fs.Var(cliflag.NewStringSlice(&c.RuntimeHookDisableStages), "runtime-hooks-disable-stages", "disable stages for runtime hooks")
	fs.BoolVar(&c.RuntimeHooksNRI, "enable-nri-runtime-hook", c.RuntimeHooksNRI, "enable/disable runtime hooks nri mode")
	fs.DurationVar(&c.RuntimeHookReconcileInterval, "runtime-hooks-reconcile-interval", c.RuntimeHookReconcileInterval, "reconcile interval for each plugins")
}

func init() {
	runtime.Must(features.DefaultMutableKoordletFeatureGate.Add(defaultRuntimeHooksFG))
}
