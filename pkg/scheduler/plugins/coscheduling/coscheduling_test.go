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

package coscheduling

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/events"
	"k8s.io/kube-scheduler/config/v1beta3"
	"k8s.io/kubernetes/pkg/scheduler"
	configtesting "k8s.io/kubernetes/pkg/scheduler/apis/config/testing"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/helper"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/core"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	scheduledconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	st "k8s.io/kubernetes/pkg/scheduler/testing"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	pgclientset "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"
	fakepgclientset "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned/fake"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta2"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
)

// gang test used
type PodGroupClientSetAndHandle struct {
	frameworkext.ExtendedHandle
	pgclientset.Interface

	koordInformerFactory koordinatorinformers.SharedInformerFactory
}

func (h *PodGroupClientSetAndHandle) KoordinatorSharedInformerFactory() koordinatorinformers.SharedInformerFactory {
	return h.koordInformerFactory
}

func GangPluginFactoryProxy(clientSet pgclientset.Interface, factoryFn frameworkruntime.PluginFactory, plugin *framework.Plugin) frameworkruntime.PluginFactory {
	return func(args apiruntime.Object, handle framework.Handle) (framework.Plugin, error) {
		koordClient := koordfake.NewSimpleClientset()
		koordInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClient, 0)
		extenderFactory, err := frameworkext.NewFrameworkExtenderFactory(
			frameworkext.WithKoordinatorClientSet(koordClient),
			frameworkext.WithKoordinatorSharedInformerFactory(koordInformerFactory))
		if err != nil {
			return nil, err
		}
		extender := extenderFactory.NewFrameworkExtender(handle.(framework.Framework))
		*plugin, err = factoryFn(args, &PodGroupClientSetAndHandle{
			ExtendedHandle:       extender,
			Interface:            clientSet,
			koordInformerFactory: koordInformerFactory,
		})
		return *plugin, err
	}
}

var _ framework.SharedLister = &testSharedLister{}

type testSharedLister struct {
	nodes       []*corev1.Node
	nodeInfos   []*framework.NodeInfo
	nodeInfoMap map[string]*framework.NodeInfo
}

func newTestSharedLister(pods []*corev1.Pod, nodes []*corev1.Node) *testSharedLister {
	nodeInfoMap := make(map[string]*framework.NodeInfo)
	nodeInfos := make([]*framework.NodeInfo, 0)
	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		if _, ok := nodeInfoMap[nodeName]; !ok {
			nodeInfoMap[nodeName] = framework.NewNodeInfo()
		}
		nodeInfoMap[nodeName].AddPod(pod)
	}
	for _, node := range nodes {
		if _, ok := nodeInfoMap[node.Name]; !ok {
			nodeInfoMap[node.Name] = framework.NewNodeInfo()
		}
		nodeInfoMap[node.Name].SetNode(node)
	}

	for _, v := range nodeInfoMap {
		nodeInfos = append(nodeInfos, v)
	}

	return &testSharedLister{
		nodes:       nodes,
		nodeInfos:   nodeInfos,
		nodeInfoMap: nodeInfoMap,
	}
}

func makePg(name, namespace string, min int32, creationTime *time.Time, minResource *corev1.ResourceList) *v1alpha1.PodGroup {
	var ti int32 = 10
	pg := &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       v1alpha1.PodGroupSpec{MinMember: min, ScheduleTimeoutSeconds: &ti},
	}
	if creationTime != nil {
		pg.CreationTimestamp = metav1.Time{Time: *creationTime}
	}
	if minResource != nil {
		pg.Spec.MinResources = minResource
	}
	return pg
}

func (f *testSharedLister) NodeInfos() framework.NodeInfoLister {
	return f
}

func (f *testSharedLister) List() ([]*framework.NodeInfo, error) {
	return f.nodeInfos, nil
}

func (f *testSharedLister) HavePodsWithAffinityList() ([]*framework.NodeInfo, error) {
	return nil, nil
}

func (f *testSharedLister) HavePodsWithRequiredAntiAffinityList() ([]*framework.NodeInfo, error) {
	return nil, nil
}

func (f *testSharedLister) Get(nodeName string) (*framework.NodeInfo, error) {
	return f.nodeInfoMap[nodeName], nil
}

type pluginTestSuit struct {
	framework.Handle
	proxyNew           runtime.PluginFactory
	gangSchedulingArgs *config.CoschedulingArgs
	plugin             framework.Plugin
}

func newPluginTestSuit(t *testing.T, nodes []*corev1.Node, pgClientSet pgclientset.Interface, cs kubernetes.Interface) *pluginTestSuit {
	var v1beta2args v1beta2.CoschedulingArgs
	v1beta2.SetDefaults_CoschedulingArgs(&v1beta2args)
	var gangSchedulingArgs config.CoschedulingArgs
	err := v1beta2.Convert_v1beta2_CoschedulingArgs_To_config_CoschedulingArgs(&v1beta2args, &gangSchedulingArgs, nil)
	assert.NoError(t, err)

	gangSchedulingPluginConfig := scheduledconfig.PluginConfig{
		Name: "Coscheduling",
		Args: &gangSchedulingArgs,
	}

	var plugin framework.Plugin
	proxyNew := GangPluginFactoryProxy(pgClientSet, New, &plugin)

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		func(reg *runtime.Registry, profile *scheduledconfig.KubeSchedulerProfile) {
			profile.PluginConfig = []scheduledconfig.PluginConfig{
				gangSchedulingPluginConfig,
			}
		},
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(Name, proxyNew),
		schedulertesting.RegisterPreFilterPlugin(Name, proxyNew),
		schedulertesting.RegisterReservePlugin(Name, proxyNew),
		schedulertesting.RegisterPermitPlugin(Name, proxyNew),
		schedulertesting.RegisterPluginAsExtensions(Name, proxyNew, "PostBind"),
	}

	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	informerFactory = helper.NewForceSyncSharedInformerFactory(informerFactory)
	snapshot := newTestSharedLister(nil, nodes)
	fh, err := schedulertesting.NewFramework(
		registeredPlugins,
		"koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
	)
	assert.Nil(t, err)
	return &pluginTestSuit{
		Handle:             fh,
		proxyNew:           proxyNew,
		gangSchedulingArgs: &gangSchedulingArgs,
		plugin:             plugin,
	}
}

func (p *pluginTestSuit) start() {
	ctx := context.TODO()
	p.Handle.SharedInformerFactory().Start(ctx.Done())
	p.Handle.SharedInformerFactory().WaitForCacheSync(ctx.Done())
}

func TestLess(t *testing.T) {
	pgClientSet := fakepgclientset.NewSimpleClientset()
	cs := kubefake.NewSimpleClientset()
	suit := newPluginTestSuit(t, nil, pgClientSet, cs)
	gp := suit.plugin.(*Coscheduling)

	var lowPriority, highPriority = int32(10), int32(100)
	// koordinator priority announced in pod's Labels
	var lowSubPriority, highSubPriority = "111", "222"
	var gangA_ns, gangB_ns = "namespace1", "namespace2"
	gangC_ns := "namespace3"
	gangGroupNS := "namespace4"
	now := time.Now()
	earltTime := now.Add(1 * time.Second)
	lateTime := now.Add(3 * time.Second)

	// we assume that there are tow gang: gangA and gangB
	// gangA is announced by the pod's annotation,gangB is created by the podGroup
	// so here we need to add two gangs to the cluster

	// GangA by Annotations
	podToCreateGangA := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: gangA_ns,
			Name:      "pod1",
			Annotations: map[string]string{
				extension.AnnotationGangName:   "gangA",
				extension.AnnotationGangMinNum: "2",
			},
		},
	}
	podToSatisfyGangC := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: gangC_ns,
			Name:      "podC",
			Labels: map[string]string{
				"pod-group.scheduling.sigs.k8s.io/name": "gangC",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "fake-node",
		},
	}
	// GangB by PodGroup
	gangBCreatTime := now.Add(5 * time.Second)
	pg := makePg("gangB", gangB_ns, 2, &gangBCreatTime, nil)
	// GangC by PodGroup
	gangCCreatTime := now.Add(5 * time.Second)
	pg2 := makePg("gangC", gangC_ns, 1, &gangCCreatTime, nil)
	// GangD by PodGroup
	pg3 := makePg("gangD", gangC_ns, 1, &gangCCreatTime, nil)
	gangGroup := []string{"default/gangD", "default/gangE"}
	rawGangGroup, err := json.Marshal(gangGroup)
	assert.NoError(t, err)
	pg4 := makePg("gang4", gangGroupNS, 0, nil, nil)
	pg5 := makePg("gang5", gangGroupNS, 0, nil, nil)
	pg4.Annotations = map[string]string{extension.AnnotationGangGroups: string(rawGangGroup)}
	suit.start()
	// create gangA and gangB

	err = retry.OnError(
		retry.DefaultRetry,
		errors.IsTooManyRequests,
		func() error {
			var err error
			_, err = suit.Handle.ClientSet().CoreV1().Pods(gangA_ns).Create(context.TODO(), podToCreateGangA, metav1.CreateOptions{})
			return err
		})
	if err != nil {
		t.Errorf("retry podClient create pod err: %v", err)
	}
	err = retry.OnError(
		retry.DefaultRetry,
		errors.IsTooManyRequests,
		func() error {
			var err error
			_, err = pgClientSet.SchedulingV1alpha1().PodGroups(gangB_ns).Create(context.TODO(), pg, metav1.CreateOptions{})
			return err
		})
	if err != nil {
		t.Errorf("retry pgClient create pg err: %v", err)
	}
	err = retry.OnError(
		retry.DefaultRetry,
		errors.IsTooManyRequests,
		func() error {
			var err error
			_, err = pgClientSet.SchedulingV1alpha1().PodGroups(gangC_ns).Create(context.TODO(), pg2, metav1.CreateOptions{})
			return err
		})
	if err != nil {
		t.Errorf("retry pgClient create pg err: %v", err)
	}
	err = retry.OnError(
		retry.DefaultRetry,
		errors.IsTooManyRequests,
		func() error {
			var err error
			_, err = pgClientSet.SchedulingV1alpha1().PodGroups(gangC_ns).Create(context.TODO(), pg3, metav1.CreateOptions{})
			return err
		})
	if err != nil {
		t.Errorf("retry pgClient create pg err: %v", err)
	}
	err = retry.OnError(
		retry.DefaultRetry,
		errors.IsTooManyRequests,
		func() error {
			var err error
			_, err = pgClientSet.SchedulingV1alpha1().PodGroups(gangGroupNS).Create(context.TODO(), pg4, metav1.CreateOptions{})
			return err
		})
	if err != nil {
		t.Errorf("retry pgClient create pg err: %v", err)
	}
	err = retry.OnError(
		retry.DefaultRetry,
		errors.IsTooManyRequests,
		func() error {
			var err error
			_, err = pgClientSet.SchedulingV1alpha1().PodGroups(gangGroupNS).Create(context.TODO(), pg5, metav1.CreateOptions{})
			return err
		})
	if err != nil {
		t.Errorf("retry pgClient create pg err: %v", err)
	}
	err = retry.OnError(
		retry.DefaultRetry,
		errors.IsTooManyRequests,
		func() error {
			var err error
			_, err = suit.Handle.ClientSet().CoreV1().Pods(gangC_ns).Create(context.TODO(), podToSatisfyGangC, metav1.CreateOptions{})
			return err
		})
	if err != nil {
		t.Errorf("retry podClient create pod err: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	for _, tt := range []struct {
		name                string
		p1                  *framework.QueuedPodInfo
		p2                  *framework.QueuedPodInfo
		childScheduleCycle1 int
		childScheduleCycle2 int
		annotations         map[string]string
		expected            bool
	}{
		{
			name: "p1.priority less than p2.priority,but p1's subPriority is greater than p2's",
			p1: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(lowPriority).Label(extension.LabelPodPriority, highSubPriority).Obj()),
			},
			p2: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
			},
			expected: false, // p2 should be ahead of p1 in the queue
		},
		{
			name: "p1.priority greater than p2.priority, p2's subPriority is greater than p1's",
			p1: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
			},
			p2: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(lowPriority).Label(extension.LabelPodPriority, highSubPriority).Obj()),
			},
			expected: true, // p1 should be ahead of p2 in the queue
		},
		{
			name: "equal priority. p1's subPriority is less than p2's subPriority",
			p1: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
			},
			p2: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(extension.LabelPodPriority, highSubPriority).Obj()),
			},
			expected: false, // p2 should be ahead of p1 in the queue
		},
		{
			name: "equal priority. p1's subPriority is greater than p2's subPriority",
			p1: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(highPriority).Label(extension.LabelPodPriority, highSubPriority).Obj()),
			},
			p2: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
			},
			expected: true, // p1 should be ahead of p2 in the queue
		},
		{
			name: "equal priority. p1's subPriority is illegal , p2's subPriority is greater than 0",
			p1: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(highPriority).Label(extension.LabelPodPriority, "????").Obj()),
			},
			p2: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
			},
			expected: false, // p2 should be ahead of p1 in the queue
		},
		{
			name: "equal priority, but p1 is added to schedulingQ earlier than p2",
			p1: &framework.QueuedPodInfo{
				PodInfo:   framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
				Timestamp: earltTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:   framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
				Timestamp: lateTime,
			},
			expected: true, // p1 should be ahead of p2 in the queue
		},
		{
			name: "p1.priority less than p2.priority, p1 belongs to gangB",
			p1: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod1").Priority(lowPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
			},
			p2: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod2").Priority(highPriority).Obj()),
			},
			expected: false, // p2 should be ahead of p1 in the queue
		},
		{
			name: "equal priority, p1 is added to schedulingQ earlier than p2",
			p1: &framework.QueuedPodInfo{
				PodInfo:   framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod1").Priority(highPriority).Obj()),
				Timestamp: earltTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:   framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod2").Priority(highPriority).Obj()),
				Timestamp: lateTime,
			},
			expected: true, // p1 should be ahead of p2 in the queue
		},
		{
			name: "p1.priority less than p2.priority, p1 belongs to gangA and p2 belongs to gangB",
			p1: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(lowPriority).Obj()),
			},
			annotations: map[string]string{extension.AnnotationGangName: "gangA"},
			p2: &framework.QueuedPodInfo{
				PodInfo: framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
			},
			expected: false, // p1 should be ahead of p2 in the queue
		},
		{
			name: "equal priority and creation time, both belongs to gangB, earlier lastScheduleTime pod take precedence",
			p1: &framework.QueuedPodInfo{
				PodInfo:   framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod1").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				Timestamp: lateTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:   framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				Timestamp: earltTime,
			},
			expected: false,
		},
		{
			name: "equal priority and creation time, both belongs to gangB, childScheduleCycle not equal",
			p1: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod1").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				InitialAttemptTimestamp: lateTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				InitialAttemptTimestamp: earltTime,
			},
			childScheduleCycle1: 2,
			childScheduleCycle2: 1,
			expected:            false, // p1 should be ahead of p2 in the queue
		},
		{
			name: "equal priority, p1 belongs to different gangs of one gangGroup, sort by gangID",
			p1: &framework.QueuedPodInfo{
				PodInfo:   framework.NewPodInfo(st.MakePod().Namespace(gangGroupNS).Name("pod1").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gang4").Obj()),
				Timestamp: earltTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:   framework.NewPodInfo(st.MakePod().Namespace(gangGroupNS).Name("pod2").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gang5").Obj()),
				Timestamp: earltTime,
			},
			expected: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {

			if len(tt.annotations) != 0 {
				tt.p1.Pod.Annotations = tt.annotations
			}

			gang1 := gp.pgMgr.(*core.PodGroupManager).GetGangByPod(tt.p1.Pod)
			gang2 := gp.pgMgr.(*core.PodGroupManager).GetGangByPod(tt.p2.Pod)
			if gang1 != nil {
				gang1.ChildrenScheduleRoundMap[util.GetId(tt.p1.Pod.Namespace, tt.p1.Pod.Name)] = tt.childScheduleCycle1
			}
			if gang2 != nil {
				gang2.ChildrenScheduleRoundMap[util.GetId(tt.p2.Pod.Namespace, tt.p2.Pod.Name)] = tt.childScheduleCycle2
			}

			if got := gp.Less(tt.p1, tt.p2); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}

}

func TestPostFilter(t *testing.T) {
	gangCreatedTime := time.Now()
	tests := []struct {
		name              string
		pod               *corev1.Pod
		pg                *v1alpha1.PodGroup
		pods              []*corev1.Pod
		expectedEmptyMsg  bool
		resourceSatisfied bool
		annotations       map[string]string
	}{
		{
			name:             "pod does not belong to any pod group",
			pod:              st.MakePod().Name("pod1").Namespace("gangA_ns").UID("pod1").Obj(),
			expectedEmptyMsg: true,
		},
		{
			name:             "pod belongs to non-existing gang",
			pod:              st.MakePod().Name("pod1").Namespace("gangA_ns").UID("pod1").Label(v1alpha1.PodGroupLabel, "wenshiqi").Obj(),
			expectedEmptyMsg: false,
		},
		{
			name:              "gangA is resourceSatisfied,do not reject the gang",
			pod:               st.MakePod().Name("pod1").Namespace("gangC_ns").UID("pod1").Label(v1alpha1.PodGroupLabel, "gangC").Obj(),
			pg:                makePg("gangC", "gangC_ns", 1, &gangCreatedTime, nil),
			expectedEmptyMsg:  true,
			resourceSatisfied: true,
		},
		{
			name:              "gangA is resourceSatisfied but matchPolicy is only-waiting, reject the gang",
			pod:               st.MakePod().Name("pod1").Namespace("gangC_ns").UID("pod1").Label(v1alpha1.PodGroupLabel, "gangC").Obj(),
			pg:                makePg("gangC", "gangC_ns", 1, &gangCreatedTime, nil),
			expectedEmptyMsg:  false,
			resourceSatisfied: true,
			annotations:       map[string]string{extension.AnnotationGangMatchPolicy: extension.GangMatchPolicyOnlyWaiting},
		},
		{
			name:             "resource not Satisfied,but gangB is NonStrictMode,do not reject the gang",
			pod:              st.MakePod().Name("pod2").Namespace("gangB_ns").UID("pod2").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			pg:               makePg("gangB", "gangB_ns", 4, &gangCreatedTime, nil),
			expectedEmptyMsg: true,
			annotations:      map[string]string{extension.AnnotationGangMode: extension.GangModeNonStrict},
		},

		{
			name: "resource not Satisfied,StrictMode,reject all pods belong to gangA",
			pod:  st.MakePod().Name("pod3").Namespace("gangA_ns").UID("pod3").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").Namespace("gangA_ns").UID("pod3-1").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod3-2").Namespace("gangA_ns").UID("pod3-2").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			pg:               makePg("gangA", "gangA_ns", 4, &gangCreatedTime, nil),
			expectedEmptyMsg: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgClientSet := fakepgclientset.NewSimpleClientset()
			cs := kubefake.NewSimpleClientset()
			// create gang by podGroup
			if tt.pg != nil {
				if tt.annotations != nil {
					tt.pg.Annotations = tt.annotations
				}
				_, err := pgClientSet.SchedulingV1alpha1().PodGroups(tt.pg.Namespace).Create(context.TODO(), tt.pg, metav1.CreateOptions{})
				if err != nil {
					t.Errorf("create podGroup err: %v", err)
				}
			}

			suit := newPluginTestSuit(t, nil, pgClientSet, cs)
			gp := suit.plugin.(*Coscheduling)
			suit.start()
			gangId := util.GetId(tt.pod.Namespace, util.GetGangNameByPod(tt.pod))
			if tt.resourceSatisfied {
				gp.PostBind(context.TODO(), nil, tt.pod, "test")
			}

			cycleState := framework.NewCycleState()
			var wg sync.WaitGroup
			// pod3 case should test waitingPods
			if tt.pod.Name == "pod3" {
				wg.Add(2)
			}
			for _, pod := range tt.pods {
				tmpPod := pod
				suit.Handle.(framework.Framework).RunPermitPlugins(context.Background(), cycleState, tmpPod, "")
				// start goroutine to wait for the waitingPod's status channel from PostFilter stage
				go func() {
					status := suit.Handle.(framework.Framework).WaitOnPermit(context.Background(), tmpPod)
					if status.IsSuccess() {
						t.Error()
					}
					defer wg.Done()
				}()
			}
			if tt.pod.Name == "pod3" {
				totalWaitingPods := 0
				suit.Handle.IterateOverWaitingPods(
					func(waitingPod framework.WaitingPod) {
						waitingGangId := util.GetId(waitingPod.GetPod().Namespace,
							util.GetGangNameByPod(waitingPod.GetPod()))
						if waitingGangId == gangId {
							klog.Infof("waitingPod name is %v", waitingPod.GetPod().Name)
							totalWaitingPods++
							if waitingPod.GetPod().Name != "pod3-1" && waitingPod.GetPod().Name != "pod3-2" {
								t.Error()
							}
						}
					})
				if totalWaitingPods != 2 {
					klog.Infof("totalWaitingPods is %v ", totalWaitingPods)
					t.Error()
				}
			}
			// reject waitingPods
			_, code := gp.PostFilter(context.Background(), cycleState, tt.pod, nil)
			if code.Message() == "" != tt.expectedEmptyMsg {
				t.Errorf("expectedEmptyMsg %v, got %v", tt.expectedEmptyMsg, code.Message() == "")
			}
			wg.Wait()
		})
	}
}

func TestPermit(t *testing.T) {
	gangACreatedTime := time.Now()
	// we created gangA by PodGroup,gangA has no gangGroup need
	pg1 := makePg("ganga", "gangA_ns", 3, &gangACreatedTime, nil)
	// we created gangB by PodGroup,gangB has gangGroup [gangA,gangB]
	pg2 := makePg("gangb", "gangB_ns", 2, &gangACreatedTime, nil)
	pg2.Annotations = map[string]string{extension.AnnotationGangGroups: "[\"gangA_ns/ganga\",\"gangB_ns/gangb\"]"}

	// we created gangC by PodGroup,gangC has gangGroup [gangC,gangD]
	pg3 := makePg("gangc", "gangC_ns", 2, &gangACreatedTime, nil)
	pg3.Annotations = map[string]string{extension.AnnotationGangGroups: "[\"gangC_ns/gangc\",\"gangD_ns/gangd\"]"}
	// we created gangD by PodGroup,gangD has gangGroup [gangC,gangD]
	pg4 := makePg("gangd", "gangD_ns", 2, &gangACreatedTime, nil)
	pg4.Annotations = map[string]string{extension.AnnotationGangGroups: "[\"gangC_ns/gangc\",\"gangD_ns/gangd\"]"}

	tests := []struct {
		name                  string
		pod                   *corev1.Pod
		pods                  []*corev1.Pod
		want                  framework.Code
		toActivePodsKeys      []string
		needInWaitingPods     bool
		shouldWait            bool
		waitingGangMap        map[string]bool
		shouldTestTimeoutTime bool
	}{
		{
			name: "pod1 does not belong to any pg, allow",
			pod:  st.MakePod().Name("pod1").UID("pod1").Namespace("ns1").Obj(),
			want: framework.Success,
		},
		{
			name: "pod2 belongs to a non-existing pg",
			pod:  st.MakePod().Name("pod2").UID("pod2").Namespace("ns1").Label(v1alpha1.PodGroupLabel, "gangnonexist").Obj(),
			want: framework.Wait,
		},
		{
			name: "pod3 belongs to gangA that doesn't have enough assumed pods",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			},
			want:             framework.Wait,
			shouldWait:       true,
			toActivePodsKeys: []string{"gangA_ns/pod3-1"},
		},
		{
			name: "pod4 belongs to gangA that gangA has resourceSatisfied",
			pod:  st.MakePod().Name("pod4").UID("pod4").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod4-1").UID("pod4-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
				st.MakePod().Name("pod4-2").UID("pod4-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			},
			needInWaitingPods: true,
			waitingGangMap: map[string]bool{
				"gangA_ns/ganga": true,
			},
			want: framework.Success,
		},
		{
			name: "pod4 belongs to gangA that gangA has resourceSatisfied, but gangA matchPolicy is not once-satisfied",
			pod:  st.MakePod().Name("pod4").UID("pod4").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod4-1").UID("pod4-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
				st.MakePod().Name("pod4-2").UID("pod4-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			},
			needInWaitingPods: true,
			waitingGangMap: map[string]bool{
				"gangA_ns/ganga": true,
			},
			want: framework.Success,
		},
		{
			name: "pod5 belongs to gangB that gangB has resourceSatisfied, but gangA has not satisfied",
			pod:  st.MakePod().Name("pod5").UID("pod5").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod5-1").UID("pod5-1").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
				st.MakePod().Name("pod5-2").UID("pod5-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			},
			toActivePodsKeys: []string{"gangB_ns/pod5-1"},
			shouldWait:       true,
			want:             framework.Wait,
		},
		{
			name: "pod6 belongs to gangD that gangD has resourceSatisfied, and gangC is waiting for gangD ",
			pod:  st.MakePod().Name("pod6").UID("pod6").Namespace("gangD_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod6-1").UID("pod6-1").Namespace("gangD_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
				st.MakePod().Name("pod6-2").UID("pod6-2").Namespace("gangC_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
				st.MakePod().Name("pod6-3").UID("pod6-3").Namespace("gangC_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
				st.MakePod().Name("pod6-4").UID("pod6-4").Namespace("gangC_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
			},
			needInWaitingPods: true,
			waitingGangMap: map[string]bool{
				"gangD_ns/gangd": true,
				"gangC_ns/gangc": true,
			},
			want: framework.Success,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgClientSet := fakepgclientset.NewSimpleClientset()
			cs := kubefake.NewSimpleClientset()
			// create gang by podGroup
			pgClientSet.SchedulingV1alpha1().PodGroups(pg1.Namespace).Create(context.Background(), pg1, metav1.CreateOptions{})
			pgClientSet.SchedulingV1alpha1().PodGroups(pg2.Namespace).Create(context.Background(), pg2, metav1.CreateOptions{})
			pgClientSet.SchedulingV1alpha1().PodGroups(pg3.Namespace).Create(context.Background(), pg3, metav1.CreateOptions{})
			pgClientSet.SchedulingV1alpha1().PodGroups(pg4.Namespace).Create(context.Background(), pg4, metav1.CreateOptions{})
			// create  pods
			for _, pod := range tt.pods {
				cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
			}
			cs.CoreV1().Pods(tt.pod.Namespace).Create(context.TODO(), tt.pod, metav1.CreateOptions{})

			suit := newPluginTestSuit(t, nil, pgClientSet, cs)
			suit.start()
			gp := suit.plugin.(*Coscheduling)

			// add assumed pods
			ctx := context.Background()
			cycleState := framework.NewCycleState()

			podsToActivate := framework.NewPodsToActivate()
			cycleState.Write(framework.PodsToActivateKey, podsToActivate)

			var wg sync.WaitGroup
			waitNum := len(tt.pods)

			if tt.shouldWait {
				for _, pod := range tt.pods {
					suit.Handle.(framework.Framework).RunPermitPlugins(ctx, cycleState, pod, "")
				}
			}
			if tt.needInWaitingPods {
				wg.Add(waitNum)
				for _, pod := range tt.pods {
					suit.Handle.(framework.Framework).RunPermitPlugins(ctx, cycleState, pod, "")
					// start goroutine to wait for the waitingPod's Allow signal from Permit stage
					go func(pod *corev1.Pod) {
						defer wg.Done()
						status := suit.Handle.(framework.Framework).WaitOnPermit(context.Background(), pod)
						if !status.IsSuccess() {
							t.Error()
						}
					}(pod)
				}
				totalWaitingPods := 0
				suit.Handle.IterateOverWaitingPods(
					func(waitingPod framework.WaitingPod) {
						waitingGangId := util.GetId(waitingPod.GetPod().Namespace,
							util.GetGangNameByPod(waitingPod.GetPod()))
						if !tt.waitingGangMap[waitingGangId] {
							t.Errorf("waitingGangMap should have gang gangid: %v ", waitingGangId)
						} else {
							totalWaitingPods++
						}
					})
				if totalWaitingPods != waitNum {
					klog.Infof("totalWaitingPods is %v", totalWaitingPods)
					t.Error()
				}
			}

			if got, _ := gp.Permit(ctx, cycleState, tt.pod, ""); got.Code() != tt.want {
				t.Errorf("Expect %v, but got %v", tt.want, got.Code())
			}
			wg.Wait()
			if len(tt.toActivePodsKeys) != 0 {
				for _, toActivePodsKey := range tt.toActivePodsKeys {
					if c, err := cycleState.Read(framework.PodsToActivateKey); err == nil {
						if s, ok := c.(*framework.PodsToActivate); ok {
							s.Lock()
							if _, ok := s.Map[toActivePodsKey]; !ok {
								t.Errorf("cycleState read pod key err,didn't find key:%v from cycleState", toActivePodsKey)
							}
							s.Unlock()
						}
					}
				}
			}
		})
	}
}

func TestUnreserve(t *testing.T) {
	pgClientSet := fakepgclientset.NewSimpleClientset()
	cs := kubefake.NewSimpleClientset()
	gangACreatedTime := time.Now()
	// we created gangA by PodGroup,
	pg1 := makePg("gangA", "gangA_ns", 2, &gangACreatedTime, nil)
	pgClientSet.SchedulingV1alpha1().PodGroups("gangA_ns").Create(context.TODO(), pg1, metav1.CreateOptions{})

	tests := []struct {
		name    string
		pod     *corev1.Pod
		pods    []*corev1.Pod
		timeout bool
	}{
		{
			name: "pod1 comes to Unreserve due to timeout,reject all gangA's pods",
			pod:  st.MakePod().Name("pod1").UID("pod1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod1-2").UID("pod1-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			timeout: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, status := cs.CoreV1().Pods("gangA_ns").Create(context.TODO(), tt.pod, metav1.CreateOptions{})
			assert.Nil(t, status)
			for _, pod := range tt.pods {
				_, status := cs.CoreV1().Pods("gangA_ns").Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.Nil(t, status)
			}

			suit := newPluginTestSuit(t, nil, pgClientSet, cs)
			gp := suit.plugin.(*Coscheduling)
			suit.start()

			ctx := context.TODO()
			cycleState := framework.NewCycleState()
			gangId := tt.pod.Namespace + "/" + tt.pod.Labels[v1alpha1.PodGroupLabel]
			var wg sync.WaitGroup
			waitNum := len(tt.pods)

			if tt.timeout {
				wg.Add(waitNum)
				for _, pod := range tt.pods {
					tmpPod := pod
					suit.Handle.(framework.Framework).RunPermitPlugins(context.Background(), cycleState, tmpPod, "")
					// start goroutine to wait for the waitingPod's Reject signal from Unreserve stage
					go func() {
						status := suit.Handle.(framework.Framework).WaitOnPermit(context.Background(), tmpPod)
						if status.IsSuccess() {
							t.Error()
						}
						defer wg.Done()
					}()
				}
				// assert waitingPods
				totalWaitingPods := 0
				suit.Handle.IterateOverWaitingPods(
					func(waitingPod framework.WaitingPod) {
						waitingGangId := util.GetId(waitingPod.GetPod().Namespace,
							util.GetGangNameByPod(waitingPod.GetPod()))
						if waitingGangId == gangId {
							klog.Infof("waitingPod name is %v", waitingPod.GetPod().Name)
							totalWaitingPods++
							if waitingPod.GetPod().Name != tt.pods[0].Name {
								t.Error()
							}
						}
					})
				if totalWaitingPods != waitNum {
					t.Error()
				}
			}

			gp.Unreserve(ctx, cycleState, tt.pod, "")
			wg.Wait()

		})
	}
}

func TestFairness(t *testing.T) {
	gangNames := []string{"gangA", "gangB", "gangC", "gangD", "gangE", "gangF", "gangG", "gangH", "gangI", "gangJ"}
	gangGroups := map[string][]string{
		"gangA": {"default/gangA", "default/gangB", "default/gangC"},
		"gangB": {"default/gangA", "default/gangB", "default/gangC"},
		"gangC": {"default/gangA", "default/gangB", "default/gangC"},
		"gangD": {"default/gangD", "default/gangE"},
		"gangE": {"default/gangD", "default/gangE"},
		"gangF": {"default/gangF", "default/gangG"},
		"gangG": {"default/gangF", "default/gangG"},
		"gangH": {"default/gangH", "default/gangI", "default/gangJ"},
		"gangI": {"default/gangH", "default/gangI", "default/gangJ"},
		"gangJ": {"default/gangH", "default/gangI", "default/gangJ"},
	}
	gangMinRequiredNums := []int{20, 10, 32, 20, 20, 18, 43, 20, 30, 20}
	gangInjectFilterError := []bool{false, false, true, false, false, true, false, false, false, true}
	var gangInjectFilterErrorIndex []int
	for _, gangMinRequiredNum := range gangMinRequiredNums {
		gangInjectFilterErrorIndex = append(gangInjectFilterErrorIndex, rand.Intn(gangMinRequiredNum))
	}

	podGroupCRs := map[string]*v1alpha1.PodGroup{}
	memberPodsOfGang := map[string][]*corev1.Pod{}
	var allPods []*corev1.Pod
	for i, gangName := range gangNames {
		gangID := util.GetId("default", gangName)

		podGroup := makePg(gangName, "default", int32(gangMinRequiredNums[i]), nil, nil)
		podGroup.Spec.ScheduleTimeoutSeconds = pointer.Int32(3600)
		gangGroup := gangGroups[gangName]
		rawGangGroup, err := json.Marshal(gangGroup)
		assert.NoError(t, err)
		podGroup.Annotations = map[string]string{extension.AnnotationGangGroups: string(rawGangGroup)}
		podGroupCRs[gangID] = podGroup

		for j := 0; j < gangMinRequiredNums[i]; j++ {
			memberPodOfGang := st.MakePod().
				Namespace("default").
				Name(fmt.Sprintf("%s_%d", gangName, j)).
				UID(fmt.Sprintf("%s_%d", gangName, j)).
				Label(v1alpha1.PodGroupLabel, gangName).
				SchedulerName("koord-scheduler").Obj()
			if gangInjectFilterError[i] && j == gangInjectFilterErrorIndex[i] {
				memberPodOfGang.Labels["filterError"] = "true"
			}
			memberPodsOfGang[gangID] = append(memberPodsOfGang[gangID], memberPodOfGang)
			allPods = append(allPods, memberPodOfGang)
		}
	}

	pgClientSet := fakepgclientset.NewSimpleClientset()
	cs := kubefake.NewSimpleClientset()
	for _, podGroupCR := range podGroupCRs {
		_, err := pgClientSet.SchedulingV1alpha1().PodGroups(podGroupCR.Namespace).Create(context.Background(), podGroupCR, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	rand.Shuffle(len(allPods), func(i, j int) {
		allPods[i], allPods[j] = allPods[j], allPods[i]
	})
	for _, pod := range allPods {
		_, err := cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	cfg := configtesting.V1beta3ToInternalWithDefaults(t, v1beta3.KubeSchedulerConfiguration{
		Profiles: []v1beta3.KubeSchedulerProfile{{
			SchedulerName: pointer.StringPtr("koord-scheduler"),
			Plugins: &v1beta3.Plugins{
				QueueSort: v1beta3.PluginSet{
					Enabled: []v1beta3.Plugin{
						{Name: "fakeQueueSortPlugin"},
					},
					Disabled: []v1beta3.Plugin{
						{Name: "*"},
					},
				},
			},
		}}})

	suit := newPluginTestSuit(t, nil, pgClientSet, cs)
	registry := frameworkruntime.Registry{
		"fakeQueueSortPlugin": func(_ apiruntime.Object, fh framework.Handle) (framework.Plugin, error) {
			return suit.plugin.(framework.QueueSortPlugin), nil
		},
	}

	eventBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: cs.EventsV1()})
	ctx := context.TODO()
	sched, err := scheduler.New(
		cs,
		suit.SharedInformerFactory(),
		nil,
		profile.NewRecorderFactory(eventBroadcaster),
		ctx.Done(),
		scheduler.WithProfiles(cfg.Profiles...),
		scheduler.WithFrameworkOutOfTreeRegistry(registry),
		scheduler.WithPodInitialBackoffSeconds(0),
		scheduler.WithPodMaxBackoffSeconds(0),
		scheduler.WithPodMaxInUnschedulablePodsDuration(0),
	)
	assert.NoError(t, err)
	eventBroadcaster.StartRecordingToSink(ctx.Done())
	suit.start()
	sched.SchedulingQueue.Run()

	var scheduleOrder []*debugPodScheduleInfo

	for i := 0; i < 3; i++ {
		for j := 0; j < len(allPods); j++ {
			simulateScheduleOne(t, ctx, sched, suit, &scheduleOrder, func(pod *corev1.Pod) bool {
				if gangName := util.GetGangNameByPod(pod); gangName == "gangD" || gangName == "gangE" {
					waitingPod := 0
					gangSummaries := suit.plugin.(*Coscheduling).pgMgr.GetGangSummaries()
					for _, gangSummary := range gangSummaries {
						if !gangSummary.ScheduleCycleValid {
							continue
						}
						waitingPod += gangSummary.WaitingForBindChildren.Len()
					}

					return waitingPod >= 40
				}
				return pod.Labels["filterError"] == "true"
			})
		}
	}

	waitingPodNum := 0
	suit.Handle.IterateOverWaitingPods(func(pod framework.WaitingPod) {
		waitingPodNum++
	})
	minGangSchedulingCycle, maxGangSchedulingCycle := math.MaxInt, 0
	gangSummaries := suit.plugin.(*Coscheduling).pgMgr.GetGangSummaries()

	nonZeroWaitingBoundGroup := map[string]bool{}
	for _, gangSummary := range gangSummaries {
		if gangSummary.Name == "default/gangD" || gangSummary.Name == "default/gangE" {
			assert.True(t, gangSummary.OnceResourceSatisfied)
			assert.Zero(t, len(gangSummary.WaitingForBindChildren))
			continue
		}
		if len(gangSummary.WaitingForBindChildren) != 0 {
			nonZeroWaitingBoundGroup[strings.Join(gangSummary.GangGroup, ",")] = true
		}
		if gangSummary.ScheduleCycle < minGangSchedulingCycle {
			minGangSchedulingCycle = gangSummary.ScheduleCycle
		}
		if gangSummary.ScheduleCycle > maxGangSchedulingCycle {
			maxGangSchedulingCycle = gangSummary.ScheduleCycle
		}
	}
	assert.LessOrEqual(t, 3, minGangSchedulingCycle)
	for _, info := range scheduleOrder {
		klog.Infoln(info)
	}
}

type debugPodScheduleInfo struct {
	podID                   string
	result                  string
	childScheduleCycle      int
	gangScheduleCycle       int
	hasWaitForBoundChildren bool
	schedulingCycleValid    bool
	lastScheduleTime        time.Time
}

func (p *debugPodScheduleInfo) String() string {
	return fmt.Sprintf("%s,%s,%d,%d,%t,%s,%t", p.podID, p.lastScheduleTime, p.childScheduleCycle, p.gangScheduleCycle, p.schedulingCycleValid, p.result, p.hasWaitForBoundChildren)
}

func simulateScheduleOne(t *testing.T, ctx context.Context, sched *scheduler.Scheduler, suit *pluginTestSuit, scheduleOrder *[]*debugPodScheduleInfo, injectFilterErr func(pod *corev1.Pod) bool) {
	podInfo := sched.NextPod()
	pod := podInfo.Pod

	scheduleInfo := &debugPodScheduleInfo{
		podID:              pod.Name,
		childScheduleCycle: suit.plugin.(*Coscheduling).pgMgr.GetChildScheduleCycle(pod),
	}
	summary, exists := suit.plugin.(*Coscheduling).pgMgr.GetGangSummary(util.GetId(pod.Namespace, util.GetGangNameByPod(pod)))
	if exists {
		scheduleInfo.gangScheduleCycle = summary.ScheduleCycle
		scheduleInfo.schedulingCycleValid = summary.ScheduleCycleValid
		scheduleInfo.hasWaitForBoundChildren = summary.WaitingForBindChildren.Len() != 0
		scheduleInfo.lastScheduleTime = suit.plugin.(*Coscheduling).pgMgr.GetGangGroupLastScheduleTimeOfPod(pod, time.Time{})
	}
	*scheduleOrder = append(*scheduleOrder, scheduleInfo)
	fwk := suit.Handle.(framework.Framework)
	klog.InfoS("Attempting to schedule pod", "pod", klog.KObj(pod))

	// Synchronously attempt to find a fit for the pod.
	state := framework.NewCycleState()
	// Initialize an empty podsToActivate struct, which will be filled up by plugins or stay empty.
	podsToActivate := framework.NewPodsToActivate()
	state.Write(framework.PodsToActivateKey, podsToActivate)

	schedulingCycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	scheduleResult, err := schedulePod(schedulingCycleCtx, fwk, state, pod, scheduleInfo, injectFilterErr(pod))
	if err != nil {
		// SchedulePod() may have failed because the pod would not fit on any host, so we try to
		// preempt, with the expectation that the next time the pod is tried for scheduling it
		// will fit due to the preemption. It is also possible that a different pod will schedule
		// into the resources that were preempted, but this is harmless.
		if fitError, ok := err.(*framework.FitError); ok {
			// Run PostFilter plugins to try to make the pod schedulable in a future scheduling cycle.
			_, status := suit.plugin.(*Coscheduling).PostFilter(ctx, state, pod, fitError.Diagnosis.NodeToStatusMap)
			assert.False(t, status.IsSuccess())
		}
		klog.Info("sched.Error:" + podInfo.Pod.Name)
		sched.Error(podInfo, err)
		return
	}

	// Tell the cache to assume that a pod now is running on a given node, even though it hasn't been bound yet.
	// This allows us to keep scheduling without waiting on binding to occur.
	assumedPodInfo := podInfo.DeepCopy()
	assumedPod := assumedPodInfo.Pod
	assumedPod.Spec.NodeName = scheduleResult.SuggestedHost
	scheduleInfo.result = "assumed"

	// Run "permit" plugins.
	runPermitStatus := fwk.RunPermitPlugins(schedulingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
	if runPermitStatus.Code() != framework.Wait && !runPermitStatus.IsSuccess() {
		// One of the plugins returned status different from success or wait.
		fwk.RunReservePluginsUnreserve(schedulingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
		klog.Info("sched.Error:" + assumedPodInfo.Pod.Name)
		sched.Error(assumedPodInfo, runPermitStatus.AsError())
		return
	}

	// At the end of a successful scheduling cycle, pop and move up Pods if needed.
	if len(podsToActivate.Map) != 0 {
		sched.SchedulingQueue.Activate(podsToActivate.Map)
		// Clear the entries after activation.
		podsToActivate.Map = make(map[string]*corev1.Pod)
	}

	// bind the pod to its host asynchronously (we can do this b/c of the assumption step above).
	go func() {
		bindingCycleCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		waitOnPermitStatus := fwk.WaitOnPermit(bindingCycleCtx, assumedPod)
		if !waitOnPermitStatus.IsSuccess() {
			// trigger un-reserve plugins to clean up state associated with the reserved Pod
			fwk.RunReservePluginsUnreserve(bindingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
			klog.Info("sched.Error:" + assumedPodInfo.Pod.Name)
			sched.Error(assumedPodInfo, waitOnPermitStatus.AsError())
			return
		}

		// Run "PostBind" plugins.
		fwk.RunPostBindPlugins(bindingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)

		// At the end of a successful binding cycle, move up Pods if needed.
		if len(podsToActivate.Map) != 0 {
			sched.SchedulingQueue.Activate(podsToActivate.Map)
			// Unlike the logic in scheduling cycle, we don't bother deleting the entries
			// as `podsToActivate.Map` is no longer consumed.
		}
	}()
}

func schedulePod(ctx context.Context, fwk framework.Framework, state *framework.CycleState, pod *corev1.Pod, info *debugPodScheduleInfo, injectFilterError bool) (result scheduler.ScheduleResult, err error) {
	diagnosis := framework.Diagnosis{
		NodeToStatusMap:      make(framework.NodeToStatusMap),
		UnschedulablePlugins: sets.NewString(),
	}

	// Run "prefilter" plugins.
	_, s := fwk.RunPreFilterPlugins(ctx, state, pod)
	if !s.IsSuccess() {
		info.result = "PreFiler"
	} else if injectFilterError {
		info.result = "Filter"
	}
	if info.result != "" {
		return result, &framework.FitError{
			Pod:         pod,
			NumAllNodes: 1,
			Diagnosis:   diagnosis,
		}
	}
	return scheduler.ScheduleResult{
		SuggestedHost: "fake-host",
	}, err
}

func TestDeadLockFree(t *testing.T) {
	gangNames := []string{"gangA", "gangB", "gangC", "gangD"}
	gangGroups := map[string][]string{
		"gangA": {"default/gangA"},
		"gangB": {"default/gangB"},
		"gangC": {"default/gangC"},
		"gangD": {"default/gangD"},
	}
	gangMinRequiredNums := []int{13, 13, 13, 13}

	podGroupCRs := map[string]*v1alpha1.PodGroup{}
	memberPodsOfGang := map[string][]*corev1.Pod{}
	var allPods []*corev1.Pod
	for i, gangName := range gangNames {
		gangID := util.GetId("default", gangName)

		podGroup := makePg(gangName, "default", int32(gangMinRequiredNums[i]), nil, nil)
		podGroup.Spec.ScheduleTimeoutSeconds = pointer.Int32(3600)
		gangGroup := gangGroups[gangName]
		rawGangGroup, err := json.Marshal(gangGroup)
		assert.NoError(t, err)
		podGroup.Annotations = map[string]string{extension.AnnotationGangGroups: string(rawGangGroup)}
		podGroupCRs[gangID] = podGroup

		for j := 0; j < gangMinRequiredNums[i]; j++ {
			memberPodOfGang := st.MakePod().
				Namespace("default").
				Name(fmt.Sprintf("%s_%d", gangName, j)).
				UID(fmt.Sprintf("%s_%d", gangName, j)).
				Label(v1alpha1.PodGroupLabel, gangName).
				SchedulerName("koord-scheduler").Obj()
			memberPodsOfGang[gangID] = append(memberPodsOfGang[gangID], memberPodOfGang)
			allPods = append(allPods, memberPodOfGang)
		}
	}

	pgClientSet := fakepgclientset.NewSimpleClientset()
	cs := kubefake.NewSimpleClientset()
	for _, podGroupCR := range podGroupCRs {
		_, err := pgClientSet.SchedulingV1alpha1().PodGroups(podGroupCR.Namespace).Create(context.Background(), podGroupCR, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	rand.Shuffle(len(allPods), func(i, j int) {
		allPods[i], allPods[j] = allPods[j], allPods[i]
	})
	for _, pod := range allPods {
		_, err := cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	cfg := configtesting.V1beta3ToInternalWithDefaults(t, v1beta3.KubeSchedulerConfiguration{
		Profiles: []v1beta3.KubeSchedulerProfile{{
			SchedulerName: pointer.StringPtr("koord-scheduler"),
			Plugins: &v1beta3.Plugins{
				QueueSort: v1beta3.PluginSet{
					Enabled: []v1beta3.Plugin{
						{Name: "fakeQueueSortPlugin"},
					},
					Disabled: []v1beta3.Plugin{
						{Name: "*"},
					},
				},
			},
		}}})

	suit := newPluginTestSuit(t, nil, pgClientSet, cs)
	registry := frameworkruntime.Registry{
		"fakeQueueSortPlugin": func(_ apiruntime.Object, fh framework.Handle) (framework.Plugin, error) {
			return suit.plugin.(framework.QueueSortPlugin), nil
		},
	}

	eventBroadcaster := events.NewBroadcaster(&events.EventSinkImpl{Interface: cs.EventsV1()})
	ctx := context.TODO()
	sched, err := scheduler.New(cs,
		suit.SharedInformerFactory(),
		nil,
		profile.NewRecorderFactory(eventBroadcaster),
		ctx.Done(),
		scheduler.WithProfiles(cfg.Profiles...),
		scheduler.WithFrameworkOutOfTreeRegistry(registry),
		scheduler.WithPodInitialBackoffSeconds(1),
		scheduler.WithPodMaxBackoffSeconds(1),
		scheduler.WithPodMaxInUnschedulablePodsDuration(0),
	)

	assert.NoError(t, err)
	eventBroadcaster.StartRecordingToSink(ctx.Done())
	suit.start()
	sched.SchedulingQueue.Run()

	var scheduleOrder []*debugPodScheduleInfo

	for i := 0; i < 3; i++ {
		for j := 0; j < len(allPods); j++ {
			if len(sched.SchedulingQueue.PendingPods()) == 0 {
				break
			}

			simulateScheduleOne(t, ctx, sched, suit, &scheduleOrder, func(pod *corev1.Pod) bool {
				waitingPod := 0
				gangSummaries := suit.plugin.(*Coscheduling).pgMgr.GetGangSummaries()
				for _, gangSummary := range gangSummaries {
					if gangSummary.ScheduleCycleValid == false {
						continue
					}
					waitingPod += gangSummary.WaitingForBindChildren.Len()
				}
				return waitingPod >= 13
			})
		}
	}
	assert.Equal(t, 0, len(sched.SchedulingQueue.PendingPods()))
	for _, info := range scheduleOrder {
		klog.Infoln(info)
	}
}
