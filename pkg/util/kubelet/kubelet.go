/*
Copyright 2022 The Koordinator Authors.
Copyright 2017 The Kubernetes Authors.

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

package kubelet

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"

	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/cmd/kubelet/app/options"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/kubernetes/pkg/kubelet/eviction"
	evictionapi "k8s.io/kubernetes/pkg/kubelet/eviction/api"
	"k8s.io/kubernetes/pkg/kubelet/kubeletconfig/configfiles"
	"k8s.io/kubernetes/pkg/kubelet/stats/pidlimit"
	utilfs "k8s.io/kubernetes/pkg/util/filesystem"

	"github.com/koordinator-sh/koordinator/pkg/util"
)

type KubeletOptions struct {
	*options.KubeletFlags
	*kubeletconfiginternal.KubeletConfiguration
}

func NewKubeletOptions(args []string) (*KubeletOptions, error) {
	kubeletConfig, err := options.NewKubeletConfiguration()
	if err != nil {
		return nil, err
	}

	cleanFlagSet := pflag.NewFlagSet("kubelet", pflag.ContinueOnError)
	cleanFlagSet.ParseErrorsWhitelist.UnknownFlags = true
	cleanFlagSet.SetOutput(io.Discard)
	cleanFlagSet.SetNormalizeFunc(cliflag.WordSepNormalizeFunc)
	kubeletFlags := options.NewKubeletFlags()
	kubeletFlags.AddFlags(cleanFlagSet)
	options.AddKubeletConfigFlags(cleanFlagSet, kubeletConfig)

	err = cleanFlagSet.Parse(args)
	if err != nil {
		return nil, err
	}

	// NOTE: options.ValidateKubeletFlags is not compatible with kubelet v1.18, now skip validation
	// validate the initial KubeletFlags
	// if err := options.ValidateKubeletFlags(kubeletFlags); err != nil {
	// 	return nil, fmt.Errorf("failed to validate kubelet flags: %w", err)
	// }

	// load kubelet config file, if provided
	if configFile := kubeletFlags.KubeletConfigFile; len(configFile) > 0 {
		kubeletConfig, err = loadConfigFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load kubelet config file, error: %w, path: %s", err, configFile)
		}
		// We must enforce flag precedence by re-parsing the command line into the new object.
		// This is necessary to preserve backwards-compatibility across binary upgrades.
		// See issue #56171 for more details.
		if err := kubeletConfigFlagPrecedence(kubeletConfig, args); err != nil {
			return nil, fmt.Errorf("failed to precedence kubeletConfigFlag: %w", err)
		}
	}

	return &KubeletOptions{
		KubeletFlags:         kubeletFlags,
		KubeletConfiguration: kubeletConfig,
	}, nil
}

// newFlagSetWithGlobals constructs a new pflag.FlagSet with global flags registered
// on it.
func newFlagSetWithGlobals() *pflag.FlagSet {
	fs := pflag.NewFlagSet("", pflag.ExitOnError)
	// set the normalize func, similar to k8s.io/component-base/cli//flags.go:InitFlags
	fs.SetNormalizeFunc(cliflag.WordSepNormalizeFunc)
	// explicitly add flags from libs that register global flags
	options.AddGlobalFlags(fs)
	return fs
}

// newFakeFlagSet constructs a pflag.FlagSet with the same flags as fs, but where
// all values have noop Set implementations
func newFakeFlagSet(fs *pflag.FlagSet) *pflag.FlagSet {
	ret := pflag.NewFlagSet("", pflag.ExitOnError)
	ret.SetNormalizeFunc(fs.GetNormalizeFunc())
	fs.VisitAll(func(f *pflag.Flag) {
		ret.VarP(cliflag.NoOp{}, f.Name, f.Shorthand, f.Usage)
	})
	return ret
}

// kubeletConfigFlagPrecedence re-parses flags over the KubeletConfiguration object.
// We must enforce flag precedence by re-parsing the command line into the new object.
// This is necessary to preserve backwards-compatibility across binary upgrades.
// See issue #56171 for more details.
func kubeletConfigFlagPrecedence(kc *kubeletconfiginternal.KubeletConfiguration, args []string) error {
	// We use a throwaway kubeletFlags and a fake global flagset to avoid double-parses,
	// as some Set implementations accumulate values from multiple flag invocations.
	fs := newFakeFlagSet(newFlagSetWithGlobals())
	fs.ParseErrorsWhitelist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	// register throwaway KubeletFlags
	options.NewKubeletFlags().AddFlags(fs)
	// register new KubeletConfiguration
	options.AddKubeletConfigFlags(fs, kc)
	// Remember original feature gates, so we can merge with flag gates later
	original := kc.FeatureGates
	// re-parse flags
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Add back feature gates that were set in the original kc, but not in flags
	for k, v := range original {
		if _, ok := kc.FeatureGates[k]; !ok {
			kc.FeatureGates[k] = v
		}
	}
	return nil
}

func loadConfigFile(kubeletConfigFile string) (*kubeletconfiginternal.KubeletConfiguration, error) {
	const errFmt = "failed to load Kubelet config file %s, error %v"
	loader, err := configfiles.NewFsLoader(&utilfs.DefaultFs{}, kubeletConfigFile)
	if err != nil {
		return nil, fmt.Errorf(errFmt, kubeletConfigFile, err)
	}
	kc, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf(errFmt, kubeletConfigFile, err)
	}
	return kc, err
}

func NewCPUTopology(cpuInfo *util.LocalCPUInfo) *topology.CPUTopology {
	cpuTopology := &topology.CPUTopology{
		NumCPUs:    int(cpuInfo.TotalInfo.NumberCPUs),
		NumCores:   int(cpuInfo.TotalInfo.NumberCores),
		NumSockets: int(cpuInfo.TotalInfo.NumberSockets),
		CPUDetails: topology.CPUDetails{},
	}
	for _, v := range cpuInfo.ProcessorInfos {
		cpuTopology.CPUDetails[int(v.CPUID)] = topology.CPUInfo{
			NUMANodeID: int(v.NodeID),
			SocketID:   int(v.SocketID),
			CoreID:     int(v.CoreID),
		}
	}
	return cpuTopology
}

func GetStaticCPUManagerPolicyReservedCPUs(topology *topology.CPUTopology, kubeletOptions *KubeletOptions) (cpuset.CPUSet, error) {
	if kubeletOptions.CPUManagerPolicy != string(cpumanager.PolicyStatic) {
		return cpuset.CPUSet{}, nil
	}

	reservedCPUs, kubeReserved, systemReserved, err := GetKubeletReservedOptions(kubeletOptions, topology)
	if err != nil {
		return cpuset.CPUSet{}, err
	}

	nodeAllocatableReservation, err := GetNodeAllocatableReservation(topology.NumCPUs, 0,
		kubeletOptions.EvictionHard, systemReserved, kubeReserved, kubeletOptions.ExperimentalNodeAllocatableIgnoreEvictionThreshold)
	if err != nil {
		return cpuset.CPUSet{}, err
	}

	numReservedCPUs, err := getNumReservedCPUs(nodeAllocatableReservation)
	if err != nil {
		return cpuset.CPUSet{}, err
	}

	var reserved cpuset.CPUSet

	if reservedCPUs.Size() > 0 {
		reserved = reservedCPUs
	} else {
		// takeByTopology allocates CPUs associated with low-numbered cores from
		// allCPUs.
		//
		// For example: Given a system with 8 CPUs available and HT enabled,
		// if numReservedCPUs=2, then reserved={0,4}
		allCPUs := topology.CPUDetails.CPUs()
		reserved, _ = TakeByTopology(allCPUs, numReservedCPUs, topology)
	}

	if reserved.Size() != numReservedCPUs {
		err := fmt.Errorf("[cpumanager] unable to reserve the required amount of CPUs (size of %s did not equal %d)", reserved, numReservedCPUs)
		return cpuset.CPUSet{}, err
	}
	return reserved, nil
}

func getNumReservedCPUs(nodeAllocatableReservation corev1.ResourceList) (int, error) {
	reservedCPUs, ok := nodeAllocatableReservation[corev1.ResourceCPU]
	if !ok {
		// The static policy cannot initialize without this information.
		return 0, fmt.Errorf("[cpumanager] unable to determine reserved CPU resources for static policy")
	}
	if reservedCPUs.IsZero() {
		// The static policy requires this to be nonzero. Zero CPU reservation
		// would allow the shared pool to be completely exhausted. At that point
		// either we would violate our guarantee of exclusivity or need to evict
		// any pod that has at least one container that requires zero CPUs.
		// See the comments in policy_static.go for more details.
		return 0, fmt.Errorf("[cpumanager] the static policy requires systemreserved.cpu + kubereserved.cpu to be greater than zero")
	}

	// Take the ceiling of the reservation, since fractional CPUs cannot be
	// exclusively allocated.
	reservedCPUsFloat := float64(reservedCPUs.MilliValue()) / 1000
	numReservedCPUs := int(math.Ceil(reservedCPUsFloat))
	return numReservedCPUs, nil
}

func GetKubeletReservedOptions(kubeletOptions *KubeletOptions, topology *topology.CPUTopology) (reservedSystemCPUs cpuset.CPUSet, kubeReserved, systemReserved corev1.ResourceList, err error) {
	reservedSystemCPUs, err = getReservedCPUs(topology, kubeletOptions.ReservedSystemCPUs)
	if err != nil {
		return
	}
	if reservedSystemCPUs.Size() > 0 {
		// at cmd option validation phase it is tested either --system-reserved-cgroup or --kube-reserved-cgroup is specified, so overwrite should be ok
		klog.InfoS("Option --reserved-cpus is specified, it will overwrite the cpu setting in KubeReserved and SystemReserved", "kubeReservedCPUs", kubeletOptions.KubeReserved, "systemReservedCPUs", kubeletOptions.SystemReserved)
		if kubeletOptions.KubeReserved != nil {
			delete(kubeletOptions.KubeReserved, "cpu")
		}
		if kubeletOptions.SystemReserved == nil {
			kubeletOptions.SystemReserved = make(map[string]string)
		}
		kubeletOptions.SystemReserved["cpu"] = strconv.Itoa(reservedSystemCPUs.Size())
		klog.InfoS("After cpu setting is overwritten", "kubeReservedCPUs", kubeletOptions.KubeReserved, "systemReservedCPUs", kubeletOptions.SystemReserved)
	}

	kubeReserved, err = parseResourceList(kubeletOptions.KubeReserved)
	if err != nil {
		return
	}
	systemReserved, err = parseResourceList(kubeletOptions.SystemReserved)
	if err != nil {
		return
	}
	return
}

func getReservedCPUs(topology *topology.CPUTopology, cpus string) (cpuset.CPUSet, error) {
	emptyCPUSet := cpuset.NewCPUSet()

	if cpus == "" {
		return emptyCPUSet, nil
	}

	reservedCPUSet, err := cpuset.Parse(cpus)
	if err != nil {
		return emptyCPUSet, fmt.Errorf("unable to parse reserved-cpus list: %s", err)
	}

	allCPUSet := topology.CPUDetails.CPUs()
	if !reservedCPUSet.IsSubsetOf(allCPUSet) {
		return emptyCPUSet, fmt.Errorf("reserved-cpus: %s is not a subset of online-cpus: %s", cpus, allCPUSet.String())
	}
	return reservedCPUSet, nil
}

// parseResourceList parses the given configuration map into an API
// ResourceList or returns an error.
func parseResourceList(m map[string]string) (corev1.ResourceList, error) {
	if len(m) == 0 {
		return nil, nil
	}
	rl := make(corev1.ResourceList)
	for k, v := range m {
		switch corev1.ResourceName(k) {
		// CPU, memory, local storage, and PID resources are supported.
		case corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourceEphemeralStorage, pidlimit.PIDs:
			q, err := resource.ParseQuantity(v)
			if err != nil {
				return nil, err
			}
			if q.Sign() == -1 {
				return nil, fmt.Errorf("resource quantity for %q cannot be negative: %v", k, v)
			}
			rl[corev1.ResourceName(k)] = q
		default:
			return nil, fmt.Errorf("cannot reserve %q resource", k)
		}
	}
	return rl, nil
}

func GetNodeAllocatableReservation(numCPUs int, totalMemoryInBytes int64, evictionHard map[string]string, systemReserved, kubeReserved corev1.ResourceList, experimentalNodeAllocatableIgnoreEvictionThreshold bool) (corev1.ResourceList, error) {
	hardEvictionThresholds, err := getKubeletHardEvictionThresholds(evictionHard, experimentalNodeAllocatableIgnoreEvictionThreshold)
	if err != nil {
		return nil, err
	}

	capacity := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(int64(numCPUs*1000), resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(totalMemoryInBytes, resource.BinarySI),
	}

	evictionReservation := hardEvictionReservation(hardEvictionThresholds, capacity)
	result := make(corev1.ResourceList)
	for k := range capacity {
		value := resource.NewQuantity(0, resource.DecimalSI)
		if systemReserved != nil {
			value.Add(systemReserved[k])
		}
		if kubeReserved != nil {
			value.Add(kubeReserved[k])
		}
		if evictionReservation != nil {
			value.Add(evictionReservation[k])
		}
		if !value.IsZero() {
			result[k] = *value
		}
	}
	return result, nil
}

func getKubeletHardEvictionThresholds(evictionHard map[string]string, experimentalNodeAllocatableIgnoreEvictionThreshold bool) ([]evictionapi.Threshold, error) {
	var hardEvictionThresholds []evictionapi.Threshold
	// If the user requested to ignore eviction thresholds, then do not set valid values for hardEvictionThresholds here.
	if !experimentalNodeAllocatableIgnoreEvictionThreshold {
		var err error
		hardEvictionThresholds, err = eviction.ParseThresholdConfig([]string{}, evictionHard, nil, nil, nil)
		if err != nil {
			return nil, err
		}
	}
	return hardEvictionThresholds, nil
}

// hardEvictionReservation returns a resourcelist that includes reservation of resources based on hard eviction thresholds.
func hardEvictionReservation(thresholds []evictionapi.Threshold, capacity corev1.ResourceList) corev1.ResourceList {
	if len(thresholds) == 0 {
		return nil
	}
	ret := corev1.ResourceList{}
	for _, threshold := range thresholds {
		if threshold.Operator != evictionapi.OpLessThan {
			continue
		}
		switch threshold.Signal {
		case evictionapi.SignalMemoryAvailable:
			memoryCapacity := capacity[corev1.ResourceMemory]
			value := evictionapi.GetThresholdQuantity(threshold.Value, &memoryCapacity)
			ret[corev1.ResourceMemory] = *value
		case evictionapi.SignalNodeFsAvailable:
			storageCapacity := capacity[corev1.ResourceEphemeralStorage]
			value := evictionapi.GetThresholdQuantity(threshold.Value, &storageCapacity)
			ret[corev1.ResourceEphemeralStorage] = *value
		}
	}
	return ret
}

func GetCPUManagerStateFilePath(rootDirectory string) string {
	return filepath.Join(rootDirectory, "cpu_manager_state")
}
