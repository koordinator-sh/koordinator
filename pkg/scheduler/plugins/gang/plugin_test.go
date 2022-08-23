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

package gang

import (
	"context"
	"sync"
	"testing"
	"time"

	nrtinformers "github.com/k8stopologyawareschedwg/noderesourcetopology-api/pkg/generated/informers/externalversions"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	pgclientset "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"

	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	scheduledconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	st "k8s.io/kubernetes/pkg/scheduler/testing"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	pgfake "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned/fake"

	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config"
	schedulingconfig "github.com/koordinator-sh/koordinator/apis/scheduling/config"
	"github.com/koordinator-sh/koordinator/apis/scheduling/config/v1beta2"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
)

// gang test used
type PodGroupClientSetAndHandle struct {
	framework.Handle
	pgclientset.Interface
}

func GangPluginFactoryProxy(clientSet pgclientset.Interface, factoryFn frameworkruntime.PluginFactory) frameworkruntime.PluginFactory {
	return func(args apiruntime.Object, handle framework.Handle) (framework.Plugin, error) {
		return factoryFn(args, PodGroupClientSetAndHandle{Handle: handle, Interface: clientSet})
	}
}

// unit test gang helper funcs
func (gang *Gang) setGangMode(mode string) {
	gang.lock.Lock()
	defer gang.lock.Unlock()
	gang.Mode = mode
	klog.Infof("SetGangMode, gangName:%v, mode:%v", gang.Name, mode)
}

func (gang *Gang) setTotalNum(num int) {
	gang.lock.Lock()
	defer gang.lock.Unlock()
	gang.TotalChildrenNum = num
	klog.Infof("SetTotalNum, gangName:%v, totalNum:%v", gang.Name, num)
}

func (gang *Gang) setScheduleCycle(cycle int) {
	gang.lock.Lock()
	defer gang.lock.Unlock()
	gang.ScheduleCycle = cycle
	klog.Infof("SetScheduleCycle, gangName:%v, cycle:%v", gang.Name, cycle)
}

var fakeTimeNowFn = func() time.Time {
	t := time.Time{}
	t.Add(100 * time.Second)
	return t
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
	koordinatorSharedInformerFactory koordinatorinformers.SharedInformerFactory
	nrtSharedInformerFactory         nrtinformers.SharedInformerFactory
	proxyNew                         runtime.PluginFactory
	gangSchedulingArgs               *schedulingconfig.GangArgs
	pgClient                         *pgfake.Clientset
}

func newPluginTestSuit(t *testing.T, nodes []*corev1.Node) *pluginTestSuit {
	var v1beta2args v1beta2.GangArgs
	v1beta2.SetDefaults_GangSchedulingArgs(&v1beta2args)
	var gangSchedulingArgs config.GangArgs
	err := v1beta2.Convert_v1beta2_GangArgs_To_config_GangArgs(&v1beta2args, &gangSchedulingArgs, nil)
	assert.NoError(t, err)

	gangSchedulingPluginConfig := scheduledconfig.PluginConfig{
		Name: Name,
		Args: &gangSchedulingArgs,
	}

	koordClientSet := koordfake.NewSimpleClientset()
	koordSharedInformerFactory := koordinatorinformers.NewSharedInformerFactory(koordClientSet, 0)

	pgClientSet := pgfake.NewSimpleClientset()
	proxyNew := GangPluginFactoryProxy(pgClientSet, New)

	registeredPlugins := []schedulertesting.RegisterPluginFunc{
		func(reg *runtime.Registry, profile *scheduledconfig.KubeSchedulerProfile) {
			profile.PluginConfig = []scheduledconfig.PluginConfig{
				gangSchedulingPluginConfig,
			}
		},
		schedulertesting.RegisterBindPlugin(defaultbinder.Name, defaultbinder.New),
		schedulertesting.RegisterQueueSortPlugin(Name, proxyNew),
		schedulertesting.RegisterPreFilterPlugin(Name, proxyNew),
		schedulertesting.RegisterPermitPlugin(Name, proxyNew),
	}

	cs := kubefake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(cs, 0)
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
		Handle:                           fh,
		koordinatorSharedInformerFactory: koordSharedInformerFactory,
		proxyNew:                         proxyNew,
		gangSchedulingArgs:               &gangSchedulingArgs,
		pgClient:                         pgClientSet,
	}
}

func (p *pluginTestSuit) start() {
	ctx := context.TODO()
	p.Handle.SharedInformerFactory().Start(ctx.Done())
	p.Handle.SharedInformerFactory().WaitForCacheSync(ctx.Done())
}

func TestRecover(t *testing.T) {
	suit := newPluginTestSuit(t, nil)

	waitTime := int32(300)

	pods := []*corev1.Pod{
		// pod1 announce gangA
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod1",
				Annotations: map[string]string{
					extension.AnnotationGangName:     "gangA",
					extension.AnnotationGangMinNum:   "2",
					extension.AnnotationGangTotalNum: "10",
					extension.AnnotationGangWaitTime: "300s",
					extension.AnnotationGangMode:     extension.GangModeNonStrict,
					extension.AnnotationGangGroups:   "[\"default/gangA\",\"default/gangB\"]",
				},
			},
		},
		// pod2 belongs to gangA
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod2",
				Annotations: map[string]string{
					extension.AnnotationGangName: "gangA",
				},
			},
		},

		// pod3 announce GangB,has already assumed to NodeA,
		// other pods belong to GangB didn't bind successfully
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "pod3",
				Annotations: map[string]string{
					extension.AnnotationGangName:     "gangB",
					extension.AnnotationGangMinNum:   "3",
					extension.AnnotationGangWaitTime: "300s",
					extension.AnnotationGangGroups:   "[\"default/gangA\",\"default/gangB\"]",
				},
			},
			//has bound
			Spec: corev1.PodSpec{NodeName: "NodeA"},
		},

		// pod4 announce GangC,which is created by PodGroup
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod4",
				Namespace: "default",
				Labels:    map[string]string{v1alpha1.PodGroupLabel: "gangC"},
			},
		},
		// pod5 announce GangC,which is created by PodGroup
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod5",
				Namespace: "default",
				Labels:    map[string]string{v1alpha1.PodGroupLabel: "gangC"},
			},
		},
		// pod6 has no gang
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod6",
				Namespace: "default",
			},
		},
		// pod7 's gang's podGroup hasn't created
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod7",
				Labels:    map[string]string{v1alpha1.PodGroupLabel: "wenshiqi222"},
				Namespace: "default",
			},
			//has bound
			Spec: corev1.PodSpec{NodeName: "NodeA"},
		},
	}
	podGroups := []*v1alpha1.PodGroup{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "gangC",
				Annotations: map[string]string{
					extension.AnnotationGangMode: extension.GangModeNonStrict,
				},
			},
			Spec: v1alpha1.PodGroupSpec{
				MinMember:              2,
				ScheduleTimeoutSeconds: &waitTime,
			},
		},
	}
	wantCache := map[string]*Gang{
		"default/gangA": {
			Name:              "default/gangA",
			WaitTime:          300 * time.Second,
			CreateTime:        fakeTimeNowFn(),
			Mode:              extension.GangModeNonStrict,
			MinRequiredNumber: 2,
			TotalChildrenNum:  10,
			GangGroup:         []string{"default/gangA", "default/gangB"},
			HasGangInit:       true,
			GangFrom:          GangFromPodAnnotation,
			Children: map[string]*corev1.Pod{
				"default/pod1": {
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod1",
						Annotations: map[string]string{
							extension.AnnotationGangName:     "gangA",
							extension.AnnotationGangMinNum:   "2",
							extension.AnnotationGangTotalNum: "10",
							extension.AnnotationGangWaitTime: "300s",
							extension.AnnotationGangMode:     extension.GangModeNonStrict,
							extension.AnnotationGangGroups:   "[\"default/gangA\",\"default/gangB\"]",
						},
					},
				},
				"default/pod2": {
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod2",
						Annotations: map[string]string{
							extension.AnnotationGangName: "gangA",
						},
					},
				},
			},
			WaitingForBindChildren: map[string]*corev1.Pod{},
			BoundChildren:          map[string]*corev1.Pod{},
			ScheduleCycleValid:     true,
			ScheduleCycle:          1,
			ChildrenScheduleRoundMap: map[string]int{
				"default/pod1": 0,
				"default/pod2": 0,
			},
		},
		"default/gangB": {
			Name:              "default/gangB",
			WaitTime:          300 * time.Second,
			CreateTime:        fakeTimeNowFn(),
			Mode:              extension.GangModeStrict,
			MinRequiredNumber: 3,
			TotalChildrenNum:  3,
			GangGroup:         []string{"default/gangA", "default/gangB"},
			HasGangInit:       true,
			GangFrom:          GangFromPodAnnotation,
			Children: map[string]*corev1.Pod{
				"default/pod3": {
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod3",
						Annotations: map[string]string{
							extension.AnnotationGangName:     "gangB",
							extension.AnnotationGangMinNum:   "3",
							extension.AnnotationGangWaitTime: "300s",
							extension.AnnotationGangGroups:   "[\"default/gangA\",\"default/gangB\"]",
						},
					},
					//has assumed
					Spec: corev1.PodSpec{NodeName: "NodeA"},
				},
			},
			WaitingForBindChildren: map[string]*corev1.Pod{},
			BoundChildren: map[string]*corev1.Pod{
				"default/pod3": {
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod3",
						Annotations: map[string]string{
							extension.AnnotationGangName:     "gangB",
							extension.AnnotationGangMinNum:   "3",
							extension.AnnotationGangWaitTime: "300s",
							extension.AnnotationGangGroups:   "[\"default/gangA\",\"default/gangB\"]",
						},
					},
					//has assumed
					Spec: corev1.PodSpec{NodeName: "NodeA"},
				},
			},
			ScheduleCycleValid: true,
			ScheduleCycle:      1,
			ResourceSatisfied:  true,
			ChildrenScheduleRoundMap: map[string]int{
				"default/pod3": 0,
			},
		},
		"default/gangC": {
			Name:              "default/gangC",
			WaitTime:          300 * time.Second,
			CreateTime:        fakeTimeNowFn(),
			Mode:              extension.GangModeNonStrict,
			MinRequiredNumber: 2,
			TotalChildrenNum:  2,
			GangGroup:         []string{},
			HasGangInit:       true,
			GangFrom:          GangFromPodGroupCrd,
			Children: map[string]*corev1.Pod{
				"default/pod4": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod4",
						Namespace: "default",
						Labels:    map[string]string{v1alpha1.PodGroupLabel: "gangC"},
					},
				},
				"default/pod5": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod5",
						Namespace: "default",
						Labels:    map[string]string{v1alpha1.PodGroupLabel: "gangC"},
					},
				},
			},
			WaitingForBindChildren: map[string]*corev1.Pod{},
			BoundChildren:          map[string]*corev1.Pod{},
			ScheduleCycleValid:     true,
			ScheduleCycle:          1,
			ChildrenScheduleRoundMap: map[string]int{
				"default/pod4": 0,
				"default/pod5": 0,
			},
		},
		"default/wenshiqi222": {
			Name:       "default/wenshiqi222",
			CreateTime: fakeTimeNowFn(),
			WaitTime:   0,
			Mode:       extension.GangModeStrict,
			Children: map[string]*corev1.Pod{
				"default/pod7": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod7",
						Labels:    map[string]string{v1alpha1.PodGroupLabel: "wenshiqi222"},
						Namespace: "default",
					},
					//has bound
					Spec: corev1.PodSpec{NodeName: "NodeA"},
				},
			},
			WaitingForBindChildren: make(map[string]*corev1.Pod),
			BoundChildren: map[string]*corev1.Pod{
				"default/pod7": {
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod7",
						Labels:    map[string]string{v1alpha1.PodGroupLabel: "wenshiqi222"},
						Namespace: "default",
					},
					//has bound
					Spec: corev1.PodSpec{NodeName: "NodeA"},
				},
			},
			ScheduleCycleValid: true,
			ScheduleCycle:      1,
			ChildrenScheduleRoundMap: map[string]int{
				"default/pod7": 0,
			},
			GangFrom:          GangFromPodAnnotation,
			HasGangInit:       false,
			ResourceSatisfied: true,
		},
	}

	//add pods podGroups to cache
	for _, pod := range pods {
		suit.Handle.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	}
	for _, pg := range podGroups {
		suit.pgClient.SchedulingV1alpha1().PodGroups(pg.Namespace).Create(context.TODO(), pg, metav1.CreateOptions{})
	}

	preTimeNowFn := timeNowFn
	defer func() {
		timeNowFn = preTimeNowFn
	}()
	timeNowFn = fakeTimeNowFn
	//now the cluster crash,then recover during initializing the GangPlugin
	p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
	gp := p.(*GangPlugin)

	//test New function
	assert.NotNil(t, p)
	assert.Nil(t, err)
	assert.Equal(t, Name, p.Name())
	gangCache := gp.gangCache
	assert.Equal(t, wantCache, gangCache.getGangItems())
}

func TestLess(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)

	var lowPriority, highPriority = int32(10), int32(100)
	//koordinator priority announced in pod's Labels
	var lowSubPriority, highSubPriority = "111", "222"
	var gangA_ns, gangB_ns = "namespace1", "namespace2"
	now := time.Now()
	earltTime := now.Add(1 * time.Second)
	lateTime := now.Add(3 * time.Second)

	//we assume that there are tow gang: gangA and gangB
	//gangA is announced by the pod's annotation,gangB is created by the podGroup
	//so here we need to add two gangs to the cluster

	//GangA by Annotations
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
	//GangB by PodGroup
	gangBCreatTime := now.Add(5 * time.Second)
	pg := makePG("gangB", gangB_ns, 2, &gangBCreatTime, nil)
	suit.start()
	gp := p.(*GangPlugin)
	//creat gangA and gangB
	suit.Handle.ClientSet().CoreV1().Pods(gangA_ns).Create(context.TODO(), podToCreateGangA, metav1.CreateOptions{})
	suit.pgClient.SchedulingV1alpha1().PodGroups(gangB_ns).Create(context.TODO(), pg, metav1.CreateOptions{})

	for _, tt := range []struct {
		name        string
		p1          *framework.QueuedPodInfo
		p2          *framework.QueuedPodInfo
		annotations map[string]string
		expected    bool
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
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
				InitialAttemptTimestamp: earltTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(extension.LabelPodPriority, lowSubPriority).Obj()),
				InitialAttemptTimestamp: lateTime,
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
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod1").Priority(highPriority).Obj()),
				InitialAttemptTimestamp: earltTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod2").Priority(highPriority).Obj()),
				InitialAttemptTimestamp: lateTime,
			},
			expected: true, // p1 should be ahead of p2 in the queue
		},
		{
			name: "equal priority, p1 is added to schedulingQ earlier than p2, but p1 belongs to gangB",
			p1: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod1").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				InitialAttemptTimestamp: earltTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod2").Priority(highPriority).Obj()),
				InitialAttemptTimestamp: lateTime,
			},
			expected: false, // p2 should be ahead of p1 in the queue
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
			name: "equal priority. p2 is added to schedulingQ earlier than p1, p1 belongs to gangA and p2 belongs to gangB",
			p1: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(highPriority).Obj()),
				InitialAttemptTimestamp: lateTime,
			},
			annotations: map[string]string{extension.AnnotationGangName: "gangA"},
			p2: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				InitialAttemptTimestamp: earltTime,
			},
			expected: true, // p1 should be ahead of p2 in the queue
		},
		{
			name: "equal priority and creation time, both belongs to gangB",
			p1: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod1").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				InitialAttemptTimestamp: lateTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				InitialAttemptTimestamp: earltTime,
			},
			expected: true, // p1 should be ahead of p2 in the queue
		},
		{
			name: "equal priority and creation time, p2 belongs to gangB",
			p1: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangA_ns).Name("pod1").Priority(highPriority).Obj()),
				InitialAttemptTimestamp: gangBCreatTime,
			},
			p2: &framework.QueuedPodInfo{
				PodInfo:                 framework.NewPodInfo(st.MakePod().Namespace(gangB_ns).Name("pod2").Priority(highPriority).Label(v1alpha1.PodGroupLabel, "gangB").Obj()),
				InitialAttemptTimestamp: earltTime,
			},
			expected: true, // p1 should be ahead of p2 in the queue
		},
	} {
		t.Run(tt.name, func(t *testing.T) {

			if len(tt.annotations) != 0 {
				tt.p1.Pod.Annotations = tt.annotations
			}
			if got := gp.Less(tt.p1, tt.p2); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}

}

func TestPlugin_PreFilter(t *testing.T) {
	gangACreatedTime := time.Now()
	// we created gangA by PodGroup,StrictMode
	pg1 := makePG("gangA", "gangA_ns", 4, &gangACreatedTime, nil)
	// we created gangB by PodGroup,NonStrictMode
	pg2 := makePG("gangB", "gangB_ns", 4, &gangACreatedTime, nil)

	tests := []struct {
		name string
		pod  *corev1.Pod
		pods []*corev1.Pod
		// assert value
		expectedStatus        framework.Code
		expectedScheduleCycle int
		//cases value
		annotations           map[string]string
		shouldSetChildCycle   bool
		shouldSetTestPodCycle bool
		totalNum              int
		scheduleInValid       bool
	}{
		{
			name:           "pod does not belong to any gang",
			pod:            st.MakePod().Name("pod1").UID("pod1").Namespace("ns1").Obj(),
			pods:           []*corev1.Pod{},
			expectedStatus: framework.Success,
		},
		{
			name:           "pod belongs to a non-existing pg",
			pod:            st.MakePod().Name("pod2").UID("pod2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "Wenshiqi222").Obj(),
			expectedStatus: framework.UnschedulableAndUnresolvable,
		},
		{
			name: "pod count less than minMember",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			expectedStatus: framework.UnschedulableAndUnresolvable,
		},
		{
			name:           "gangA is timeout ",
			pod:            st.MakePod().Name("pod4").UID("pod4").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods:           []*corev1.Pod{},
			annotations:    map[string]string{extension.AnnotationGangTimeout: "true"},
			expectedStatus: framework.UnschedulableAndUnresolvable,
		},
		{
			name: "pods count equal with minMember,but is NonStrictMode",
			pod:  st.MakePod().Name("pod5").UID("pod5").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod5-1").UID("pod5-1").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
				st.MakePod().Name("pod5-2").UID("pod5-2").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
				st.MakePod().Name("pod5-3").UID("pod5-3").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			},
			expectedStatus: framework.Success,
		},
		{
			name: "pods count equal with minMember,is StrictMode,but due to reschedule its podScheduleCycle is equal with the gangScheduleCycle",
			pod:  st.MakePod().Name("pod6").UID("pod6").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod6-1").UID("pod6-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod6-2").UID("pod6-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod6-3").UID("pod6-3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			shouldSetChildCycle:   true,
			shouldSetTestPodCycle: true,
			expectedStatus:        framework.Success,
		},
		{
			name: "pods count equal with minMember,is StrictMode,but the gang's scheduleCycle is not valid due to pre pod Filter Failed",
			pod:  st.MakePod().Name("pod7").UID("pod7").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod7-1").UID("pod7-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod7-2").UID("pod7-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod7-3").UID("pod7-3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			shouldSetChildCycle: true,
			scheduleInValid:     true,
			// pod7 is the last pod of this schedulingCycle,even if it falied
			// we should still move the schedulingCycle+1
			expectedScheduleCycle: 1,
			expectedStatus:        framework.UnschedulableAndUnresolvable,
		},
		{
			name: "pods count equal with minMember,is StrictMode,scheduleCycle valid,but childrenNum is not reach to total num",
			pod:  st.MakePod().Name("pod8").UID("pod8").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod8-1").UID("pod8-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod8-2").UID("pod8-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod8-3").UID("pod8-3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			totalNum:              5,
			shouldSetChildCycle:   true,
			expectedScheduleCycle: 1,
			expectedStatus:        framework.Success,
		},
		{
			name: "pods count more than minMember,is StrictMode,scheduleCycle valid,and childrenNum reach to total num",
			pod:  st.MakePod().Name("pod9").UID("pod9").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod9-1").UID("pod9-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod9-2").UID("pod9-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod9-3").UID("pod9-3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod9-4").UID("pod9-4").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			totalNum:              5,
			shouldSetChildCycle:   true,
			expectedScheduleCycle: 1,
			expectedStatus:        framework.Success,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)
			suit.start()
			gp := p.(*GangPlugin)
			//create gang by podGroup
			suit.pgClient.SchedulingV1alpha1().PodGroups(pg1.Namespace).Create(context.TODO(), pg1, metav1.CreateOptions{})
			suit.pgClient.SchedulingV1alpha1().PodGroups(pg2.Namespace).Create(context.TODO(), pg2, metav1.CreateOptions{})
			time.Sleep(10 * time.Millisecond)

			if tt.annotations != nil {
				tt.pod.Annotations = tt.annotations
			}
			//create  pods
			for _, pod := range tt.pods {
				suit.Handle.ClientSet().CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
			}
			suit.Handle.ClientSet().CoreV1().Pods(tt.pod.Namespace).Create(context.TODO(), tt.pod, metav1.CreateOptions{})
			time.Sleep(10 * time.Millisecond)

			gang := gp.gangCache.getGangFromCache(tt.pod.Namespace + "/" + tt.pod.Labels[v1alpha1.PodGroupLabel])
			if gang != nil {
				if tt.totalNum != 0 {
					gang.setTotalNum(tt.totalNum)
				}
				if tt.scheduleInValid {
					gang.setScheduleCycleValid(false)
				}
				if tt.shouldSetChildCycle {
					gangCycle := gang.getGangScheduleCycle()
					for _, pod := range tt.pods {
						gang.setChildScheduleCycle(pod, gangCycle)
					}
					if tt.shouldSetTestPodCycle {
						gang.setChildScheduleCycle(tt.pod, gangCycle)
					}
				}
			}
			ctx := context.TODO()
			status := gp.PreFilter(ctx, framework.NewCycleState(), tt.pod)
			if status.Code() != tt.expectedStatus {
				t.Errorf("desire %v, get %v", tt.expectedStatus, status.Code())
			}
			if tt.expectedScheduleCycle != 0 {
				if gang.getGangScheduleCycle() != tt.expectedScheduleCycle {
					t.Errorf("desire %v, get %v", tt.expectedScheduleCycle, gang.getGangScheduleCycle())
				}
			}
		})
	}
}

func TestPostFilter(t *testing.T) {
	gangCreatedTime := time.Now()
	// we created gangA by PodGroup,StrictMode
	pg1 := makePG("gangA", "gangA_ns", 4, &gangCreatedTime, nil)
	// we created gangB by PodGroup,NonStrictMode
	pg2 := makePG("gangB", "gangB_ns", 4, &gangCreatedTime, nil)
	pg2.Annotations = map[string]string{extension.AnnotationGangMode: extension.GangModeNonStrict}
	tests := []struct {
		name              string
		pod               *corev1.Pod
		pods              []*corev1.Pod
		expectedEmptyMsg  bool
		resourceSatisfied bool
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
			pod:               st.MakePod().Name("pod1").Namespace("gangA_ns").UID("pod1").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			expectedEmptyMsg:  true,
			resourceSatisfied: true,
		},
		{
			name:             "resource not Satisfied,but gangB is NonStrictMode,do not reject the gang",
			pod:              st.MakePod().Name("pod2").Namespace("gangB_ns").UID("pod2").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			expectedEmptyMsg: true,
		},

		{
			name: "resource not Satisfied,StrictMode,reject all pods belong to gangA",
			pod:  st.MakePod().Name("pod3").Namespace("gangA_ns").UID("pod3").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").Namespace("gangA_ns").UID("pod3-1").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod3-2").Namespace("gangA_ns").UID("pod3-2").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			expectedEmptyMsg: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)
			suit.start()
			gp := p.(*GangPlugin)
			//create gang by podGroup
			suit.pgClient.SchedulingV1alpha1().PodGroups(pg1.Namespace).Create(context.TODO(), pg1, metav1.CreateOptions{})
			suit.pgClient.SchedulingV1alpha1().PodGroups(pg2.Namespace).Create(context.TODO(), pg2, metav1.CreateOptions{})
			time.Sleep(10 * time.Millisecond)
			gangId := tt.pod.Namespace + "/" + tt.pod.Labels[v1alpha1.PodGroupLabel]
			gang := gp.gangCache.getGangFromCache(gangId)
			if gang != nil {
				if tt.resourceSatisfied {
					gang.setResourceSatisfied()
				}
			}
			cycleState := framework.NewCycleState()
			var wg sync.WaitGroup
			//pod3 case should test waitingPods
			if tt.pod.Name == "pod3" {
				wg.Add(2)
			}
			for _, pod := range tt.pods {
				tmpPod := pod
				suit.Handle.(framework.Framework).RunPermitPlugins(context.Background(), cycleState, tmpPod, "")
				//start goroutine to wait for the waitingPod's status channel from PostFilter stage
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
						waitingGangId := getNamespaceSplicingName(waitingPod.GetPod().Namespace,
							getGangNameByPod(waitingPod.GetPod()))
						if waitingGangId == gangId {
							klog.Infof("waitingPod name is %v", waitingPod.GetPod().Name)
							totalWaitingPods++
							if waitingPod.GetPod().Name != "pod3-1" && waitingPod.GetPod().Name != "pod3-2" {
								t.Error()
							}
						}
					})
				if totalWaitingPods != 2 {
					t.Error()
				}
			}
			//reject waitingPods
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
	pg1 := makePG("gangA", "gangA_ns", 3, &gangACreatedTime, nil)
	// we created gangB by PodGroup,gangB has gangGroup [gangA,gangB]
	pg2 := makePG("gangB", "gangB_ns", 2, &gangACreatedTime, nil)
	pg2.Annotations = map[string]string{extension.AnnotationGangGroups: "[\"gangA_ns/gangA\",\"gangB_ns/gangB\"]"}
	tests := []struct {
		name                  string
		pod                   *corev1.Pod
		pods                  []*corev1.Pod
		want                  framework.Code
		toActivePodsKeys      []string
		needInWaitingPods     bool
		shouldTestTimeoutTime bool
	}{
		{
			name: "pod1 does not belong to any pg, allow",
			pod:  st.MakePod().Name("pod1").UID("pod1").Namespace("ns1").Obj(),
			want: framework.Success,
		},
		{
			name: "pod2 belongs to a non-existing pg",
			pod:  st.MakePod().Name("pod2").UID("pod2").Namespace("ns1").Label(v1alpha1.PodGroupLabel, "gangNonExist").Obj(),
			want: framework.Unschedulable,
		},
		{
			name: "pod3 belongs to gangA that doesn't have enough assumed pods",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			want:             framework.Wait,
			toActivePodsKeys: []string{"gangA_ns/pod3-1"},
		},
		{
			name: "pod4 belongs to gangA that gangA has resourceSatisfied",
			pod:  st.MakePod().Name("pod4").UID("pod4").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod4-1").UID("pod4-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod4-2").UID("pod4-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			needInWaitingPods: true,
			want:              framework.Success,
		},
		{
			name: "pod5 belongs to gangB that gangB has resourceSatisfied, but gangA has not satisfied",
			pod:  st.MakePod().Name("pod5").UID("pod5").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod5-1").UID("pod5-1").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
				st.MakePod().Name("pod5-2").UID("pod5-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			toActivePodsKeys: []string{"gangB_ns/pod5-1"},
			want:             framework.Wait,
		},
		{
			name: "pod6 belongs to gangB that gangB has resourceSatisfied, and gangA has satisfied too",
			pod:  st.MakePod().Name("pod6").UID("pod6").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod6-1").UID("pod6-1").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
				st.MakePod().Name("pod6-2").UID("pod6-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod6-3").UID("pod6-3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
				st.MakePod().Name("pod6-4").UID("pod6-4").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			needInWaitingPods: true,
			want:              framework.Success,
		},
		{
			name: "test timeoutStartTime",
			pod:  st.MakePod().Name("pod5").UID("pod5").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod5-1").UID("pod5-1").Namespace("gangB_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
				st.MakePod().Name("pod5-2").UID("pod5-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			toActivePodsKeys:      []string{"gangB_ns/pod5-1"},
			want:                  framework.Wait,
			shouldTestTimeoutTime: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit := newPluginTestSuit(t, nil)
			p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
			assert.NotNil(t, p)
			assert.Nil(t, err)
			suit.start()
			gp := p.(*GangPlugin)
			//create gang by podGroup
			suit.pgClient.SchedulingV1alpha1().PodGroups(pg1.Namespace).Create(context.TODO(), pg1, metav1.CreateOptions{})
			suit.pgClient.SchedulingV1alpha1().PodGroups(pg2.Namespace).Create(context.TODO(), pg2, metav1.CreateOptions{})
			time.Sleep(10 * time.Millisecond)

			//create  pods
			for _, pod := range tt.pods {
				suit.Handle.SharedInformerFactory().Core().V1().Pods().Informer().GetStore().Add(pod)
			}
			suit.Handle.SharedInformerFactory().Core().V1().Pods().Informer().GetStore().Add(tt.pod)

			time.Sleep(10 * time.Millisecond)
			//add assumed pods
			ctx := context.TODO()
			cycleState := framework.NewCycleState()
			gangId := tt.pod.Namespace + "/" + tt.pod.Labels[v1alpha1.PodGroupLabel]
			gang := gp.gangCache.getGangFromCache(gangId)

			podsToActivate := framework.NewPodsToActivate()
			cycleState.Write(framework.PodsToActivateKey, podsToActivate)

			waitNum := len(tt.pods)
			var wg sync.WaitGroup
			if tt.needInWaitingPods {
				wg.Add(waitNum)
			}
			for _, pod := range tt.pods {
				tmpPod := pod
				suit.Handle.(framework.Framework).RunPermitPlugins(context.Background(), cycleState, tmpPod, "")
				//start goroutine to wait for the waitingPod's Allow signal from Permit stage
				go func() {
					status := suit.Handle.(framework.Framework).WaitOnPermit(context.Background(), tmpPod)
					if !status.IsSuccess() {
						t.Error()
					}
					defer wg.Done()
				}()
			}

			if gang != nil {
				for _, pod := range tt.pods {
					gp.Reserve(ctx, cycleState, pod, "")
				}
			}
			gp.Reserve(ctx, cycleState, tt.pod, "")

			if tt.needInWaitingPods {
				totalWaitingPods := 0
				suit.Handle.IterateOverWaitingPods(
					func(waitingPod framework.WaitingPod) {
						waitingGangId := getNamespaceSplicingName(waitingPod.GetPod().Namespace,
							getGangNameByPod(waitingPod.GetPod()))
						if waitingGangId == "gangB_ns/gangB" || waitingGangId == "gangA_ns/gangA" {
							klog.Infof("waitingPod name is %v", waitingPod.GetPod().Name)
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
			if tt.shouldTestTimeoutTime {
				preTimeoutStartTime := gang.TimeoutStartTime
				gp.Permit(ctx, cycleState, tt.pods[0], "")
				if gang.TimeoutStartTime != preTimeoutStartTime || preTimeoutStartTime.IsZero() {
					t.Error()
				}
			}

		})
	}
}

func TestReserve(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)
	suit.start()
	gangACreatedTime := time.Now()
	// we created gangA by PodGroup
	pg1 := makePG("gangA", "gangA_ns", 1, &gangACreatedTime, nil)
	// pod1 normal
	pod1 := st.MakePod().Name("pod1").UID("pod1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj()
	// pod2's gang cannot find
	pod2 := st.MakePod().Name("pod2").UID("pod2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangC").Obj()
	// pod3 has no gang
	pod3 := st.MakePod().Name("pod3").UID("pod3").Namespace("gangQ_ns").Obj()
	gp := p.(*GangPlugin)

	gp.gangCache.onPodGroupAdd(pg1)
	gp.gangCache.onPodAdd(pod1)
	ctx := context.TODO()
	cycleState := framework.NewCycleState()

	// 1. add assumed pod
	result := gp.Reserve(ctx, cycleState, pod1, "")
	if result.Code() != framework.Success {
		t.Error()
	}
	gang := gp.gangCache.getGangFromCache(pod1.Namespace + "/" + pod1.Labels[v1alpha1.PodGroupLabel])
	gang.lock.Lock()
	if len(gang.WaitingForBindChildren) != 1 || gang.WaitingForBindChildren[getNamespaceSplicingName(pod1.Namespace, pod1.Name)] == nil || !gang.ResourceSatisfied {
		t.Error()
	}

	gang.lock.Unlock()
	// 2. pod without gang should be ignored here
	gp.gangCache.onPodGroupDelete(pg1)
	gp.gangCache.onPodGroupAdd(pg1)
	result = gp.Reserve(ctx, cycleState, pod2, "")
	if result.Code() != framework.UnschedulableAndUnresolvable {
		t.Error()
	}

	// 3. pod has no gang
	gp.gangCache.onPodGroupDelete(pg1)
	gp.gangCache.onPodGroupAdd(pg1)
	result = gp.Reserve(ctx, cycleState, pod3, "")
	if result.Code() != framework.Success {
		t.Error()
	}
}

func TestUnreserve(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)
	gangACreatedTime := time.Now()
	// we created gangA by PodGroup,
	pg1 := makePG("gangA", "gangA_ns", 2, &gangACreatedTime, nil)
	suit.pgClient.SchedulingV1alpha1().PodGroups("gangA_ns").Create(context.TODO(), pg1, metav1.CreateOptions{})
	time.Sleep(100 * time.Millisecond)

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
		{
			name: "pod1 comes to Unreserve without timeout",
			pod:  st.MakePod().Name("pod2").UID("pod2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod2-1").UID("pod2-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suit.start()
			_, status := suit.Handle.ClientSet().CoreV1().Pods("gangA_ns").Create(context.TODO(), tt.pod, metav1.CreateOptions{})
			assert.Nil(t, status)
			for _, pod := range tt.pods {
				_, status := suit.Handle.ClientSet().CoreV1().Pods("gangA_ns").Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.Nil(t, status)
			}
			gp := p.(*GangPlugin)
			ctx := context.TODO()
			cycleState := framework.NewCycleState()
			gangId := tt.pod.Namespace + "/" + tt.pod.Labels[v1alpha1.PodGroupLabel]
			gang := gp.gangCache.getGangFromCache(gangId)
			var wg sync.WaitGroup
			waitNum := len(tt.pods)

			if tt.timeout {
				wg.Add(waitNum)
				gang.setTimeoutStartTime(time.Unix(2, 0))
				for _, pod := range tt.pods {
					tmpPod := pod
					suit.Handle.(framework.Framework).RunPermitPlugins(context.Background(), cycleState, tmpPod, "")
					//start goroutine to wait for the waitingPod's Reject signal from Unreserve stage
					go func() {
						status := suit.Handle.(framework.Framework).WaitOnPermit(context.Background(), tmpPod)
						if status.IsSuccess() {
							t.Error()
						}
						defer wg.Done()
					}()
				}
				//assert waitingPods
				totalWaitingPods := 0
				suit.Handle.IterateOverWaitingPods(
					func(waitingPod framework.WaitingPod) {
						waitingGangId := getNamespaceSplicingName(waitingPod.GetPod().Namespace,
							getGangNameByPod(waitingPod.GetPod()))
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
			podModified1, status := suit.Handle.ClientSet().CoreV1().Pods("gangA_ns").Get(context.TODO(), tt.pod.Name, metav1.GetOptions{})
			assert.Nil(t, status)
			assert.NotNil(t, podModified1)
			var podModified2 *corev1.Pod
			for _, pod := range tt.pods {
				podModified2, status = suit.Handle.ClientSet().CoreV1().Pods("gangA_ns").Get(context.TODO(), pod.Name, metav1.GetOptions{})
				assert.Nil(t, status)
				assert.NotNil(t, podModified2)
			}

			anno1, anno2 := podModified1.Annotations[extension.AnnotationGangTimeout], podModified2.Annotations[extension.AnnotationGangTimeout]
			if tt.timeout {
				if anno1 != "true" {
					t.Errorf("anno1 wrong,expected true,got %v", anno1)
				}
				if anno2 != "true" {
					t.Errorf("anno2 wrong,expected true,got %v", anno2)
				}
				if !gang.TimeoutStartTime.IsZero() {
					t.Errorf("After gang timeout at Permit Stage,TimeoutStartTime should be zero,got:%v", gang.TimeoutStartTime)
				}
			} else {
				if anno1 != "" {
					t.Errorf("anno1 wrong,expected nothing,got %v", anno1)
				}
				if anno2 != "" {
					t.Errorf("anno2 wrong,expected nothing,got %v", anno2)
				}
				if !gang.TimeoutStartTime.IsZero() {
					t.Errorf("After gang timeout at Permit Stage,TimeoutStartTime should be zero,got:%v", gang.TimeoutStartTime)
				}
			}
		})
	}
}

func TestPostBind(t *testing.T) {
	suit := newPluginTestSuit(t, nil)
	p, err := suit.proxyNew(suit.gangSchedulingArgs, suit.Handle)
	assert.NotNil(t, p)
	assert.Nil(t, err)
	suit.start()
	gangACreatedTime := time.Now()
	// we created gangA by PodGroup
	pg1 := makePG("gangA", "gangA_ns", 1, &gangACreatedTime, nil)
	// pod1 normal
	pod1 := st.MakePod().Name("pod1").UID("pod1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj()

	gp := p.(*GangPlugin)

	gp.gangCache.onPodGroupAdd(pg1)
	gp.gangCache.onPodAdd(pod1)
	ctx := context.TODO()
	cycleState := framework.NewCycleState()

	gp.PostBind(ctx, cycleState, pod1, "")

	gang := gp.gangCache.getGangFromCache(pod1.Namespace + "/" + pod1.Labels[v1alpha1.PodGroupLabel])
	gang.lock.Lock()
	if len(gang.BoundChildren) != 1 || gang.BoundChildren[getNamespaceSplicingName(pod1.Namespace, pod1.Name)] == nil {
		t.Error()
	}

	gang.lock.Unlock()

}
