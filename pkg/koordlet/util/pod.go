package util

import (
	"fmt"
	"io/ioutil"
	"path"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	resources "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

// @kubeRelativeDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @return kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
func GetPodCgroupDirWithKube(podKubeRelativeDir string) string {
	return path.Join(sysutil.CgroupPathFormatter.ParentDir, podKubeRelativeDir)
}

// @return like kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
func GetPodKubeRelativePath(pod *corev1.Pod) string {
	qosClass := GetKubeQosClass(pod)
	return path.Join(
		sysutil.CgroupPathFormatter.QOSDirFn(qosClass),
		sysutil.CgroupPathFormatter.PodDirFn(qosClass, string(pod.UID)),
	)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpuacct/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpuacct.proc_stat
func GetPodCgroupCPUAcctProcStatPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.CpuacctStat)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.shares
func GetPodCgroupCPUSharePath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.CPUShares)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.cfs_period_us
func GetPodCgroupCFSPeriodPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.CPUCFSPeriod)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.cfs_quota_us
func GetPodCgroupCFSQuotaPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.CPUCFSQuota)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpuset/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpuset.cpus
func GetPodCgroupCPUSetPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.CPUSet)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpuacct/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpuacct.proc_stat
func GetPodCgroupMemStatPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.MemStat)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/memory/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/memory.limit_in_bytes
func GetPodCgroupMemLimitPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.MemoryLimit)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.bvt_warp_ns
func GetPodCgroupCPUBvtPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.CPUBVTWarpNs)
}

// @podParentDir kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
// @output /sys/fs/cgroup/cpu/kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/cpu.stat
func GetPodCgroupCPUStatPath(podParentDir string) string {
	podPath := GetPodCgroupDirWithKube(podParentDir)
	return sysutil.GetCgroupFilePath(podPath, sysutil.CPUStat)
}

func GetPodMilliCPULimit(pod *corev1.Pod) int64 {
	podCPUMilliLimit := int64(0)
	for _, container := range pod.Spec.Containers {
		containerCPUMilliLimit := GetContainerMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			return -1
		}
		podCPUMilliLimit += containerCPUMilliLimit
	}
	for _, container := range pod.Spec.InitContainers {
		containerCPUMilliLimit := GetContainerMilliCPULimit(&container)
		if containerCPUMilliLimit <= 0 {
			return -1
		}
		podCPUMilliLimit = MaxInt64(podCPUMilliLimit, containerCPUMilliLimit)
	}
	if podCPUMilliLimit <= 0 {
		return -1
	}
	return podCPUMilliLimit
}

func GetPodMemoryByteLimit(pod *corev1.Pod) int64 {
	podMemoryByteLimit := int64(0)
	for _, container := range pod.Spec.Containers {
		containerMemByteLimit := GetContainerMemoryByteLimit(&container)
		if containerMemByteLimit <= 0 {
			return -1
		}
		podMemoryByteLimit += containerMemByteLimit
	}
	for _, container := range pod.Spec.InitContainers {
		containerMemByteLimit := GetContainerMemoryByteLimit(&container)
		if containerMemByteLimit <= 0 {
			return -1
		}
		podMemoryByteLimit = MaxInt64(podMemoryByteLimit, containerMemByteLimit)
	}
	if podMemoryByteLimit <= 0 {
		return -1
	}
	return podMemoryByteLimit
}

func GetPodCurCPUShare(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupCPUSharePath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetPodCurCFSPeriod(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupCFSPeriodPath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetPodCurCFSQuota(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupCFSQuotaPath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetPodCurMemLimitBytes(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupMemLimitPath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

func GetPodCurBvtValue(podParentDir string) (int64, error) {
	cgroupPath := GetPodCgroupCPUBvtPath(podParentDir)
	rawContent, err := ioutil.ReadFile(cgroupPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(rawContent)), 10, 64)
}

// getPodRequestFromContainers get pod request by summarizing pod containers' requests
func getPodRequestFromContainers(pod *corev1.Pod) corev1.ResourceList {
	result := corev1.ResourceList{}
	for _, container := range pod.Spec.Containers {
		result = resources.Add(result, container.Resources.Requests)
	}
	// take max_resource(sum_pod, any_init_container)
	for _, container := range pod.Spec.InitContainers {
		result = resources.Max(result, container.Resources.Requests)
	}
	// add pod overhead if it exists
	if pod.Spec.Overhead != nil {
		result = resources.Add(result, pod.Spec.Overhead)
	}
	return resources.Mask(result, []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory})
}

// GetPodRequest get pod request resource
func GetPodRequest(pod *corev1.Pod) corev1.ResourceList {
	return getPodRequestFromContainers(pod)
}

func GetPodKey(pod *corev1.Pod) string {
	return fmt.Sprintf("%v/%v", pod.GetNamespace(), pod.GetName())
}

func GetPodMetricKey(podMetric *slov1alpha1.PodMetricInfo) string {
	return fmt.Sprintf("%v/%v", podMetric.Namespace, podMetric.Name)
}

func GetKubeQosClass(pod *corev1.Pod) corev1.PodQOSClass {
	qosClass := pod.Status.QOSClass
	if qosClass == "" {
		qosClass = qos.GetPodQOS(pod)
	}
	return qosClass
}

// @return like kubepods.slice/kubepods-burstable.slice/
func GetPodQoSRelativePath(qosClass corev1.PodQOSClass) string {
	return path.Join(
		sysutil.CgroupPathFormatter.ParentDir,
		sysutil.CgroupPathFormatter.QOSDirFn(qosClass),
	)
}

// @return 7712555c_ce62_454a_9e18_9ff0217b8941 from kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice
func ParsePodID(basename string) (string, error) {
	return sysutil.CgroupPathFormatter.PodIDParser(basename)
}

// @return 7712555c_ce62_454a_9e18_9ff0217b8941 from docker-7712555c_ce62_454a_9e18_9ff0217b8941.scope
func ParseContainerID(basename string) (string, error) {
	return sysutil.CgroupPathFormatter.ContainerIDParser(basename)
}
