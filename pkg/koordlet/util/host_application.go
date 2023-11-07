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

package util

import (
	"path/filepath"

	corev1 "k8s.io/api/core/v1"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

const (
	defaultHostLSCgroupDir = "host-latency-sensitive"
	defaultHostBECgroupDir = "host-best-effort"
)

func GetHostAppCgroupRelativePath(hostAppSpec *slov1alpha1.HostApplicationSpec) string {
	if hostAppSpec == nil {
		return ""
	}
	if hostAppSpec.CgroupPath == nil {
		cgroupBaseDir := ""
		switch hostAppSpec.QoS {
		case ext.QoSLSE, ext.QoSLSR, ext.QoSLS:
			cgroupBaseDir = defaultHostLSCgroupDir
		case ext.QoSBE:
			cgroupBaseDir = defaultHostBECgroupDir
			// empty string for QoSNone as default
		}
		return filepath.Join(cgroupBaseDir, hostAppSpec.Name)
	} else {
		cgroupBaseDir := ""
		switch hostAppSpec.CgroupPath.Base {
		case slov1alpha1.CgroupBaseTypeKubepods:
			cgroupBaseDir = GetPodQoSRelativePath(corev1.PodQOSGuaranteed)
		case slov1alpha1.CgroupBaseTypeKubeBurstable:
			cgroupBaseDir = GetPodQoSRelativePath(corev1.PodQOSBurstable)
		case slov1alpha1.CgroupBaseTypeKubeBesteffort:
			cgroupBaseDir = GetPodQoSRelativePath(corev1.PodQOSBestEffort)
			// empty string for CgroupBaseTypeRoot as default
		}
		return filepath.Join(cgroupBaseDir, hostAppSpec.CgroupPath.ParentDir, hostAppSpec.CgroupPath.RelativePath)
	}
}
