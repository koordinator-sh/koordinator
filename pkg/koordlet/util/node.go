package util

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	nodesv1beta1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

// GetNodeKey returns a generated key with given node
func GetNodeKey(node *corev1.Node) string {
	return fmt.Sprintf("%v/%v", node.GetNamespace(), node.GetName())
}

// GetNodeMetricKey returns a generated key with given nodeMetric
func GetNodeMetricKey(nodeMetric *nodesv1beta1.NodeMetric) string {
	return fmt.Sprintf("%v/%v", nodeMetric.GetNamespace(), nodeMetric.GetName())
}

// @output like kubepods.slice/kubepods-besteffort.slice/
func GetKubeQosRelativePath(qosClass corev1.PodQOSClass) string {
	return GetPodCgroupDirWithKube(sysutil.CgroupPathFormatter.QOSDirFn(qosClass))
}

// GetRootCgroupCPUSetDir gets the cpuset parent directory of the specified podQos' root cgroup
// @output /sys/fs/cgroup/cpuset/kubepods.slice/kubepods-besteffort.slice
func GetRootCgroupCPUSetDir(qosClass corev1.PodQOSClass) string {
	rootCgroupParentDir := GetKubeQosRelativePath(qosClass)
	return path.Join(sysutil.Conf.CgroupRootDir, sysutil.CgroupCPUSetDir, rootCgroupParentDir)
}

// GetRootCgroupCurCPUSet gets the current cpuset of the specified podQos' root cgroup
func GetRootCgroupCurCPUSet(qosClass corev1.PodQOSClass) ([]int32, error) {
	rawContent, err := sysutil.CgroupFileRead(GetKubeQosRelativePath(qosClass), sysutil.CPUSet)
	if err != nil {
		return nil, err
	}

	return ParseCPUSetStr(rawContent)
}

func GetRootCgroupCurCFSPeriod(qosClass corev1.PodQOSClass) (int64, error) {
	rawContent, err := sysutil.CgroupFileRead(GetKubeQosRelativePath(qosClass), sysutil.CPUCFSPeriod)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(rawContent), 10, 64)
}

func GetRootCgroupCurCFQuota(qosClass corev1.PodQOSClass) (int64, error) {
	rawContent, err := sysutil.CgroupFileRead(GetKubeQosRelativePath(qosClass), sysutil.CPUCFSQuota)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(rawContent), 10, 64)
}
