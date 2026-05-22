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
	"encoding/json"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	"github.com/koordinator-sh/koordinator/apis/configuration"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	koordinatorclientset "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/manifest"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
)

var _ = SIGDescribe("BlkIOQoS", func() {
	var nodeList *corev1.NodeList
	var c clientset.Interface
	var koordClient koordinatorclientset.Interface
	var koordNamespace, sloConfigName string
	var err error

	f := framework.NewDefaultFramework("blkioqos")
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

	framework.KoordinatorDescribe("BlkIOQoS ConfigUpdate", func() {
		framework.ConformanceIt("updates blkio qos in NodeSLO", func() {
			expectedBlkIOQOS, blkIOQOSData := makeBlkIOQOSConfig()

			ginkgo.By("Loading slo-controller-config in the cluster")
			configMap, createdForTest := ensureSLOConfigMap(c, koordNamespace, sloConfigName)
			if createdForTest {
				defer rollbackSLOConfigObject(f, koordNamespace, sloConfigName)
			} else {
				rollbackData, hadKey := configMap.Data[configuration.ResourceQOSConfigKey]
				defer rollbackSLOConfigData(f, koordNamespace, sloConfigName, configuration.ResourceQOSConfigKey, rollbackData, hadKey)
			}

			ginkgo.By("Prepare slo-controller-config to enable blkio qos")
			newConfigMap := configMap.DeepCopy()
			if newConfigMap.Data == nil {
				newConfigMap.Data = map[string]string{}
			}
			newConfigMap.Data[configuration.ResourceQOSConfigKey] = blkIOQOSData
			configMap, err = c.CoreV1().ConfigMaps(koordNamespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
			framework.ExpectNoError(err)
			framework.Logf("update slo-controller-config successfully, data: %+v", configMap.Data)

			ginkgo.By("Check NodeSLO blkio qos config")
			gomega.Eventually(func() bool {
				nodeList, err = e2enode.GetReadySchedulableNodes(c)
				framework.ExpectNoError(err)
				gomega.Expect(len(nodeList.Items)).NotTo(gomega.BeZero())

				for i := range nodeList.Items {
					node := &nodeList.Items[i]
					nodeSLO, err := koordClient.SloV1alpha1().NodeSLOs().Get(context.TODO(), node.Name, metav1.GetOptions{})
					if err != nil {
						framework.Logf("failed to get NodeSLO for node %s, err: %v", node.Name, err)
						return false
					}
					if nodeSLO.Spec.ResourceQOSStrategy == nil || nodeSLO.Spec.ResourceQOSStrategy.BEClass == nil {
						framework.Logf("NodeSLO %s has no BEClass resource qos strategy", node.Name)
						return false
					}
					if !equality.Semantic.DeepEqual(nodeSLO.Spec.ResourceQOSStrategy.BEClass.BlkIOQOS, expectedBlkIOQOS) {
						framework.Logf("NodeSLO %s has unexpected BEClass blkio qos: got %+v, want %+v",
							node.Name, nodeSLO.Spec.ResourceQOSStrategy.BEClass.BlkIOQOS, expectedBlkIOQOS)
						return false
					}
				}

				return true
			}, 120*time.Second, 5*time.Second).Should(gomega.Equal(true))
		})
	})
})

func makeBlkIOQOSConfig() (*slov1alpha1.BlkIOQOSCfg, string) {
	blkIOQOS := &slov1alpha1.BlkIOQOSCfg{
		Enable: ptr.To[bool](true),
		BlkIOQOS: slov1alpha1.BlkIOQOS{
			Blocks: []*slov1alpha1.BlockCfg{
				{
					Name:      "/dev/koordlet-e2e-blkio",
					BlockType: slov1alpha1.BlockTypeDevice,
					IOCfg: slov1alpha1.IOCfg{
						ReadIOPS:        ptr.To[int64](2048),
						WriteIOPS:       ptr.To[int64](1024),
						ReadBPS:         ptr.To[int64](10485760),
						WriteBPS:        ptr.To[int64](5242880),
						IOWeightPercent: ptr.To[int64](60),
					},
				},
			},
		},
	}
	resourceQOSCfg := &configuration.ResourceQOSCfg{
		ClusterStrategy: &slov1alpha1.ResourceQOSStrategy{
			BEClass: &slov1alpha1.ResourceQOS{
				BlkIOQOS: blkIOQOS,
			},
		},
	}
	cfgBytes, err := json.Marshal(resourceQOSCfg)
	framework.ExpectNoError(err)
	return blkIOQOS, string(cfgBytes)
}

func ensureSLOConfigMap(c clientset.Interface, namespace, name string) (*corev1.ConfigMap, bool) {
	configMap, err := c.CoreV1().ConfigMaps(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err == nil {
		framework.Logf("successfully get slo-controller-config %s/%s", namespace, name)
		return configMap, false
	}
	if !errors.IsNotFound(err) {
		framework.Failf("failed to get slo-controller-config %s/%s, got unexpected error: %v", namespace, name, err)
	}

	framework.Logf("cannot get slo-controller-config %s/%s, try to create a new one", namespace, name)
	configMap, err = manifest.ConfigMapFromManifest("slocontroller/slo-controller-config.yaml")
	framework.ExpectNoError(err)
	configMap.SetNamespace(namespace)
	configMap.SetName(name)
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap, err = c.CoreV1().ConfigMaps(namespace).Create(context.TODO(), configMap, metav1.CreateOptions{})
	framework.ExpectNoError(err)
	framework.Logf("create slo-controller-config successfully, data: %+v", configMap.Data)

	return configMap, true
}

func rollbackSLOConfigData(f *framework.Framework, namespace, name, key, value string, hadKey bool) {
	configMap, err := f.ClientSet.CoreV1().ConfigMaps(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	framework.ExpectNoError(err)
	newConfigMap := configMap.DeepCopy()
	if newConfigMap.Data == nil {
		newConfigMap.Data = map[string]string{}
	}
	if hadKey {
		newConfigMap.Data[key] = value
	} else {
		delete(newConfigMap.Data, key)
	}
	newConfigMap, err = f.ClientSet.CoreV1().ConfigMaps(namespace).Update(context.TODO(), newConfigMap, metav1.UpdateOptions{})
	framework.ExpectNoError(err)
	framework.Logf("finish rollback updating slo-controller-config %s/%s, final data: %v", namespace, name, newConfigMap.Data)
}

func rollbackSLOConfigObject(f *framework.Framework, namespace, name string) {
	err := f.ClientSet.CoreV1().ConfigMaps(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
	framework.ExpectNoError(err, fmt.Sprintf("failed to delete slo-controller-config %s/%s", namespace, name))
	framework.Logf("finish deleting slo-controller-config %s/%s", namespace, name)
}
