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
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	apiv1 "k8s.io/kubernetes/pkg/api/v1/pod"

	schedclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
	e2epod "github.com/koordinator-sh/koordinator/test/e2e/framework/pod"
)

var _ = SIGDescribe("quota-guaranteed", func() {
	var nodeList *corev1.NodeList
	var err error
	var quotaClient schedclientset.Interface
	var totalResource = corev1.ResourceList{}

	f := framework.NewDefaultFramework("quota-guaranteed")

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

	framework.KoordinatorDescribe("quota-guaranteed", func() {
		framework.ConformanceIt("quota guaranteed", func() {
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

			ginkgo.By("create child quota 1, min is zero")

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
					Max: totalResource,
				},
			}

			_, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota1, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			podRequests := corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}

			ginkgo.By("create child quota 2, min is totalRequests-podRequests")

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
					Min: quotav1.Subtract(totalResource, podRequests),
					Max: totalResource,
				},
			}

			_, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota2, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("create pod1 with child quota 1, success")
			pod1 := createQuotaE2EPod(f.Namespace.Name, "basic-pod-1", childQuota1.Name, podRequests)
			_, err = f.ClientSet.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod1, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			err = e2epod.WaitTimeoutForPodReadyInNamespace(f.ClientSet, pod1.Name, pod1.Namespace, framework.PodStartTimeout)
			framework.ExpectNoError(err)

			ginkgo.By("check quota runtime and guaranteed")

			quota, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), childQuota1.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)

			guaranteed, _ := extension.GetGuaranteed(quota)
			runtime, _ := extension.GetRuntime(quota)
			framework.Logf("childQuota1 runtime: %v, guaranteed: %v", util.DumpJSON(guaranteed), util.DumpJSON(runtime))
			gomega.Expect(quotav1.Equals(guaranteed, podRequests)).Should(gomega.Equal(true))
			gomega.Expect(quotav1.Equals(runtime, podRequests)).Should(gomega.Equal(true))

			quota, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), childQuota2.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)

			guaranteed, _ = extension.GetGuaranteed(quota)
			runtime, _ = extension.GetRuntime(quota)
			framework.Logf("childQuota2 runtime: %v, guaranteed: %v", util.DumpJSON(guaranteed), util.DumpJSON(runtime))
			gomega.Expect(quotav1.Equals(guaranteed, childQuota2.Spec.Min)).Should(gomega.Equal(true))
			gomega.Expect(quotav1.Equals(runtime, childQuota2.Spec.Min)).Should(gomega.Equal(true))

			quota, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), parentQuota.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)

			guaranteed, _ = extension.GetGuaranteed(quota)
			runtime, _ = extension.GetRuntime(quota)
			framework.Logf("parentQuota runtime: %v, guaranteed: %v", util.DumpJSON(guaranteed), util.DumpJSON(runtime))
			gomega.Expect(quotav1.Equals(guaranteed, parentQuota.Spec.Min)).Should(gomega.Equal(true))
			gomega.Expect(quotav1.Equals(runtime, parentQuota.Spec.Min)).Should(gomega.Equal(true))

			ginkgo.By("create pod2 with child quota 1, failed")
			pod2 := createQuotaE2EPod(f.Namespace.Name, "basic-pod-2", childQuota1.Name, podRequests)
			_, err = f.ClientSet.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod2, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			time.Sleep(2 * time.Second)
			pod, err := f.ClientSet.CoreV1().Pods(f.Namespace.Name).Get(context.TODO(), pod2.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			gomega.Expect(pod.Spec.NodeName).Should(gomega.Equal(""))
			_, condition := apiv1.GetPodCondition(&pod.Status, corev1.PodScheduled)
			gomega.Expect(condition).ShouldNot(gomega.Equal(nil))
			gomega.Expect(condition.Status).Should(gomega.Equal(corev1.ConditionFalse))
			gomega.Expect(strings.Contains(condition.Message, "exceedDimensions")).Should(gomega.Equal(true))
			framework.Logf("pod %v/%v failed schedule, condition: %v", pod.Namespace, pod.Name, util.DumpJSON(condition))

			ginkgo.By("create pod3 with child quota 2, success")
			pod3 := createQuotaE2EPod(f.Namespace.Name, "basic-pod-3", childQuota2.Name, podRequests)
			_, err = f.ClientSet.CoreV1().Pods(f.Namespace.Name).Create(context.TODO(), pod3, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			time.Sleep(2 * time.Second)
			pod, err = f.ClientSet.CoreV1().Pods(f.Namespace.Name).Get(context.TODO(), pod3.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			gomega.Expect(pod.Spec.NodeName).ShouldNot(gomega.Equal(""))
			framework.Logf("pod %v/%v scheduled to node %v", pod.Namespace, pod.Name, pod.Spec.NodeName)

			ginkgo.By("clean pods and elasticquotas")
			f.PodClient().DeleteSync(pod1.Name, metav1.DeleteOptions{}, 5*time.Minute)
			f.PodClient().DeleteSync(pod2.Name, metav1.DeleteOptions{}, 5*time.Minute)
			f.PodClient().DeleteSync(pod3.Name, metav1.DeleteOptions{}, 5*time.Minute)

			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), childQuota1.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), childQuota2.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), parentQuota.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
		})
	})
})
