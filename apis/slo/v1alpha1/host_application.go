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

package v1alpha1

import (
	"github.com/koordinator-sh/koordinator/apis/extension"
)

// HostApplicationSpec describes the QoS management for out-out-band applications on node
type HostApplicationSpec struct {
	Name string `json:"name,omitempty"`
	// Priority class of the application
	Priority extension.PriorityClass `json:"priority,omitempty"`
	// QoS class of the application
	QoS extension.QoSClass `json:"qos,omitempty"`
	// Optional, defines the host cgroup configuration, use default if not specified according to priority and qos
	CgroupPath *CgroupPath `json:"cgroupPath,omitempty"`
	// QoS Strategy of host application
	Strategy *HostApplicationStrategy `json:"strategy,omitempty"`
}

type HostApplicationStrategy struct {
}

// CgroupPath decribes the cgroup path for out-of-band applications
type CgroupPath struct {
	// cgroup base dir, the format is various across cgroup drivers
	Base CgroupBaseType `json:"base,omitempty"`
	// cgroup parent path under base dir
	ParentDir string `json:"parentDir,omitempty"`
	// cgroup relative path under parent dir
	RelativePath string `json:"relativePath,omitempty"`
}

// CgroupBaseType defines the cgroup base dir for HostCgroup
type CgroupBaseType string

const (
	// CgroupBaseTypeRoot is the root dir of cgroup fs on node, e.g. /sys/fs/cgroup/cpu/
	CgroupBaseTypeRoot CgroupBaseType = "CgroupRoot"
	// CgroupBaseTypeRoot is the cgroup dir for k8s pods, e.g. /sys/fs/cgroup/cpu/kubepods/
	CgroupBaseTypeKubepods CgroupBaseType = "Kubepods"
	// CgroupBaseTypeRoot is the cgroup dir for k8s burstable pods, e.g. /sys/fs/cgroup/cpu/kubepods/burstable/
	CgroupBaseTypeKubeBurstable CgroupBaseType = "KubepodsBurstable"
	// CgroupBaseTypeRoot is the cgroup dir for k8s besteffort pods, e.g. /sys/fs/cgroup/cpu/kubepods/besteffort/
	CgroupBaseTypeKubeBesteffort CgroupBaseType = "KubepodsBesteffort"
)
