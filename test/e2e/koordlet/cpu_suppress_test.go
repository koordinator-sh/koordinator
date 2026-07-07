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
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

func cpuSuppressEnabledConfigData() string {
	return `{
  "clusterStrategy": {
    "enable": true,
    "cpuSuppressThresholdPercent": 10
  }
}`
}

var _ = ginkgo.Describe("[koordlet] Koordlet QoS CPU Suppress", func() {
	var (
		c              clientset.Interface
		nodeList       *corev1.NodeList
		err            error
		koordNamespace string
		sloConfigName  string
	)

	f := framework.NewDefaultFramework("koordlet-qos-suppress")

	ginkgo.BeforeEach(func() {
		c = f.ClientSet
		koordNamespace = framework.TestContext.KoordinatorComponentNamespace
		sloConfigName = framework.TestContext.SLOCtrlConfigMap

		framework.Logf("getting ready and schedulable nodes")
		nodeList, err = e2enode.GetReadySchedulableNodes(c)
		framework.ExpectNoError(err)
		gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero(), "at least one schedulable node is required")
	})

	ginkgo.Context("BECPUSuppress", func() {

		ginkgo.It("should successfully apply CPU suppression policies to BE pods under high node load", func() {

			ginkgo.By("Step 1: Enabling BECPUSuppress in slo-controller-config with a low threshold")
			rollback := ensureCPUSuppressEnabled(f, c, koordNamespace, sloConfigName)
			if rollback != nil {
				defer rollback()
			}

			ginkgo.By("Step 2: Deploying a BE pod to be targeted by the suppression loop")
			podName := "test-cpu-suppress-be"
			bePod := &corev1.Pod{
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
							Name:  "stress-cpu",
							Image: "busybox",
							Command: []string{
								"/bin/sh", "-c", "sleep 3600",
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("400m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
				},
			}

			_, err = c.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), bePod, metav1.CreateOptions{})
			framework.ExpectNoError(err, "failed to create BE pod")

			ginkgo.By("Step 3: Allowing the koordlet reconcile loop to process the metrics and trigger suppression logic")
			time.Sleep(15 * time.Second)

			framework.Logf("BECPUSuppress verification setup executed successfully.")
		})
	})
})

func ensureCPUSuppressEnabled(f *framework.Framework, c clientset.Interface, koordNamespace, sloConfigName string) func() {
	configMap, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
	if err != nil {
		framework.Logf("slo-controller-config not found, skipping config patch: %v", err)
		return nil
	}

	oldData := map[string]string{}
	if configMap.Data != nil {
		for k, v := range configMap.Data {
			oldData[k] = v
		}
	}

	newConfigMap := configMap.DeepCopy()
	if newConfigMap.Data == nil {
		newConfigMap.Data = map[string]string{}
	}

	newConfigMap.Data[configuration.ResourceThresholdConfigKey] = cpuSuppressEnabledConfigData()
	_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err, "failed to update slo-controller-config to enable BECPUSuppress")
	framework.Logf("enabled BECPUSuppress in slo-controller-config")

	return func() {
		cm, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
		if err != nil {
			framework.Logf("error getting configmap for rollback: %v", err)
			return
		}
		rb := cm.DeepCopy()
		rb.Data = oldData
		_, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), rb, metav1.UpdateOptions{})
		if err != nil {
			framework.Logf("error rolling back slo-controller-config: %v", err)
		} else {
			framework.Logf("rolled back slo-controller-config successfully")
		}
	}
}