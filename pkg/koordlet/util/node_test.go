package util

import (
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_GetKubeQosRelativePath(t *testing.T) {

	guaranteedPathSystemd := GetKubeQosRelativePath(corev1.PodQOSGuaranteed)
	assert.Equal(t, path.Clean(sysutil.KubeRootNameSystemd), guaranteedPathSystemd)

	burstablePathSystemd := GetKubeQosRelativePath(corev1.PodQOSBurstable)
	assert.Equal(t, path.Join(sysutil.KubeRootNameSystemd, sysutil.KubeBurstableNameSystemd), burstablePathSystemd)

	besteffortPathSystemd := GetKubeQosRelativePath(corev1.PodQOSBestEffort)
	assert.Equal(t, path.Join(sysutil.KubeRootNameSystemd, sysutil.KubeBesteffortNameSystemd), besteffortPathSystemd)

	sysutil.SetupCgroupPathFormatter(sysutil.Cgroupfs)
	guaranteedPathCgroupfs := GetKubeQosRelativePath(corev1.PodQOSGuaranteed)
	assert.Equal(t, path.Clean(sysutil.KubeRootNameCgroupfs), guaranteedPathCgroupfs)

	burstablePathCgroupfs := GetKubeQosRelativePath(corev1.PodQOSBurstable)
	assert.Equal(t, path.Join(sysutil.KubeRootNameCgroupfs, sysutil.KubeBurstableNameCgroupfs), burstablePathCgroupfs)

	besteffortPathCgroupfs := GetKubeQosRelativePath(corev1.PodQOSBestEffort)
	assert.Equal(t, path.Join(sysutil.KubeRootNameCgroupfs, sysutil.KubeBesteffortNameCgroupfs), besteffortPathCgroupfs)
}
