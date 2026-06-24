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
	"fmt"
	"time"

	topov1alpha1 "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/apis/topology/v1alpha1"
	nrtclientset "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/clientset/versioned"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	koordutil "github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
	"github.com/koordinator-sh/koordinator/test/e2e/framework/skipper"
)

var hugepageResourceNames = []corev1.ResourceName{
	corev1.ResourceName(string(corev1.ResourceHugePagesPrefix) + "2Mi"),
	corev1.ResourceName(string(corev1.ResourceHugePagesPrefix) + "1Gi"),
}

var _ = SIGDescribe("HugePageReport", func() {
	f := framework.NewDefaultFramework("hugepage-report")

	var (
		c         clientset.Interface
		nrtClient nrtclientset.Interface
	)

	ginkgo.BeforeEach(func() {
		c = f.ClientSet

		var err error
		nrtClient, err = nrtclientset.NewForConfig(f.ClientConfig())
		framework.ExpectNoError(err, "unable to create NRT client")
	})

	framework.KoordinatorDescribe("HugePageReport NodeResourceTopology", func() {
		framework.ConformanceIt("report hugepage capacity in NRT zones", func() {
			nodeList, err := e2enode.GetReadySchedulableNodes(c)
			framework.ExpectNoError(err)
			gomega.Expect(nodeList.Items).NotTo(gomega.BeEmpty())

			node, resourceNames := findNodeWithHugepages(nodeList.Items)
			if node == nil {
				skipper.Skipf("no nodes with hugepage capacity > 0; skipping HugePageReport e2e test")
			}

			expected := map[corev1.ResourceName]resource.Quantity{}
			for _, resName := range resourceNames {
				expected[resName] = node.Status.Capacity[resName]
			}

			ginkgo.By(fmt.Sprintf("Using node %s to verify hugepage reporting", node.Name))

			gomega.Eventually(func() error {
				nrt, err := nrtClient.TopologyV1alpha1().NodeResourceTopologies().Get(context.TODO(), node.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if len(nrt.Zones) == 0 {
					return fmt.Errorf("node resource topology has no zones")
				}

				for resName, expectedQty := range expected {
					actual := sumZoneResource(nrt, resName)
					if actual.Cmp(expectedQty) != 0 {
						return fmt.Errorf("resource %s mismatch: expected %s, got %s", resName, expectedQty.String(), actual.String())
					}
				}

				return nil
			}, 2*time.Minute, 5*time.Second).Should(gomega.Succeed())
		})
	})
})

func findNodeWithHugepages(nodes []corev1.Node) (*corev1.Node, []corev1.ResourceName) {
	for i := range nodes {
		node := &nodes[i]
		var resourceNames []corev1.ResourceName
		for _, resName := range hugepageResourceNames {
			quantity, ok := node.Status.Capacity[resName]
			if !ok || quantity.Value() <= 0 {
				continue
			}
			resourceNames = append(resourceNames, resName)
		}
		if len(resourceNames) > 0 {
			return node, resourceNames
		}
	}

	return nil, nil
}

func sumZoneResource(nrt *topov1alpha1.NodeResourceTopology, resourceName corev1.ResourceName) resource.Quantity {
	total := resource.NewQuantity(0, resource.BinarySI)
	for _, zone := range nrt.Zones {
		if zone.Type != koordutil.NodeZoneType {
			continue
		}
		for _, res := range zone.Resources {
			if res.Name == string(resourceName) {
				total.Add(res.Capacity)
			}
		}
	}

	return *total
}
