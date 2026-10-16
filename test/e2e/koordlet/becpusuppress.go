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

package koordlet

import (
	"context"
	"strconv"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	imageutils "github.com/koordinator-sh/koordinator/test/utils/image"
)

const cgroupQuotaCmd = `
if [ -d /host-cgroup ]; then
  # Find the besteffort parent cgroup (avoiding pod-level cgroups)
  be_cgroup=$(find /host-cgroup -type d -name "*besteffort*" | grep -E "(kubepods\.slice/kubepods-besteffort\.slice$|kubepods/besteffort$)" | head -1)
  if [ -n "$be_cgroup" ]; then
    if [ -f "$be_cgroup/cpu.max" ]; then
      val=$(awk '{print $1}' "$be_cgroup/cpu.max")
      if [ "$val" = "max" ]; then echo "-1"; else echo "$val"; fi
      exit 0
    elif [ -f "$be_cgroup/cpu.cfs_quota_us" ]; then
      cat "$be_cgroup/cpu.cfs_quota_us"
      exit 0
    fi
  fi
fi
echo "NOT_SUPPORTED"
`

var _ = SIGDescribe("BECPUSuppress", func() {
	f := framework.NewDefaultFramework("becpusuppress")

	framework.KoordinatorDescribe("BECPUSuppress [koordlet]", func() {
		framework.ConformanceIt("should throttle cpu.cfs_quota_us on BE pod when BECPUSuppress is enabled", func() {
			c := f.ClientSet

			configStr := `{"clusterStrategy": {"cpuSuppressThresholdPercent": 1, "cpuSuppressPolicy": "cfsQuota", "enable": true}}`
			cleanup := PatchSLOConfig(c, framework.TestContext.KoordinatorComponentNamespace, configStr)
			defer cleanup()

			// Create BE pod
			podName := "be-pod-suppress-" + framework.RandomSuffix()
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "busybox",
							Image:   imageutils.GetE2EImage(imageutils.BusyBox),
							Command: []string{"/bin/sh", "-c", "while true; do :; done"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "cgroup", MountPath: "/host-cgroup"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "cgroup", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys/fs/cgroup"}}},
					},
				},
			}

			_, err := c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), podName, metav1.DeleteOptions{})
			}()

			waitForPodRunning(f, c, pod)

			// Check cgroup accessibility before polling
			out, _, err := f.ExecCommandInContainerWithFullOutput(podName, "busybox", "/bin/sh", "-c", cgroupQuotaCmd)
			framework.ExpectNoError(err, "failed to exec cgroup detection command")
			if strings.TrimSpace(out) == "NOT_SUPPORTED" {
				ginkgo.Skip("cgroup not supported or not found in testing environment")
			}

			// Poll for cgroup cpu quota to be set (suppressed)
			gomega.Eventually(func() bool {
				out, _, err := f.ExecCommandInContainerWithFullOutput(podName, "busybox", "/bin/sh", "-c", cgroupQuotaCmd)
				if err != nil {
					return false
				}
				outStr := strings.TrimSpace(out)
				quota, err := strconv.Atoi(outStr)
				if err != nil {
					return false
				}
				return quota > 0 && quota != -1
			}, 90, 5).Should(gomega.BeTrue())
		})

		framework.ConformanceIt("should restore cpu.cfs_quota_us to unlimited after BECPUSuppress is disabled", func() {
			c := f.ClientSet

			configStr := `{"clusterStrategy": {"cpuSuppressThresholdPercent": 1, "cpuSuppressPolicy": "cfsQuota", "enable": true}}`
			cleanup := PatchSLOConfig(c, framework.TestContext.KoordinatorComponentNamespace, configStr)
			defer cleanup()

			// Create BE pod
			podName := "be-pod-suppress-restore-" + framework.RandomSuffix()
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: f.Namespace.Name,
					Labels: map[string]string{
						apiext.LabelPodQoS: string(apiext.QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "busybox",
							Image:   imageutils.GetE2EImage(imageutils.BusyBox),
							Command: []string{"/bin/sh", "-c", "while true; do :; done"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "cgroup", MountPath: "/host-cgroup"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "cgroup", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys/fs/cgroup"}}},
					},
				},
			}

			_, err := c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			defer func() {
				_ = c.CoreV1().Pods(f.Namespace.Name).Delete(context.TODO(), podName, metav1.DeleteOptions{})
			}()

			waitForPodRunning(f, c, pod)

			// Check cgroup accessibility before polling
			out, _, err := f.ExecCommandInContainerWithFullOutput(podName, "busybox", "/bin/sh", "-c", cgroupQuotaCmd)
			framework.ExpectNoError(err, "failed to exec cgroup detection command")
			if strings.TrimSpace(out) == "NOT_SUPPORTED" {
				ginkgo.Skip("cgroup not supported or not found in testing environment")
			}

			// Poll for cgroup cpu quota to be set (suppressed)
			gomega.Eventually(func() bool {
				out, _, err := f.ExecCommandInContainerWithFullOutput(podName, "busybox", "/bin/sh", "-c", cgroupQuotaCmd)
				if err != nil {
					return false
				}
				outStr := strings.TrimSpace(out)
				quota, err := strconv.Atoi(outStr)
				if err != nil {
					return false
				}
				return quota > 0 && quota != -1
			}, 90, 5).Should(gomega.BeTrue())

			// Disable suppression
			cmUpdate, err := c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Get(context.TODO(), "slo-controller-config", metav1.GetOptions{})
			framework.ExpectNoError(err)
			cmUpdate.Data[configuration.ResourceThresholdConfigKey] = `{"clusterStrategy": {"enable": false}}`
			_, err = c.CoreV1().ConfigMaps(framework.TestContext.KoordinatorComponentNamespace).Update(context.TODO(), cmUpdate, metav1.UpdateOptions{})
			framework.ExpectNoError(err)

			// Poll for cgroup cpu quota to be restored to -1 (unlimited)
			gomega.Eventually(func() bool {
				out, _, err := f.ExecCommandInContainerWithFullOutput(podName, "busybox", "/bin/sh", "-c", cgroupQuotaCmd)
				if err != nil {
					return false
				}
				outStr := strings.TrimSpace(out)
				quota, err := strconv.Atoi(outStr)
				if err != nil {
					return false
				}
				return quota == -1
			}, 90, 5).Should(gomega.BeTrue())
		})
	})
})
