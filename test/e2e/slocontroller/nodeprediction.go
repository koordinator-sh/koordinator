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

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

const midColocationEnabledConfigData = `{
  "enable": true,
  "cpuReclaimThresholdPercent": 95,
  "memoryReclaimThresholdPercent": 95,
  "memoryCalculatePolicy": "usage",
  "midCPUThresholdPercent": 100,
  "midMemoryThresholdPercent": 100,
  "midUnallocatedPercent": 0
}`

// Poll until converged.
var (
	minNodesProdReclaimableReadyRatio = 0.7
	prodReclaimableCheckTimeout       = 300 * time.Second
	prodReclaimableCheckInterval      = 5 * time.Second
)

var _ = SIGDescribe("NodePrediction", func() {
	var nodeList *corev1.NodeList
	var c clientset.Interface
	var koordClient koordinatorclientset.Interface
	var koordNamespace, sloConfigName string
	var err error

	f := framework.NewDefaultFramework("nodeprediction")
	f.SkipNamespaceCreation = true

	ginkgo.BeforeEach(func() {
		c = f.ClientSet
		koordClient = f.KoordinatorClientSet
		koordNamespace = framework.TestContext.KoordinatorComponentNamespace
		sloConfigName = framework.TestContext.SLOCtrlConfigMap

		framework.Logf("get some nodes which are ready and schedulable")
		nodeList, err = e2enode.GetReadySchedulableNodes(c)
		framework.ExpectNoError(err)

		gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())
	})

	framework.KoordinatorDescribe("NodePrediction ProdReclaimable", func() {
		framework.ConformanceIt("report prod reclaimable prediction and update node Mid resources", func() {
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

			ginkgo.By("Prepare slo-controller-config to enable colocation and mid resource")
			if isConfigCreated {
				needUpdate := false
				rollbackData := map[string]string{}
				if configMap.Data == nil {
					needUpdate = true
				} else if configMap.Data[configuration.ColocationConfigKey] != midColocationEnabledConfigData {
					rollbackData[configuration.ColocationConfigKey] = configMap.Data[configuration.ColocationConfigKey]
					needUpdate = true
				}

				if _, ok := rollbackData[configuration.ColocationConfigKey]; ok && needUpdate {
					defer rollbackSLOConfigData(f, koordNamespace, sloConfigName, rollbackData)
				}

				if needUpdate {
					framework.Logf("colocation/mid is not enabled in slo-controller-config, need update")
					newConfigMap := configMap.DeepCopy()
					newConfigMap.Data[configuration.ColocationConfigKey] = midColocationEnabledConfigData
					newConfigMapUpdated, err := c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
					framework.ExpectNoError(err)
					framework.Logf("update slo-controller-config successfully, data: %+v", newConfigMapUpdated.Data)
					configMap = newConfigMapUpdated
				} else {
					framework.Logf("colocation/mid is already enabled in slo-controller-config, keep the same")
				}
			} else {
				framework.Logf("slo-controller-config does not exist, need create")
				newConfigMap, err := manifest.ConfigMapFromManifest("slocontroller/slo-controller-config.yaml")
				framework.ExpectNoError(err)

				newConfigMap.SetNamespace(koordNamespace)
				newConfigMap.SetName(sloConfigName)
				newConfigMap.Data[configuration.ColocationConfigKey] = midColocationEnabledConfigData

				newConfigMapCreated, err := c.CoreV1().ConfigMaps(koordNamespace).Create(context.TODO(), newConfigMap, metav1.CreateOptions{})
				framework.ExpectNoError(err)
				framework.Logf("create slo-controller-config successfully, data: %+v", newConfigMapCreated.Data)
				configMap = newConfigMapCreated

				defer rollbackSLOConfigObject(f, koordNamespace, sloConfigName)
			}

			// koordlet prediction -> NodeMetric -> slo-controller Mid allocatable.
			ginkgo.By("Check node prod reclaimable prediction on NodeMetric and Mid resources on node allocatable")
			totalCount, validCount := len(nodeList.Items), 0
			gomega.Eventually(func() bool {
				framework.Logf("relist some nodes which are ready and schedulable")
				nodeList, err = e2enode.GetReadySchedulableNodes(c)
				framework.ExpectNoError(err)
				gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())
				totalCount, validCount = len(nodeList.Items), 0

				for i := range nodeList.Items {
					node := &nodeList.Items[i]

					nodeMetric, err := koordClient.SloV1alpha1().NodeMetrics().Get(context.TODO(), node.Name, metav1.GetOptions{})
					if err != nil {
						framework.Logf("failed to get node metric for node %s, err: %v", node.Name, err)
						continue
					}

					if isValid, msg := isNodeProdReclaimableValid(node, nodeMetric); !isValid {
						framework.Logf("node %s has no valid prod reclaimable prediction, msg: %s", node.Name, msg)
						continue
					}

					if isValid, msg := isNodeMidResourcesValid(node, nodeMetric); !isValid {
						framework.Logf("node %s has no valid mid resource, msg: %s", node.Name, msg)
						continue
					}

					validCount++
				}

				if float64(validCount) > float64(totalCount)*minNodesProdReclaimableReadyRatio {
					framework.Logf("finish checking node prod reclaimable prediction, total[%v], valid[%v]",
						totalCount, validCount)
					return true
				}

				framework.Logf("there should be enough nodes that have valid prod reclaimable prediction, but got:"+
					" total[%v], valid[%v]", totalCount, validCount)
				return false
			}, prodReclaimableCheckTimeout, prodReclaimableCheckInterval).Should(gomega.Equal(true))
		})
	})
})

func isNodeProdReclaimableValid(node *corev1.Node, nodeMetric *slov1alpha1.NodeMetric) (bool, string) {
	if node == nil || node.Status.Allocatable == nil {
		return false, "node is incomplete"
	}
	if isValid, msg := isNodeMetricValid(nodeMetric); !isValid {
		return false, msg
	}
	prodReclaimable := nodeMetric.Status.ProdReclaimableMetric
	if prodReclaimable == nil || prodReclaimable.Resource.ResourceList == nil {
		return false, "prod reclaimable metric is missing"
	}
	// Bounds-only; zero is valid.
	reclaimableCPU := prodReclaimable.Resource.Cpu()
	if reclaimableCPU.MilliValue() < 0 || reclaimableCPU.MilliValue() > node.Status.Allocatable.Cpu().MilliValue() {
		return false, "prod reclaimable cpu is illegal"
	}
	reclaimableMemory := prodReclaimable.Resource.Memory()
	if reclaimableMemory.Value() < 0 || reclaimableMemory.Value() > node.Status.Allocatable.Memory().Value() {
		return false, "prod reclaimable memory is illegal"
	}
	return true, ""
}

func isNodeMidResourcesValid(node *corev1.Node, nodeMetric *slov1alpha1.NodeMetric) (bool, string) {
	if node == nil || node.Status.Allocatable == nil {
		return false, "node is incomplete"
	}
	// MidCPU is stored in milli-cores, so its Value() is the milli value
	midCPU, ok := node.Status.Allocatable[apiext.MidCPU]
	if !ok {
		return false, "mid cpu is missing"
	}
	if midCPU.Value() < 0 {
		return false, "mid cpu is illegal"
	}
	midMemory, ok := node.Status.Allocatable[apiext.MidMemory]
	if !ok {
		return false, "mid memory is missing"
	}
	if midMemory.Value() < 0 || midMemory.Value() > node.Status.Allocatable.Memory().Value() {
		return false, "mid memory is illegal"
	}

	// Consumption cross-check: with midUnallocatedPercent=0 
	prodReclaimable := nodeMetric.Status.ProdReclaimableMetric
	if prodReclaimable == nil || prodReclaimable.Resource.ResourceList == nil {
		return false, "prod reclaimable metric is missing"
	}
	if midCPU.Value() > prodReclaimable.Resource.Cpu().MilliValue() {
		return false, "mid cpu exceeds prod reclaimable prediction"
	}
	if midMemory.Value() > prodReclaimable.Resource.Memory().Value() {
		return false, "mid memory exceeds prod reclaimable prediction"
	}
	return true, ""
}
