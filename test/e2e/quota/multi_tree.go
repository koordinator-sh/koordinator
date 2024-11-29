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
	quotav1 "k8s.io/apiserver/pkg/quota/v1"

	schedclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"

	"github.com/koordinator-sh/koordinator/apis/extension"
	quotav1alpha1 "github.com/koordinator-sh/koordinator/apis/quota/v1alpha1"
	schedv1alpha1 "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/test/e2e/framework"
	e2enode "github.com/koordinator-sh/koordinator/test/e2e/framework/node"
	imageutils "github.com/koordinator-sh/koordinator/test/utils/image"
)

var QuotaE2eLabel = "koord-quota-e2e"

var _ = SIGDescribe("multi-quota-tree", func() {
	var nodeList *corev1.NodeList
	var err error
	var quotaClient schedclientset.Interface

	f := framework.NewDefaultFramework("multi-quota-tree")

	ginkgo.BeforeEach(func() {
		framework.Logf("get some nodes which are ready and schedulable")
		nodeList, err = e2enode.GetReadySchedulableNodes(f.ClientSet)
		framework.ExpectNoError(err)

		quotaClient, err = schedclientset.NewForConfig(f.ClientConfig())
		framework.ExpectNoError(err)

		// fail the test when no node available
		gomega.Expect(len(nodeList.Items)).Should(gomega.BeNumerically(">", 1))
	})
	ginkgo.AfterEach(func() {})

	framework.KoordinatorDescribe("multi quota tree", func() {
		framework.ConformanceIt("create two profile and construct two quota tree, check the min and labels", func() {
			ginkgo.By("create quota profile1")
			profile1 := &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-profile1", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel: "true",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: fmt.Sprintf("%s-profile1-root-quota", f.Namespace.Name),
					QuotaLabels: map[string]string{
						extension.LabelQuotaIsParent: "true",
						QuotaE2eLabel:                "true",
					},
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							corev1.LabelHostname: nodeList.Items[0].Name,
						},
					},
				},
			}

			_, err := f.KoordinatorClientSet.QuotaV1alpha1().ElasticQuotaProfiles(profile1.Namespace).Create(context.TODO(), profile1, metav1.CreateOptions{})
			if err == nil {
				framework.Logf("successfully create profile1")
			} else {
				framework.Failf("failed create elasticquota profile1, err: %v", err)
			}

			ginkgo.By("wait for root quota 1 created")
			gomega.Eventually(func() bool {
				_, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), profile1.Spec.QuotaName, metav1.GetOptions{})
				if err == nil {
					return true
				}
				return false
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))
			rootQuota1, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), profile1.Spec.QuotaName, metav1.GetOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("compare node 1 resource and root quota 1 min")
			totalResource1 := corev1.ResourceList{}
			totalResource1[corev1.ResourceCPU] = nodeList.Items[0].Status.Allocatable[corev1.ResourceCPU]
			totalResource1[corev1.ResourceMemory] = nodeList.Items[0].Status.Allocatable[corev1.ResourceMemory]

			gomega.Expect(quotav1.Equals(totalResource1, rootQuota1.Spec.Min)).Should(gomega.Equal(true))
			framework.Logf("root quota min is right")
			framework.Logf("node %v resource: %v, root quota 1 min: %v", nodeList.Items[0].Name, util.DumpJSON(nodeList.Items[0].Status.Allocatable), util.DumpJSON(rootQuota1.Spec.Min))

			gomega.Expect(rootQuota1.Labels[extension.LabelQuotaTreeID]).ShouldNot(gomega.Equal(""))
			gomega.Expect(rootQuota1.Labels[extension.LabelQuotaIsRoot]).Should(gomega.Equal("true"))
			framework.Logf("root quota has tree id: %v, is root: %v", rootQuota1.Labels[extension.LabelQuotaTreeID], rootQuota1.Labels[extension.LabelQuotaIsRoot])

			ginkgo.By("create child quota for root quota 1")
			time.Sleep(2 * time.Second)
			childQuota1 := &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-profile1-child1-quota", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel:              "true",
						extension.LabelQuotaParent: rootQuota1.Name,
					},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Min: corev1.ResourceList{
						corev1.ResourceCPU:    util.MultiplyQuant(rootQuota1.Spec.Min[corev1.ResourceCPU], 0.5),
						corev1.ResourceMemory: util.MultiplyQuant(rootQuota1.Spec.Min[corev1.ResourceMemory], 0.5),
					},
					Max: rootQuota1.Spec.Min,
				},
			}

			retChildQuota1, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota1, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("check child 1 quota tree id")
			gomega.Expect(rootQuota1.Labels[extension.LabelQuotaTreeID]).Should(gomega.Equal(retChildQuota1.Labels[extension.LabelQuotaTreeID]), "parent and child has the same tree id")

			ginkgo.By("check child 1 quota runtime quota")
			gomega.Eventually(func() bool {
				quota, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), childQuota1.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				runtime, err := extension.GetRuntime(quota)
				framework.ExpectNoError(err)
				if quotav1.Equals(runtime, quota.Spec.Min) {
					framework.Logf("child 1 runtime quota is same to min, runtime: %v", util.DumpJSON(runtime))
					return true
				}
				return false
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))

			ginkgo.By("create quota profile2")
			profile2 := &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-profile2", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel: "true",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: fmt.Sprintf("%s-profile2-root-quota", f.Namespace.Name),
					QuotaLabels: map[string]string{
						extension.LabelQuotaIsParent: "true",
						QuotaE2eLabel:                "true",
					},
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							corev1.LabelHostname: nodeList.Items[1].Name,
						},
					},
				},
			}

			_, err = f.KoordinatorClientSet.QuotaV1alpha1().ElasticQuotaProfiles(profile2.Namespace).Create(context.TODO(), profile2, metav1.CreateOptions{})
			if err == nil {
				framework.Logf("successfully create profile2")
			} else {
				framework.Failf("failed create elasticquota profile2, err: %v", err)
			}

			ginkgo.By("wait for root quota 2 created")
			gomega.Eventually(func() bool {
				_, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), profile2.Spec.QuotaName, metav1.GetOptions{})
				if err == nil {
					return true
				}
				return false
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))
			rootQuota2, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), profile2.Spec.QuotaName, metav1.GetOptions{})
			framework.ExpectNoError(err)

			gomega.Expect(rootQuota2.Labels[extension.LabelQuotaTreeID]).ShouldNot(gomega.Equal(""))
			gomega.Expect(rootQuota2.Labels[extension.LabelQuotaIsRoot]).Should(gomega.Equal("true"))
			framework.Logf("root quota 2 has tree id: %v, is-root: %v", rootQuota2.Labels[extension.LabelQuotaTreeID], rootQuota2.Labels[extension.LabelQuotaIsRoot])

			ginkgo.By("compare different tree id for different profile")
			gomega.Expect(rootQuota2.Labels[extension.LabelQuotaTreeID]).ShouldNot(gomega.Equal(rootQuota1.Labels[extension.LabelQuotaTreeID]))
			framework.Logf("root quota 1 tree id: %v, root quota 2 tree id: %v", rootQuota1.Labels[extension.LabelQuotaTreeID], rootQuota2.Labels[extension.LabelQuotaTreeID])

			ginkgo.By("create child quota for root quota 2")
			time.Sleep(2 * time.Second)
			childQuota2 := &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-profile2-child2-quota", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel:              "true",
						extension.LabelQuotaParent: rootQuota2.Name,
					},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Min: corev1.ResourceList{
						corev1.ResourceCPU:    util.MultiplyQuant(rootQuota2.Spec.Min[corev1.ResourceCPU], 0.5),
						corev1.ResourceMemory: util.MultiplyQuant(rootQuota2.Spec.Min[corev1.ResourceMemory], 0.5),
					},
					Max: rootQuota2.Spec.Min,
				},
			}

			retChildQuota2, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota2, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("check child 2 quota tree id")
			gomega.Expect(rootQuota2.Labels[extension.LabelQuotaTreeID]).Should(gomega.Equal(retChildQuota2.Labels[extension.LabelQuotaTreeID]), "parent and child has the same tree id")

			ginkgo.By("check child 2 quota runtime quota")
			gomega.Eventually(func() bool {
				quota, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), childQuota2.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				runtime, err := extension.GetRuntime(quota)
				framework.ExpectNoError(err)
				if quotav1.Equals(runtime, quota.Spec.Min) {
					framework.Logf("child 2 runtime quota is same to min, runtime: %v", util.DumpJSON(runtime))
					return true
				}
				return false
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))

			ginkgo.By("forbidden mv exist quota in different trees")
			retChildQuota2, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), childQuota2.Name, metav1.GetOptions{})
			framework.ExpectNoError(err)
			retChildQuota2.Labels[extension.LabelQuotaParent] = rootQuota1.Name
			_, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Update(context.TODO(), retChildQuota2, metav1.UpdateOptions{})
			framework.ExpectError(err)
			framework.Logf("mv childQuota2 failed, err: %v", err)

			ginkgo.By("create child quota for root quota 2 with different tree id, reject")
			childQuota3 := &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-profile2-child3-quota", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel:              "true",
						extension.LabelQuotaParent: rootQuota2.Name,
						extension.LabelQuotaTreeID: "dfafda",
					},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Min: corev1.ResourceList{
						corev1.ResourceCPU:    util.MultiplyQuant(rootQuota2.Spec.Min[corev1.ResourceCPU], 0.25),
						corev1.ResourceMemory: util.MultiplyQuant(rootQuota2.Spec.Min[corev1.ResourceMemory], 0.25),
					},
					Max: rootQuota2.Spec.Min,
				},
			}

			_, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota3, metav1.CreateOptions{})
			framework.ExpectError(err)
			framework.Logf("create childQuota3 failed, err: %v", err)

			ginkgo.By("clean profile/quotas")
			err = f.KoordinatorClientSet.QuotaV1alpha1().ElasticQuotaProfiles(f.Namespace.Name).Delete(context.TODO(), profile1.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = f.KoordinatorClientSet.QuotaV1alpha1().ElasticQuotaProfiles(f.Namespace.Name).Delete(context.TODO(), profile2.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), childQuota1.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), childQuota2.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), profile1.Spec.QuotaName, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), profile2.Spec.QuotaName, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
		})

		framework.ConformanceIt("create profile with resource ratio", func() {
			ginkgo.By("create quota profile with resource ratio")
			resourceRatio := "0.5"
			profile := &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-profile-with-resource-ratio", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel: "true",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: fmt.Sprintf("%s-profile-with-resource-ratio-root-quota", f.Namespace.Name),
					QuotaLabels: map[string]string{
						extension.LabelQuotaIsParent: "true",
						QuotaE2eLabel:                "true",
					},
					ResourceRatio: &resourceRatio,
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							corev1.LabelHostname: nodeList.Items[0].Name,
						},
					},
				},
			}

			_, err := f.KoordinatorClientSet.QuotaV1alpha1().ElasticQuotaProfiles(profile.Namespace).Create(context.TODO(), profile, metav1.CreateOptions{})
			if err == nil {
				framework.Logf("successfully create profile")
			} else {
				framework.Failf("failed create elasticquota profile, err: %v", err)
			}

			ginkgo.By("wait for root quota  created")
			gomega.Eventually(func() bool {
				_, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), profile.Spec.QuotaName, metav1.GetOptions{})
				if err == nil {
					return true
				}
				return false
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))
			rootQuota, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), profile.Spec.QuotaName, metav1.GetOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("compare node resource and root quota min")
			totalResource := corev1.ResourceList{}
			totalResource[corev1.ResourceCPU] = util.MultiplyQuant(nodeList.Items[0].Status.Allocatable[corev1.ResourceCPU], 0.5)
			totalResource[corev1.ResourceMemory] = util.MultiplyQuant(nodeList.Items[0].Status.Allocatable[corev1.ResourceMemory], 0.5)

			gomega.Expect(quotav1.Equals(totalResource, rootQuota.Spec.Min)).Should(gomega.Equal(true))
			framework.Logf("root quota  min is right")
			framework.Logf("node %v resource: %v, root quota min: %v", nodeList.Items[0].Name, util.DumpJSON(nodeList.Items[0].Status.Allocatable), util.DumpJSON(rootQuota.Spec.Min))

			ginkgo.By("clean profile/quotas")
			err = f.KoordinatorClientSet.QuotaV1alpha1().ElasticQuotaProfiles(f.Namespace.Name).Delete(context.TODO(), profile.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), profile.Spec.QuotaName, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
		})

		framework.ConformanceIt("root quota show resource usage", func() {
			ginkgo.By("create quota profile")
			profile := &quotav1alpha1.ElasticQuotaProfile{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-profile3", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel: "true",
					},
				},
				Spec: quotav1alpha1.ElasticQuotaProfileSpec{
					QuotaName: fmt.Sprintf("%s-profile3-root-quota", f.Namespace.Name),
					QuotaLabels: map[string]string{
						extension.LabelQuotaIsParent: "true",
						QuotaE2eLabel:                "true",
					},
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							corev1.LabelHostname: nodeList.Items[0].Name,
						},
					},
				},
			}

			_, err := f.KoordinatorClientSet.QuotaV1alpha1().ElasticQuotaProfiles(profile.Namespace).Create(context.TODO(), profile, metav1.CreateOptions{})
			if err == nil {
				framework.Logf("successfully create profile")
			} else {
				framework.Failf("failed create elasticquota profile, err: %v", err)
			}

			ginkgo.By("wait for root quota  created")
			gomega.Eventually(func() bool {
				_, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), profile.Spec.QuotaName, metav1.GetOptions{})
				if err == nil {
					return true
				}
				return false
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))

			rootQuota, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), profile.Spec.QuotaName, metav1.GetOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("create child quota")
			time.Sleep(2 * time.Second)
			childQuota := &schedv1alpha1.ElasticQuota{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: f.Namespace.Name,
					Name:      fmt.Sprintf("%s-profile3-child-quota", f.Namespace.Name),
					Labels: map[string]string{
						QuotaE2eLabel:              "true",
						extension.LabelQuotaParent: rootQuota.Name,
					},
				},
				Spec: schedv1alpha1.ElasticQuotaSpec{
					Min: corev1.ResourceList{
						corev1.ResourceCPU:    util.MultiplyQuant(rootQuota.Spec.Min[corev1.ResourceCPU], 0.5),
						corev1.ResourceMemory: util.MultiplyQuant(rootQuota.Spec.Min[corev1.ResourceMemory], 0.5),
					},
					Max: rootQuota.Spec.Min,
				},
			}

			_, err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Create(context.TODO(), childQuota, metav1.CreateOptions{})
			framework.ExpectNoError(err)

			ginkgo.By("check root quota allocated, is equal child quota min")
			gomega.Eventually(func() bool {
				quota, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), rootQuota.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)

				allocated, err := extension.GetAllocated(quota)
				framework.ExpectNoError(err)

				if quotav1.Equals(allocated, childQuota.Spec.Min) {
					framework.Logf("root quota allocated %v, equal child min", util.DumpJSON(allocated))
					return true
				}

				return false
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))

			podRequests := corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			}

			ginkgo.By("create pod with child quota")
			pod := createQuotaE2EPod(f.Namespace.Name, fmt.Sprintf("%s-quota-test-pod-1", f.Namespace.Name), childQuota.Name, podRequests)

			f.PodClient().Create(pod)
			gomega.Eventually(func() bool {
				pod, err := f.ClientSet.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				if pod.Spec.NodeName != "" {
					return true
				}
				return false
			})

			ginkgo.By("check child quota allocated, is equal pod requests")
			gomega.Eventually(func() bool {
				quota, err := quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Get(context.TODO(), childQuota.Name, metav1.GetOptions{})
				framework.ExpectNoError(err)
				allocated, err := extension.GetAllocated(quota)
				framework.ExpectNoError(err)

				if quotav1.Equals(allocated, podRequests) {
					framework.Logf("child quota allocated %v, equal pod requests", util.DumpJSON(allocated))
					return true
				}

				return false
			}, 60*time.Second, 1*time.Second).Should(gomega.Equal(true))

			ginkgo.By("clean pod/profiles/quotas")
			f.PodClient().DeleteSync(pod.Name, metav1.DeleteOptions{}, 5*time.Minute)
			err = f.KoordinatorClientSet.QuotaV1alpha1().ElasticQuotaProfiles(f.Namespace.Name).Delete(context.TODO(), profile.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), childQuota.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
			err = quotaClient.SchedulingV1alpha1().ElasticQuotas(f.Namespace.Name).Delete(context.TODO(), profile.Spec.QuotaName, metav1.DeleteOptions{})
			framework.ExpectNoError(err)
		})
	})

})

func createQuotaE2EPod(namespace, name, quotaName string, requests corev1.ResourceList) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				QuotaE2eLabel:            "true",
				extension.LabelQuotaName: quotaName,
			},
			Annotations: map[string]string{},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "quota-e2e-testa",
					Image: imageutils.GetPauseImageName(),
					Resources: corev1.ResourceRequirements{
						Requests: requests,
					},
				},
			},
			// tolerate all nodes.
			Tolerations: []corev1.Toleration{
				corev1.Toleration{
					Operator: corev1.TolerationOpExists,
				},
			},
		},
	}

	return pod
}
