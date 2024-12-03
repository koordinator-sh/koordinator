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

package quota

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	schedclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("basic-quota", func() {
	var nodeList *corev1.NodeList
	var err error
	var quotaClient schedclientset.Interface
	var totalResource = corev1.ResourceList{}

	f := framework.NewDefaultFramework("basic-quota")

	ginkgo.BeforeEach(func() {
		framework.Logf("get some nodes which are ready and schedulable")
		nodeList, err = e2enode.GetReadySchedulableNodes(f.ClientSet)
		framework.ExpectNoError(err)

		quotaClient, err = schedclientset.NewForConfig(f.ClientConfig())
		framework.ExpectNoError(err)

		// fail the test when no node available
		gomega.Expect(len(nodeList.Items)).Should(gomega.BeNumerically(">", 0))

		totalResource[corev1.ResourceCPU] = nodeList.Items[0].Status.Allocatable[corev1.ResourceCPU]
		totalResource[corev1.ResourceMemory] = nodeList.Items[0].Status.Allocatable[corev1.ResourceMemory]

		cpu := totalResource[corev1.ResourceCPU]
		memory := totalResource[corev1.ResourceMemory]
		gomega.Expect(cpu.Value()).Should(gomega.BeNumerically(">=", 2))
		gomega.Expect(memory.Value()).Should(gomega.BeNumerically(">=", 4*1024*1024*1024))
	})
	ginkgo.AfterEach(func() {})

	framework.KoordinatorDescribe("basic-quota", func() {
		framework.ConformanceIt("the sum of child min is smaller than parent min", func() {
			ginkgo.By("create parent quota")
			parentQuota := &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-parent-quota", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel:                "true",
						extension.LabelQuotaIsParent: "true",
					},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Min: totalResource,
					Max: totalResource,
				},
			}

			_, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), parentQuota, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("create child quota 1, min is the 0.5*parentQuota.Min")

			childQuota1 := &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-child-quota-1", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel:              "true",
						extension.LabelQuotaParent: parentQuota.Name,
					},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Min: corev1.ResourceList{
						corev1.ResourceCPU:    util.MultiplyQuant(totalResource[corev1.ResourceCPU], 0.5),
						corev1.ResourceMemory: util.MultiplyQuant(totalResource[corev1.ResourceMemory], 0.5),
					},
					Max: totalResource,
				},
			}

			_, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota1, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("create child quota 2, min is the 0.6*parentQuota.Min, failed")
			childQuota2 := &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-child-quota-2", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel:              "true",
						extension.LabelQuotaParent: parentQuota.Name,
					},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Min: corev1.ResourceList{
						corev1.ResourceCPU:    util.MultiplyQuant(totalResource[corev1.ResourceCPU], 0.6),
						corev1.ResourceMemory: util.MultiplyQuant(totalResource[corev1.ResourceMemory], 0.6),
					},
					Max: totalResource,
				},
			}

			_, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota2, metav1.CreateOptions{})
			framework.ExpectError(err)
			framework.Logf("failed create childQuota2, err: %v", err)

			ginkgo.By("create child quota 2, min is the 0.5*parentQuota.Min, success")
			childQuota2.Spec.Min = corev1.ResourceList{
				corev1.ResourceCPU:    util.MultiplyQuant(totalResource[corev1.ResourceCPU], 0.5),
				corev1.ResourceMemory: util.MultiplyQuant(totalResource[corev1.ResourceMemory], 0.5),
			}

			_, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota2, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), childQuota1.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), childQuota2.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), parentQuota.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
		})

		framework.ConformanceIt("check the quota max", func() {

			requests := corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}

			ginkgo.By("create basic quota")
			basicQuota := &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-basic-quota", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel: "true",
					},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Max: requests,
				},
			}

			_, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), basicQuota, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			pod1 := createQuotaE2EPod(f.Namespace.Name, "basic-pod-1", basicQuota.Name, requests)
			_, err = f.ClientSet.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod1, metav1.CreateOptions{})
			framework.ExpectNoError(err)
			err = e2epod.WaitTimeoutForPodReadyInNamespace(f.ClientSet, pod1.Name, pod1.Namespace, framework.PodStartTimeout)
			framework.ExpectNoError(err)

			pod2 := createQuotaE2EPod(f.Namespace.Name, "basic-pod-2", basicQuota.Name, requests)
			_, err = f.ClientSet.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod1, metav1.CreateOptions{})
			framework.ExpectError(err)
			framework.Logf("create pod %v failed, err: %v", pod2.Name, err)

			ginkgo.By("clean pods and elasticquotas")
			f.PodClient().DeleteSync(pod1.Name, metav1.DeleteOptions{}, 5*time.Minute)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), basicQuota.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
		})
	})
})
