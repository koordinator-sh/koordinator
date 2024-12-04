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
	"math"
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
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/rdma"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/resctrl"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/tc"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks/terwayqos"
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

	// RDMADeviceInject injects rdma device info according to allocate result from koord-scheduler.
	//
	// owner: @ZiMengSheng
	// alpha: v1.6
	RDMADeviceInject featuregate.Feature = "RDMADeviceInject"

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

	// TerwayQoS enables net QoS feature of koordlet.
	// owner: @l1b0k
	// alpha: v1.5
	TerwayQoS featuregate.Feature = "TerwayQoS"

	// TCNetworkQoS indicates a network qos implementation based on tc.
	// owner: @lucming
	// alpha: v1.5
	TCNetworkQoS featuregate.Feature = "TCNetworkQoS"

	// Resctrl adjusts LLC/MB value for pod.
	//
	// owner: @kangclzjc @saintube @zwzhang0107
	// alpha: v1.5
	Resctrl featuregate.Feature = "Resctrl"
)

var (
	defaultRuntimeHooksFG = map[featuregate.Feature]featuregate.FeatureSpec{
		GroupIdentity:    {Default: true, PreRelease: featuregate.Beta},
		CPUSetAllocator:  {Default: true, PreRelease: featuregate.Beta},
		GPUEnvInject:     {Default: false, PreRelease: featuregate.Alpha},
		RDMADeviceInject: {Default: false, PreRelease: featuregate.Alpha},
		BatchResource:    {Default: true, PreRelease: featuregate.Beta},
		CPUNormalization: {Default: false, PreRelease: featuregate.Alpha},
		CoreSched:        {Default: false, PreRelease: featuregate.Alpha},
		TerwayQoS:        {Default: false, PreRelease: featuregate.Alpha},
		TCNetworkQoS:     {Default: false, PreRelease: featuregate.Alpha},
		Resctrl:          {Default: false, PreRelease: featuregate.Alpha},
	}

	runtimeHookPlugins = map[featuregate.Feature]HookPlugin{
		GroupIdentity:    groupidentity.Object(),
		CPUSetAllocator:  cpuset.Object(),
		GPUEnvInject:     gpu.Object(),
		RDMADeviceInject: rdma.Object(),
		BatchResource:    batchresource.Object(),
		CPUNormalization: cpunormalization.Object(),
		CoreSched:        coresched.Object(),
		TerwayQoS:        terwayqos.Object(),
		TCNetworkQoS:     tc.Object(),
		Resctrl:          resctrl.Object(),
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
	RuntimeHooksNRIConnectTimeout   time.Duration
	RuntimeHooksNRIBackOffDuration  time.Duration
	RuntimeHooksNRIBackOffCap       time.Duration
	RuntimeHooksNRIBackOffFactor    float64
	RuntimeHooksNRIBackOffSteps     int
	RuntimeHooksNRISocketPath       string
	RuntimeHooksNRIPluginName       string
	RuntimeHooksNRIPluginIndex      string
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
		RuntimeHooksNRIConnectTimeout:   6 * time.Second,
		RuntimeHooksNRIBackOffDuration:  1 * time.Second,
		RuntimeHooksNRIBackOffCap:       1<<62 - 1,
		RuntimeHooksNRIBackOffSteps:     math.MaxInt32,
		RuntimeHooksNRIBackOffFactor:    2,
		RuntimeHooksNRISocketPath:       "nri/nri.sock",
		RuntimeHooksNRIPluginName:       "koordlet_nri",
		RuntimeHooksNRIPluginIndex:      "00",
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
	fs.DurationVar(&c.RuntimeHooksNRIConnectTimeout, "runtime-hooks-nri-connect-timeout", c.RuntimeHooksNRIConnectTimeout, "nri server connect time out, it should be a little more than default plugin registration timeout(5 seconds) which is defined in containerd config")
	fs.DurationVar(&c.RuntimeHooksNRIBackOffDuration, "runtime-hooks-nri-backoff-duration", c.RuntimeHooksNRIBackOffDuration, "nri server backoff duration")
	fs.DurationVar(&c.RuntimeHooksNRIBackOffCap, "runtime-hooks-nri-backoff-cap", c.RuntimeHooksNRIBackOffCap, "nri server backoff cap")
	fs.IntVar(&c.RuntimeHooksNRIBackOffSteps, "runtime-hooks-nri-backoff-steps", c.RuntimeHooksNRIBackOffSteps, "nri server backoff steps")
	fs.Float64Var(&c.RuntimeHooksNRIBackOffFactor, "runtime-hooks-nri-backoff-factor", c.RuntimeHooksNRIBackOffFactor, "nri server reconnect backoff factor")
	fs.StringVar(&c.RuntimeHooksNRISocketPath, "runtime-hooks-nri-socket-path", c.RuntimeHooksNRISocketPath, "nri server socket path")
	fs.StringVar(&c.RuntimeHooksNRIPluginName, "runtime-hooks-nri-plugin-name", c.RuntimeHooksNRISocketPath, "nri plugin name of the koordlet runtime hooks")
	fs.StringVar(&c.RuntimeHooksNRIPluginIndex, "runtime-hooks-nri-plugin-index", c.RuntimeHooksNRIPluginIndex, "nri plugin index of the koordlet runtime hooks")
	fs.Var(cliflag.NewStringSlice(&c.RuntimeHookDisableStages), "runtime-hooks-disable-stages", "disable stages for runtime hooks")
	fs.BoolVar(&c.RuntimeHooksNRI, "enable-nri-runtime-hook", c.RuntimeHooksNRI, "enable/disable runtime hooks nri mode")
	fs.DurationVar(&c.RuntimeHookReconcileInterval, "runtime-hooks-reconcile-interval", c.RuntimeHookReconcileInterval, "reconcile interval for each plugins")
}

func init() {
	runtime.Must(features.DefaultMutableKoordletFeatureGate.Add(defaultRuntimeHooksFG))
}
