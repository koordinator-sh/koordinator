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

package slocontroller

import (
	"context"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	k8spodutil "k8s.io/kubernetes/pkg/api/v1/pod"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

const colocationEnabledConfigData = `{
  "enable": true,
  "cpuReclaimThresholdPercent": 95,
  "memoryReclaimThresholdPercent": 95,
  "memoryCalculatePolicy": "usage"
}`

var (
	cpuReclaimThresholdPercent    = 95
	memoryReclaimThresholdPercent = 95
	maxNodeBatchCPUDiffPercent    = 10
	maxNodeBatchMemoryDiffPercent = 5

	minNodesBatchResourceAllocatableRatio = 0.7
)

var _ = SIGDescribe("BatchResource", func() {
	var nodeList *corev1.NodeList
	var c clientset.Interface
	var koordClient koordinatorclientset.Interface
	var koordNamespace, sloConfigName, koordSchedulerName string
	var err error

	f := framework.NewDefaultFramework("batchresource")

	ginkgo.BeforeEach(func() {
		c = f.ClientSet
		koordClient = f.KoordinatorClientSet
		koordNamespace = framework.TestContext.KoordinatorComponentNamespace
		sloConfigName = framework.TestContext.SLOCtrlConfigMap
		koordSchedulerName = framework.TestContext.KoordSchedulerName

		framework.Logf("get some nodes which are ready and schedulable")
		nodeList, err = e2enode.GetReadySchedulableNodes(c)
		framework.ExpectNoError(err)

		// fail the test when no node available
		gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())
	})

	framework.KoordinatorDescribe("BatchResource AllocatableUpdate", func() {
		framework.ConformanceIt("update batch resources in the node allocatable", func() {
			ginkgo.By("Loading slo-controller-config in the cluster")
			isConfigCreated := false
			configMap, err := c.CoreV1().ConfigMaps(koordNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
			if err == nil {
				isConfigCreated = true
				framework.Logf("successfully get slo-controller-config %s/%s", koordNamespace, sloConfigName)
			} else if errors.IsNotFound(err) {
				framework.Logf("cannot get slo-controller-config %s/%s, try to create a new one",
					koordNamespace, sloConfigName)
			} else {
				framework.Failf("failed to get slo-controller-config %s/%s, got unexpected error: %v",
					koordNamespace, sloConfigName, err)
			}

			// If configmap is created, try to patch it with colocation enabled.
			// If not exist, create the slo-controller-config.
			// NOTE: slo-controller-config should not be modified by the others during the e2e test.
			ginkgo.By("Prepare slo-controller-config to enable colocation")
			if isConfigCreated {
				needUpdate := false
				rollbackData := map[string]string{}
				if configMap.Data == nil {
					needUpdate = true
				} else if configMap.Data[configuration.ColocationConfigKey] != colocationEnabledConfigData {
					rollbackData[configuration.ColocationConfigKey] = configMap.Data[configuration.ColocationConfigKey]
					needUpdate = true
				}

				if _, ok := rollbackData[configuration.ColocationConfigKey]; ok && needUpdate {
					defer rollbackSLOConfigData(f, koordNamespace, sloConfigName, rollbackData)
				}

				if needUpdate {
					framework.Logf("colocation is not enabled in slo-controller-config, need update")
					newConfigMap := configMap.DeepCopy()
					newConfigMap.Data[configuration.ColocationConfigKey] = colocationEnabledConfigData
					newConfigMapUpdated, err := c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
					framework.ExpectNoError(err)
					framework.Logf("update slo-controller-config successfully, data: %+v", newConfigMapUpdated.Data)
					configMap = newConfigMapUpdated
				} else {
					framework.Logf("colocation is already enabled in slo-controller-config, keep the same")
				}
			} else {
				framework.Logf("slo-controller-config does not exist, need create")
				newConfigMap, err := manifest.ConfigMapFromManifest("slocontroller/slo-controller-config.yaml")
				framework.ExpectNoError(err)

				newConfigMap.SetNamespace(koordNamespace)
				newConfigMap.SetName(sloConfigName)
				newConfigMap.Data[configuration.ColocationConfigKey] = colocationEnabledConfigData

				newConfigMapCreated, err := c.CoreV1().ConfigMaps(koordNamespace).Create(context.TODO(), newConfigMap, metav1.CreateOptions{})
				framework.ExpectNoError(err)
				framework.Logf("create slo-controller-config successfully, data: %+v", newConfigMapCreated.Data)
				configMap = newConfigMapCreated

				defer rollbackSLOConfigObject(f, koordNamespace, sloConfigName)
			}

			ginkgo.By("Check node allocatable for batch resources")
			totalCount, allocatableCount := len(nodeList.Items), 0 // assert totalCount > 0
			gomega.Eventually(func() bool {
				for i := range nodeList.Items {
					node := &nodeList.Items[i]

					nodeMetric, err := koordClient.SloV1alpha1().NodeMetrics().Get(context.TODO(), node.Name, metav1.GetOptions{})
					if err != nil {
						framework.Logf("failed to get node metric for node %s, err: %v", node.Name, err)
						continue
					}

					// check node allocatable
					isAllocatable, msg := isNodeBatchResourcesValid(node, nodeMetric)
					if !isAllocatable {
						framework.Logf("node %s has no allocatable batch resource, msg: %s", node.Name, msg)
						continue
					}

					allocatableCount++
				}

				if float64(allocatableCount) > float64(totalCount)*minNodesBatchResourceAllocatableRatio {
					framework.Logf("finish checking node batch resources, total[%v], allocatable[%v]",
						totalCount, allocatableCount)
					return true
				}

				framework.Logf("there should be enough nodes that have batch resources allocatable, but got:"+
					" total[%v], allocatable[%v]", totalCount, allocatableCount)
				// reset nodes and counters
				nodeList, err = e2enode.GetReadySchedulableNodes(c)
				framework.ExpectNoError(err)
				gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())
				totalCount, allocatableCount = len(nodeList.Items), 0

				return false
			}, 180*time.Second, 5*time.Second).Should(gomega.Equal(true))

			framework.Logf("check node batch resources finished, total[%v], allocatable[%v]", totalCount, allocatableCount)

			framework.Logf("start to verify node batch resources scheduling")

			ginkgo.By("Loading Pod from manifest")
			pod, err := manifest.PodFromManifest("slocontroller/be-demo.yaml")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			pod.Namespace = f.Namespace.Name
			pod.Spec.SchedulerName = koordSchedulerName
			gomega.Expect(len(pod.Spec.Containers)).Should(gomega.Equal(1))

			ginkgo.By("Create a Batch Pod")
			f.PodClient().Create(pod)
			defer func() {
				err = f.PodClient().Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
				framework.ExpectNoError(err)
			}()

			ginkgo.By("Wait for Batch Pod Ready")
			gomega.Eventually(func() bool {
				p, err := f.PodClient().Get(context.TODO(), pod.Name, metav1.GetOptions{})
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				_, podReady := k8spodutil.GetPodCondition(&p.Status, corev1.PodReady)
				_, containersReady := k8spodutil.GetPodCondition(&p.Status, corev1.ContainersReady)
				framework.Logf("created batch pod %s/%s and got status: %+v", p.Namespace, p.Name, p.Status)

				return podReady != nil && podReady.Status == corev1.ConditionTrue &&
					containersReady != nil && containersReady.Status == corev1.ConditionTrue
			}, 180*time.Second, 5*time.Second).Should(gomega.Equal(true))
		})
	})
})

func isNodeMetricValid(nodeMetric *slov1alpha1.NodeMetric) (bool, string) {
	if nodeMetric == nil || nodeMetric.Status.NodeMetric == nil || nodeMetric.Status.NodeMetric.NodeUsage.ResourceList == nil {
		return false, "node metric is incomplete"
	}
	_, ok := nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[corev1.ResourceCPU]
	if !ok {
		return false, "cpu usage is missing"
	}
	_, ok = nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[corev1.ResourceMemory]
	if !ok {
		return false, "memory usage is missing"
	}
	return true, ""
}

func isNodeBatchResourcesValid(node *corev1.Node, nodeMetric *slov1alpha1.NodeMetric) (bool, string) {
	// validate the node
	if node == nil || node.Status.Allocatable == nil {
		return false, "node is incomplete"
	}
	// validate the node batch resources
	batchMilliCPU, ok := node.Status.Allocatable[apiext.BatchCPU]
	if !ok {
		return false, "batch cpu is missing"
	}
	// batch cpu can be larger when cpu normalization ratio > 1.0
	if batchMilliCPU.Value() < 0 {
		return false, "batch cpu is illegal"
	}
	batchMemory, ok := node.Status.Allocatable[apiext.BatchMemory]
	if !ok {
		return false, "batch memory is missing"
	}
	if batchMemory.Value() < 0 || batchMemory.Value() > node.Status.Allocatable.Memory().Value() {
		return false, "batch memory is illegal"
	}
	// validate the node metric
	if isValid, msg := isNodeMetricValid(nodeMetric); !isValid {
		return false, msg
	}
	cpuUsage := nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[corev1.ResourceCPU]
	memoryUsage := nodeMetric.Status.NodeMetric.NodeUsage.ResourceList[corev1.ResourceMemory]
	// roughly check the batch resource results:
	// batch.total >= node.total - node.total * cpuReclaimRatio - nodeMetric.usage - node.total * maxDiffRatio
	estimatedBatchMilliCPULower := node.Status.Allocatable.Cpu().MilliValue()*int64(100-cpuReclaimThresholdPercent-maxNodeBatchCPUDiffPercent)/100 - cpuUsage.MilliValue()
	if batchMilliCPU.Value() < estimatedBatchMilliCPULower {
		return false, "batch cpu is too small"
	}
	estimatedBatchMemoryLower := node.Status.Allocatable.Memory().Value()*int64(100-memoryReclaimThresholdPercent-maxNodeBatchMemoryDiffPercent)/100 - memoryUsage.Value()
	if batchMemory.Value() < estimatedBatchMemoryLower {
		return false, "batch memory is too small"
	}

	return true, ""
}

// restore the slo-controller-config by updating with the initial data
func rollbackSLOConfigData(f *framework.Framework, sloConfigNamespace, sloConfigName string, rollbackData map[string]string) {
	configMap, err := f.ClientSet.CoreV1().ConfigMaps(sloConfigNamespace).Get(context.TODO(), sloConfigName, metav1.GetOptions{})
	framework.ExpectNoError(err)
	newConfigMap := configMap.DeepCopy()
	for k, v := range rollbackData {
		newConfigMap.Data[k] = v
	}
	newConfigMap, err = f.ClientSet.CoreV1().ConfigMaps(sloConfigNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err)
	framework.Logf("finish rollback updating slo-controller-config, final data: %v", newConfigMap.Data)
}

// delete slo-controller-config configmap if it does not exist initially
func rollbackSLOConfigObject(f *framework.Framework, sloConfigNamespace, sloConfigName string) {
	err := f.ClientSet.CoreV1().ConfigMaps(sloConfigNamespace).Delete(context.TODO(), sloConfigName, metav1.DeleteOptions{})
	framework.ExpectNoError(err)
	framework.Logf("finish deleting slo-controller-config")
}
