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

	"github.com/onsi/ginkgo/v2"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

// SIGDescribe annotates the test with the SIG label.
func SIGDescribe(text string, body func()) bool {
	return ginkgo.Describe("[koordlet] "+text, body)
}

func waitForPodRunning(f *framework.Framework, c clientset.Interface, pod *corev1.Pod) {
	framework.ExpectNoError(e2epod.WaitForPodRunningInNamespace(c, pod), "unable to schedule the pod")
}

// PatchSLOConfig patches the slo-controller-config ConfigMap and returns a cleanup function to defer.
func PatchSLOConfig(c clientset.Interface, ns string, configStr string) func() {
	cm, err := c.CoreV1().ConfigMaps(ns).Get(context.TODO(), "slo-controller-config", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		framework.Failf("failed to get slo-controller-config, err: %v", err)
	}

	if err == nil {
		oldData := cm.DeepCopy().Data
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data[configuration.ResourceThresholdConfigKey] = configStr
		_, err = c.CoreV1().ConfigMaps(ns).Update(context.TODO(), cm, metav1.UpdateOptions{})
		framework.ExpectNoError(err)

		return func() {
			cmToRestore, err := c.CoreV1().ConfigMaps(ns).Get(context.TODO(), "slo-controller-config", metav1.GetOptions{})
			framework.ExpectNoError(err)
			cmToRestore.Data = oldData
			_, err = c.CoreV1().ConfigMaps(ns).Update(context.TODO(), cmToRestore, metav1.UpdateOptions{})
			framework.ExpectNoError(err)
		}
	} else {
		newConfigMap, err := manifest.ConfigMapFromManifest("slocontroller/slo-controller-config.yaml")
		framework.ExpectNoError(err)
		newConfigMap.SetNamespace(ns)
		newConfigMap.SetName("slo-controller-config")
		if newConfigMap.Data == nil {
			newConfigMap.Data = make(map[string]string)
		}
		newConfigMap.Data[configuration.ResourceThresholdConfigKey] = configStr

		_, err = c.CoreV1().ConfigMaps(ns).Create(context.TODO(), newConfigMap, metav1.CreateOptions{})
		framework.ExpectNoError(err)

		return func() {
			_ = c.CoreV1().ConfigMaps(ns).Delete(context.TODO(), "slo-controller-config", metav1.DeleteOptions{})
		}
	}
}
