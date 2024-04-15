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

package system

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

type CgroupDriverType string

const (
	Cgroupfs CgroupDriverType = "cgroupfs"
	Systemd  CgroupDriverType = "systemd"

	kubeletDefaultCgroupDriver = Cgroupfs

	KubeRootNameSystemd       = "kubepods.slice/"
	KubeBurstableNameSystemd  = "kubepods-burstable.slice/"
	KubeBesteffortNameSystemd = "kubepods-besteffort.slice/"

	KubeRootNameCgroupfs       = "kubepods/"
	KubeBurstableNameCgroupfs  = "burstable/"
	KubeBesteffortNameCgroupfs = "besteffort/"

	RuntimeTypeDocker     = "docker"
	RuntimeTypeContainerd = "containerd"
	RuntimeTypePouch      = "pouch"
        RuntimeTypeCrio       = "cri-o"
	RuntimeTypeUnknown    = "unknown"
)

func (c CgroupDriverType) Validate() bool {
	return c == Cgroupfs || c == Systemd
}

type Formatter struct {
	ParentDir string
	QOSDirFn  func(qos corev1.PodQOSClass) string
	PodDirFn  func(qos corev1.PodQOSClass, podUID string) string
	// containerID format: "containerd://..." or "docker://...", return (containerd, HashID)
	ContainerDirFn func(id string) (string, string, error)

	PodIDParser       func(basename string) (string, error)
	ContainerIDParser func(basename string) (string, error)
}

var cgroupPathFormatterInSystemd = Formatter{
	ParentDir: KubeRootNameSystemd,
	QOSDirFn: func(qos corev1.PodQOSClass) string {
		switch qos {
		case corev1.PodQOSBurstable:
			return KubeBurstableNameSystemd
		case corev1.PodQOSBestEffort:
			return KubeBesteffortNameSystemd
		case corev1.PodQOSGuaranteed:
			return "/"
		}
		return "/"
	},
	PodDirFn: func(qos corev1.PodQOSClass, podUID string) string {
		id := strings.ReplaceAll(podUID, "-", "_")
		switch qos {
		case corev1.PodQOSBurstable:
			return fmt.Sprintf("kubepods-burstable-pod%s.slice/", id)
		case corev1.PodQOSBestEffort:
			return fmt.Sprintf("kubepods-besteffort-pod%s.slice/", id)
		case corev1.PodQOSGuaranteed:
			return fmt.Sprintf("kubepods-pod%s.slice/", id)
		}
		return "/"
	},
	ContainerDirFn: func(id string) (string, string, error) {
		hashID := strings.Split(id, "://")
		if len(hashID) < 2 {
			return RuntimeTypeUnknown, "", fmt.Errorf("parse container id %s failed", id)
		}

		switch hashID[0] {
		case RuntimeTypeDocker:
			return RuntimeTypeDocker, fmt.Sprintf("docker-%s.scope", hashID[1]), nil
		case RuntimeTypeContainerd:
			return RuntimeTypeContainerd, fmt.Sprintf("cri-containerd-%s.scope", hashID[1]), nil
		case RuntimeTypePouch:
			return RuntimeTypePouch, fmt.Sprintf("pouch-%s.scope", hashID[1]), nil
                case RuntimeTypeCrio:
                        return RuntimeTypeCrio, fmt.Sprintf("crio-%s.scope", hashID[1]), nil
		default:
			return RuntimeTypeUnknown, "", fmt.Errorf("unknown container protocol %s", id)
		}
	},
	PodIDParser: func(basename string) (string, error) {
		patterns := []struct {
			prefix string
			suffix string
		}{
			{
				prefix: "kubepods-besteffort-pod",
				suffix: ".slice",
			},
			{
				prefix: "kubepods-burstable-pod",
				suffix: ".slice",
			},
			{
				prefix: "kubepods-pod",
				suffix: ".slice",
			},
		}

		for i := range patterns {
			if strings.HasPrefix(basename, patterns[i].prefix) && strings.HasSuffix(basename, patterns[i].suffix) {
				return basename[len(patterns[i].prefix) : len(basename)-len(patterns[i].suffix)], nil
			}
		}
		return "", fmt.Errorf("fail to parse pod id: %v", basename)
	},
	ContainerIDParser: func(basename string) (string, error) {
		patterns := []struct {
			prefix string
			suffix string
		}{
			{
				prefix: "docker-",
				suffix: ".scope",
			},
			{
				prefix: "cri-containerd-",
				suffix: ".scope",
			},
			{
				prefix: "crio-",
				suffix: ".scope",
			},
		}

		for i := range patterns {
			if strings.HasPrefix(basename, patterns[i].prefix) && strings.HasSuffix(basename, patterns[i].suffix) {
				return basename[len(patterns[i].prefix) : len(basename)-len(patterns[i].suffix)], nil
			} 
		}
		if strings.HasPrefix(basename, "crio-") {
			return basename[len("crio-") : len(basename)], nil
		}
		return "", fmt.Errorf("fail to parse container id: %v", basename)
	},
}

var cgroupPathFormatterInCgroupfs = Formatter{
	ParentDir: KubeRootNameCgroupfs,
	QOSDirFn: func(qos corev1.PodQOSClass) string {
		switch qos {
		case corev1.PodQOSBurstable:
			return KubeBurstableNameCgroupfs
		case corev1.PodQOSBestEffort:
			return KubeBesteffortNameCgroupfs
		case corev1.PodQOSGuaranteed:
			return "/"
		}
		return "/"
	},
	PodDirFn: func(qos corev1.PodQOSClass, podUID string) string {
		return fmt.Sprintf("pod%s/", podUID)
	},
	ContainerDirFn: func(id string) (string, string, error) {
		hashID := strings.Split(id, "://")
		if len(hashID) < 2 {
			return RuntimeTypeUnknown, "", fmt.Errorf("parse container id %s failed", id)
		}
		if hashID[0] == RuntimeTypeDocker || hashID[0] == RuntimeTypeContainerd || hashID[0] == RuntimeTypePouch || hashID[0] == RuntimeTypeCrio {
			return hashID[0], fmt.Sprintf("%s", hashID[1]), nil
		} else {
			return RuntimeTypeUnknown, "", fmt.Errorf("unknown container protocol %s", id)
		}
	},
	PodIDParser: func(basename string) (string, error) {
		if strings.HasPrefix(basename, "pod") {
			return basename[len("pod"):], nil
		}
		return "", fmt.Errorf("fail to parse pod id: %v", basename)
	},
	ContainerIDParser: func(basename string) (string, error) {
		return basename, nil
	},
}

// CgroupPathFormatter is the cgroup driver formatter.
// It is initialized with a fastly looked-up type and will be slowly detected with the kubelet when the daemon starts.
var CgroupPathFormatter = GetCgroupFormatter()

// GetCgroupFormatter gets the cgroup formatter simply looking up the cgroup directory names.
func GetCgroupFormatter() Formatter {
	nodeName := os.Getenv("NODE_NAME")
	// setup cgroup path formatter from cgroup driver type
	driver := GetCgroupDriverFromCgroupName()
	if driver.Validate() {
		klog.Infof("Node %s use '%s' as cgroup driver guessed with the cgroup name", nodeName, string(driver))
		return GetCgroupPathFormatter(driver)
	}
	klog.V(4).Infof("can not guess cgroup driver from 'kubepods' cgroup name")
	return cgroupPathFormatterInSystemd
}

// GetCgroupDriver gets the cgroup driver both from the cgroup directory names and kubelet configs. Check kubelet
// config can be slow, so it should be called infrequently.
func GetCgroupDriver() CgroupDriverType {
	nodeName := os.Getenv("NODE_NAME")
	driver := GetCgroupDriverFromCgroupName()
	if driver.Validate() {
		klog.Infof("Node %s use '%s' as cgroup driver according to the cgroup name", nodeName, string(driver))
		return driver
	}

	// get cgroup driver from the kubelet config; it may take at most 60s
	driver, err := GetCgroupDriverFromKubelet(nodeName)
	if err != nil {
		klog.Errorf("failed to get cgroup driver from kubelet config: %v", err)
		return Systemd
	}

	klog.Infof("Node %s use '%s' as cgroup driver according to the kubelet config", nodeName, string(driver))
	return driver
}

func GetCgroupDriverFromKubelet(nodeName string) (CgroupDriverType, error) {
	var cgroupDriver CgroupDriverType
	if pollErr := wait.PollImmediate(time.Second*10, time.Minute, func() (bool, error) {
		cfg, err := config.GetConfig()
		if err != nil {
			klog.Errorf("failed to get kube restConfig. error: %v", err)
			return false, nil
		}
		kubeClient := clientset.NewForConfigOrDie(cfg)
		node, err := kubeClient.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("failed to get node %v. error: %v", nodeName, err)
			return false, nil
		}

		port := int(node.Status.DaemonEndpoints.KubeletEndpoint.Port)
		if driver, err := GetCgroupDriverFromKubeletPort(port); err == nil && driver.Validate() {
			cgroupDriver = driver
			return true, nil
		} else {
			klog.Errorf("failed to get cgroup driver from kubelet, retry...: %v", err)
			return false, nil
		}
	}); pollErr != nil {
		return "", pollErr
	}

	return cgroupDriver, nil
}

func GetCgroupPathFormatter(driver CgroupDriverType) Formatter {
	switch driver {
	case Systemd:
		return cgroupPathFormatterInSystemd
	case Cgroupfs:
		return cgroupPathFormatterInCgroupfs
	default:
		klog.Warningf("cgroup driver formatter not supported: '%s'", string(driver))
		return cgroupPathFormatterInSystemd
	}
}

func SetupCgroupPathFormatter(driver CgroupDriverType) {
	switch driver {
	case Systemd:
		CgroupPathFormatter = cgroupPathFormatterInSystemd
	case Cgroupfs:
		CgroupPathFormatter = cgroupPathFormatterInCgroupfs
	default:
		klog.Warningf("cgroup driver formatter not supported: '%s'", string(driver))
	}
}
