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
	"encoding/json"
	"math"
	"time"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	nrtclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	"github.com/koordinator-sh/koordinator/apis/extension"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

var (
	defaultCPUModelRatioCfg = configuration.ModelRatioCfg{
		BaseRatio:                    ptr.To[float64](1.5),
		TurboEnabledRatio:            ptr.To[float64](1.65),
		HyperThreadEnabledRatio:      ptr.To[float64](1.0),
		HyperThreadTurboEnabledRatio: ptr.To[float64](1.1),
	}

	ratioDiffEpsilon                     = 0.01
	minNodesCPUBasicInfoReadyRatio       = 0.7
	minNodesCPUNormalizationCorrectRatio = 0.7
)

var _ = SIGDescribe("CPUNormalization", func() {
	var nodeList *corev1.NodeList
	var c clientset.Interface
	var koordClient koordinatorclientset.Interface
	var nrtClient nrtclientset.Interface
	var koordNamespace, sloConfigName string
	var err error

	f := framework.NewDefaultFramework("cpunormalization")
	f.SkipNamespaceCreation = true

	ginkgo.BeforeEach(func() {
		c = f.ClientSet
		koordClient = f.KoordinatorClientSet
		koordNamespace = framework.TestContext.KoordinatorComponentNamespace
		sloConfigName = framework.TestContext.SLOCtrlConfigMap

		nrtClient, err = nrtclientset.NewForConfig(f.ClientConfig())
		framework.ExpectNoError(err, "unable to create NRT ClientSet")

		framework.Logf("get some nodes which are ready and schedulable")
		nodeList, err = e2enode.GetReadySchedulableNodes(c)
		framework.ExpectNoError(err)

		// fail the test when no node available
		gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())
	})

	framework.KoordinatorDescribe("CPUNormalization RatioUpdate", func() {
		framework.ConformanceIt("update cpu normalization ratios in the node annotations", func() {
			ginkgo.By("Check node cpu basic infos on NodeResourceTopology")
			// assert totalCount > 0
			// FIXME: the failures of NUMA topology reporting are ignored
			totalCount, validNodeCount, skippedCount := len(nodeList.Items), 0, 0
			var cpuModels []*extension.CPUBasicInfo
			gomega.Eventually(func() bool {
				for i := range nodeList.Items {
					node := &nodeList.Items[i]

					nodeMetric, err := koordClient.SloV1alpha1().NodeMetrics().Get(context.TODO(), node.Name, metav1.GetOptions{})
					if err != nil {
						framework.Logf("failed to get node metric for node %s, err: %v", node.Name, err)
						continue
					}
					// validate NodeMetric
					isMetricValid, msg := isNodeMetricValid(nodeMetric)
					if !isMetricValid {
						framework.Logf("node %s has no valid node metric, msg: %s", node.Name, msg)
						continue
					}

					nrt, err := nrtClient.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), node.Name, metav1.GetOptions{})
					if err != nil {
						framework.Logf("failed to get node resource topology for node %s, err: %v", node.Name, err)
						continue
					}
					// validate NodeResourceTopology
					isNRTValid, msg := isNRTValid(nrt)
					if !isNRTValid {
						skippedCount++
						framework.Logf("node %s has no valid node resource topology, msg: %s", node.Name, msg)
						continue
					}
					cpuBasicInfo, err := extension.GetCPUBasicInfo(nrt.Annotations)
					if err != nil {
						framework.Logf("nrt %s has no valid cpu basic info, err: %s", nrt.Name, err)
						continue
					}
					if cpuBasicInfo == nil {
						skippedCount++
						framework.Logf("nrt %s has no cpu basic info", nrt.Name)
						continue
					}
					cpuModels = append(cpuModels, cpuBasicInfo)

					validNodeCount++
				}

				// expect enough valid NRTs have cpu basic info
				if float64(validNodeCount+skippedCount) <= float64(totalCount)*minNodesCPUBasicInfoReadyRatio {
					framework.Logf("there should be enough nodes that have cpu basic info on NRT, but got:"+
						" total[%v], valid[%v], skipped[%v]", totalCount, validNodeCount, skippedCount)
					// reset nodes and counters
					nodeList, err = e2enode.GetReadySchedulableNodes(c)
					framework.ExpectNoError(err)
					gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())
					cpuModels = make([]*extension.CPUBasicInfo, 0)
					totalCount, validNodeCount, skippedCount = len(nodeList.Items), 0, 0
					return false
				}

				framework.Logf("finish checking node cpu basic info, total[%v], valid[%v], skipped[%v]",
					totalCount, validNodeCount, skippedCount)
				return true
			}, 180*time.Second, 5*time.Second).Should(gomega.Equal(true))

			cpuNormalizationStrategy := makeCPUNormalizationStrategyForModels(cpuModels)
			framework.Logf("prepare cpu ratio model: [%+v]", cpuNormalizationStrategy)
			cpuNormalizationConfigBytes, err := json.Marshal(cpuNormalizationStrategy)
			framework.ExpectNoError(err)
			cpuNormalizationData := string(cpuNormalizationConfigBytes)

			// NOTE: slo-controller-config should not be modified by the others during the e2e test.
			ginkgo.By("Prepare slo-controller-config to enable cpu normalization")
			defer ensureSLOConfigData(f, koordNamespace, sloConfigName, map[string]string{
				configuration.ColocationConfigKey:       colocationEnabledConfigData,
				configuration.CPUNormalizationConfigKey: cpuNormalizationData,
			})()

			ginkgo.By("Check node cpu normalization ratios")
			totalCount, validNodeCount, skippedCount = len(nodeList.Items), 0, 0
			gomega.Eventually(func() bool {
				framework.Logf("relist some nodes which are ready and schedulable")
				nodeList, err = e2enode.GetReadySchedulableNodes(c)
				framework.ExpectNoError(err)

				for i := range nodeList.Items {
					node := &nodeList.Items[i]
					ratio, err := extension.GetCPUNormalizationRatio(node)
					if err != nil {
						framework.Logf("failed to get cpu normalization ratio for node %s, err: %v", node.Name, err)
						continue
					}
					if ratio < 1.0 {
						skippedCount++
						framework.Logf("failed to get valid normalization ratio for node %s, skipped, ratio: %v",
							node.Name, ratio)
						continue
					}

					nrt, err := nrtClient.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), node.Name, metav1.GetOptions{})
					if err != nil {
						framework.Logf("failed to get node resource topology for node %s, err: %v", node.Name, err)
						continue
					}
					// validate NodeResourceTopology
					isNRTValid, msg := isNRTValid(nrt)
					if !isNRTValid {
						skippedCount++
						framework.Logf("node %s has no valid node resource topology, msg: %s", node.Name, msg)
						continue
					}
					cpuBasicInfo, err := extension.GetCPUBasicInfo(nrt.Annotations)
					if err != nil {
						framework.Logf("nrt %s has no valid cpu basic info, err: %s", nrt.Name, err)
						continue
					}
					if cpuBasicInfo == nil {
						skippedCount++
						framework.Logf("nrt %s has no cpu basic info", nrt.Name)
						continue
					}

					if expectedRatio := getCPUNormalizationRatioInDefaultModel(cpuBasicInfo); math.Abs(ratio-expectedRatio) > ratioDiffEpsilon {
						framework.Logf("node cpu normalization ratio is different from expected, node %s, "+
							"cpu model [%+v], expected %v, current %v", node.Name, cpuBasicInfo, expectedRatio, ratio)
						continue
					}

					framework.Logf("check node %s cpu normalization ratio successfully, ratio %v",
						node.Name, ratio)
					validNodeCount++
				}

				// expect enough correct node ratios
				if float64(validNodeCount+skippedCount) <= float64(totalCount)*minNodesCPUNormalizationCorrectRatio {
					framework.Logf("there should be enough nodes that have correct cpu normalization ratio, "+
						"but got: total[%v], valid[%v], skipped[%v]", totalCount, validNodeCount, skippedCount)
					// reset nodes and counters
					nodeList, err = e2enode.GetReadySchedulableNodes(c)
					framework.ExpectNoError(err)
					gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())
					totalCount, validNodeCount, skippedCount = len(nodeList.Items), 0, 0
					return false
				}

				framework.Logf("finish checking node cpu normalization ratio, total[%v], valid[%v], skipped[%v]",
					totalCount, validNodeCount, skippedCount)
				return true
			}, 120*time.Second, 5*time.Second).Should(gomega.Equal(true))
		})

		// TODO: submit a LS pod allocating cpu-normalized cpu resource
	})
})

func isNRTValid(nrt *topov1alpha1.NodeResourceTopology) (bool, string) {
	if nrt == nil || nrt.Annotations == nil {
		return false, "nrt is incomplete"
	}
	if len(nrt.Zones) <= 0 {
		return false, "nrt has no zone"
	}
	if len(nrt.TopologyPolicies) <= 0 {
		return false, "nrt has no topology policy"
	}
	return true, ""
}

func makeCPUNormalizationStrategyForModels(cpuModels []*extension.CPUBasicInfo) *configuration.CPUNormalizationStrategy {
	ratioModel := map[string]configuration.ModelRatioCfg{}
	for _, cpuModel := range cpuModels {
		ratioCfg, ok := ratioModel[cpuModel.CPUModel]
		if !ok {
			ratioCfg = configuration.ModelRatioCfg{}
		}
		if cpuModel.HyperThreadEnabled && cpuModel.TurboEnabled {
			ratioCfg.HyperThreadTurboEnabledRatio = defaultCPUModelRatioCfg.HyperThreadTurboEnabledRatio
		} else if cpuModel.HyperThreadEnabled {
			ratioCfg.HyperThreadEnabledRatio = defaultCPUModelRatioCfg.HyperThreadEnabledRatio
		} else if cpuModel.TurboEnabled {
			ratioCfg.TurboEnabledRatio = defaultCPUModelRatioCfg.TurboEnabledRatio
		} else {
			ratioCfg.BaseRatio = defaultCPUModelRatioCfg.BaseRatio
		}
		ratioModel[cpuModel.CPUModel] = ratioCfg
	}

	return &configuration.CPUNormalizationStrategy{
		Enable:     ptr.To[bool](true),
		RatioModel: ratioModel,
	}
}

func getCPUNormalizationRatioInDefaultModel(info *extension.CPUBasicInfo) float64 {
	if info.HyperThreadEnabled && info.TurboEnabled {
		return *defaultCPUModelRatioCfg.HyperThreadTurboEnabledRatio
	}
	if info.HyperThreadEnabled {
		return *defaultCPUModelRatioCfg.HyperThreadEnabledRatio
	}
	if info.TurboEnabled {
		return *defaultCPUModelRatioCfg.TurboEnabledRatio
	}
	return *defaultCPUModelRatioCfg.BaseRatio
}
