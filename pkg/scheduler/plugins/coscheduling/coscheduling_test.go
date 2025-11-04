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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	scheduledconfig "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
	st "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingv1alpha1 "github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	pgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned"
	fakepgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned/fake"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinatorinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config/v1beta3"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
	reservationutil "github.com/koordinator-sh/koordinator/pkg/util/reservation"
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

func makePg(name, namespace string, min int32, creationTime *time.Time, minResource corev1.ResourceList) *v1alpha1.PodGroup {
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

func (f *testSharedLister) StorageInfos() framework.StorageInfoLister {
	return f
}

func (f *testSharedLister) IsPVCUsedByPods(key string) bool {
	return false
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
	var v1beta3args v1beta3.CoschedulingArgs
	v1beta3.SetDefaults_CoschedulingArgs(&v1beta3args)
	var gangSchedulingArgs config.CoschedulingArgs
	err := v1beta3.Convert_v1beta3_CoschedulingArgs_To_config_CoschedulingArgs(&v1beta3args, &gangSchedulingArgs, nil)
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
		schedulertesting.RegisterPreFilterPlugin(Name, proxyNew),
		schedulertesting.RegisterReservePlugin(Name, proxyNew),
		schedulertesting.RegisterPermitPlugin(Name, proxyNew),
		schedulertesting.RegisterQueueSortPlugin(queuesort.Name, queuesort.New),
		schedulertesting.RegisterPluginAsExtensions(Name, proxyNew, "PostBind"),
		schedulertesting.RegisterPluginAsExtensions(Name, proxyNew, "PreEnqueue"),
	}
	fakeRecorder := record.NewFakeRecorder(1024)
	eventRecorder := record.NewEventRecorderAdapter(fakeRecorder)

	informerFactory := informers.NewSharedInformerFactory(cs, 0)
	snapshot := newTestSharedLister(nil, nodes)
	fh, err := schedulertesting.NewFramework(
		context.TODO(),
		registeredPlugins,
		"koord-scheduler",
		runtime.WithClientSet(cs),
		runtime.WithInformerFactory(informerFactory),
		runtime.WithSnapshotSharedLister(snapshot),
		runtime.WithEventRecorder(eventRecorder),
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

func NewPodInfo(t *testing.T, pod *corev1.Pod) *framework.PodInfo {
	podInfo, err := framework.NewPodInfo(pod)
	assert.NoError(t, err)
	return podInfo
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
			_, code := gp.pgMgr.AfterPostFilter(context.Background(), cycleState, tt.pod, suit.Handle, Name, nil)
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
			toActivePodsKeys: nil,
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
			toActivePodsKeys: nil,
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

func TestPreBind(t *testing.T) {
	gangCreatedTime := time.Now()

	tests := []struct {
		name                string
		pg                  *v1alpha1.PodGroup
		pod                 *corev1.Pod
		pods                []*corev1.Pod
		expectLabelsSet     bool
		expectGangGroupID   string
		expectMemberCount   string
		podHasExistingLabel bool
	}{
		{
			name: "gang satisfied, should set labels on pod",
			pg:   makePg("gangA", "gangA_ns", 2, &gangCreatedTime, nil),
			pod:  st.MakePod().Name("pod-1").UID("pod-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod-2").UID("pod-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			expectLabelsSet:   true,
			expectGangGroupID: "gangA_ns/gangA",
			expectMemberCount: "2",
		},
		{
			name:            "pod not in gang, should not set labels",
			pg:              makePg("gangB", "gangB_ns", 2, &gangCreatedTime, nil),
			pod:             st.MakePod().Name("pod-3").UID("pod-3").Namespace("ns1").Obj(),
			pods:            []*corev1.Pod{},
			expectLabelsSet: false,
		},
		{
			name: "pod has existing labels, should overwrite gang labels",
			pg:   makePg("gangC", "gangC_ns", 2, &gangCreatedTime, nil),
			pod: st.MakePod().Name("pod-4").UID("pod-4").Namespace("gangC_ns").
				Label(v1alpha1.PodGroupLabel, "gangC").
				Label("custom-label", "custom-value").
				Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod-5").UID("pod-5").Namespace("gangC_ns").Label(v1alpha1.PodGroupLabel, "gangC").Obj(),
			},
			expectLabelsSet:     true,
			expectGangGroupID:   "gangC_ns/gangC",
			expectMemberCount:   "2",
			podHasExistingLabel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgClientSet := fakepgclientset.NewSimpleClientset()
			cs := kubefake.NewSimpleClientset()

			if tt.pg != nil {
				_, err := pgClientSet.SchedulingV1alpha1().PodGroups(tt.pg.Namespace).Create(context.TODO(), tt.pg, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			_, err := cs.CoreV1().Pods(tt.pod.Namespace).Create(context.TODO(), tt.pod, metav1.CreateOptions{})
			assert.NoError(t, err)
			for _, pod := range tt.pods {
				_, err = cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			suit := newPluginTestSuit(t, nil, pgClientSet, cs)
			suit.start()
			gp := suit.plugin.(*Coscheduling)

			ctx := context.TODO()
			cycleState := framework.NewCycleState()

			// Make gang satisfied if needed
			if len(tt.pods) > 0 {
				// Let first pod enter Permit and be assumed
				status1, _ := gp.Permit(ctx, cycleState, tt.pod, "")
				assert.Equal(t, framework.Wait, status1.Code())

				// Let second pod enter Permit and satisfy the gang requirements
				for _, pod := range tt.pods {
					status2, _ := gp.Permit(ctx, cycleState, pod, "")
					assert.Equal(t, framework.Success, status2.Code())
				}
			}

			// Act: PreBind should set gang binding labels
			preBindStatus := gp.PreBind(ctx, cycleState, tt.pod, "node-1")
			assert.True(t, preBindStatus.IsSuccess())

			// Assert: check gang labels
			if tt.expectLabelsSet {
				assert.Equal(t, tt.expectGangGroupID, tt.pod.Labels[extension.LabelBindGangGroupId])
				assert.Equal(t, tt.expectMemberCount, tt.pod.Labels[extension.LabelBindGangMemberCount])
				if tt.podHasExistingLabel {
					assert.Equal(t, "custom-value", tt.pod.Labels["custom-label"])
				}
			} else {
				assert.NotContains(t, tt.pod.Labels, extension.LabelBindGangGroupId)
				assert.NotContains(t, tt.pod.Labels, extension.LabelBindGangMemberCount)
			}
		})
	}
}

func TestPreBindReservation(t *testing.T) {
	gangCreatedTime := time.Now()

	tests := []struct {
		name                string
		pg                  *v1alpha1.PodGroup
		reservation         *schedulingv1alpha1.Reservation
		pods                []*corev1.Pod
		expectLabelsSet     bool
		expectGangGroupID   string
		expectMemberCount   string
		reservationHasLabel bool
	}{
		{
			name: "reservation in gang satisfied, should set labels",
			pg:   makePg("gangA", "gangA_ns", 2, &gangCreatedTime, nil),
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "r-1",
					Namespace: "gangA_ns",
					UID:       "r-1-uid",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: "gangA",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "gangA_ns",
						},
					},
				},
			},
			pods: []*corev1.Pod{
				st.MakePod().Name("pod-1").UID("pod-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			expectLabelsSet:   true,
			expectGangGroupID: "gangA_ns/gangA",
			expectMemberCount: "2",
		},
		{
			name: "reservation not in gang, should not set labels",
			pg:   makePg("gangB", "gangB_ns", 2, &gangCreatedTime, nil),
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "r-2",
					Namespace: "ns1",
					UID:       "r-2-uid",
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "ns1",
						},
					},
				},
			},
			pods:            []*corev1.Pod{},
			expectLabelsSet: false,
		},
		{
			name: "reservation has existing labels, should add gang labels",
			pg:   makePg("gangC", "gangC_ns", 2, &gangCreatedTime, nil),
			reservation: &schedulingv1alpha1.Reservation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "r-3",
					Namespace: "gangC_ns",
					UID:       "r-3-uid",
					Labels: map[string]string{
						v1alpha1.PodGroupLabel: "gangC",
						"custom-label":         "custom-value",
					},
				},
				Spec: schedulingv1alpha1.ReservationSpec{
					Template: &corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "gangC_ns",
						},
					},
				},
			},
			pods: []*corev1.Pod{
				st.MakePod().Name("pod-2").UID("pod-2").Namespace("gangC_ns").Label(v1alpha1.PodGroupLabel, "gangC").Obj(),
			},
			expectLabelsSet:     true,
			expectGangGroupID:   "gangC_ns/gangC",
			expectMemberCount:   "2",
			reservationHasLabel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgClientSet := fakepgclientset.NewSimpleClientset()
			cs := kubefake.NewSimpleClientset()

			if tt.pg != nil {
				_, err := pgClientSet.SchedulingV1alpha1().PodGroups(tt.pg.Namespace).Create(context.TODO(), tt.pg, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			for _, pod := range tt.pods {
				_, err := cs.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
				assert.NoError(t, err)
			}

			suit := newPluginTestSuit(t, nil, pgClientSet, cs)
			suit.start()
			gp := suit.plugin.(*Coscheduling)

			ctx := context.TODO()
			cycleState := framework.NewCycleState()

			// Make gang satisfied if needed
			if len(tt.pods) > 0 {
				// Create a mock pod from reservation to trigger gang logic
				reservePod := reservationutil.NewReservePod(tt.reservation)
				cs.CoreV1().Pods(reservePod.Namespace).Create(context.TODO(), reservePod, metav1.CreateOptions{})

				// Let reserve pod enter Permit and be assumed
				status1, _ := gp.Permit(ctx, cycleState, reservePod, "")
				assert.Equal(t, framework.Wait, status1.Code())

				// Let other pods enter Permit to satisfy the gang
				for _, pod := range tt.pods {
					status2, _ := gp.Permit(ctx, cycleState, pod, "")
					assert.Equal(t, framework.Success, status2.Code())
				}
			}

			// Act: PreBindReservation should set gang binding labels
			preBindRStatus := gp.PreBindReservation(ctx, cycleState, tt.reservation, "node-1")
			assert.True(t, preBindRStatus.IsSuccess())

			// Assert: check gang labels on the Reservation
			if tt.expectLabelsSet {
				assert.NotNil(t, tt.reservation.Labels)
				assert.Equal(t, tt.expectGangGroupID, tt.reservation.Labels[extension.LabelBindGangGroupId])
				assert.Equal(t, tt.expectMemberCount, tt.reservation.Labels[extension.LabelBindGangMemberCount])
				if tt.reservationHasLabel {
					assert.Equal(t, "custom-value", tt.reservation.Labels["custom-label"])
				}
			} else {
				if tt.reservation.Labels != nil {
					assert.NotContains(t, tt.reservation.Labels, extension.LabelBindGangGroupId)
					assert.NotContains(t, tt.reservation.Labels, extension.LabelBindGangMemberCount)
				}
			}
		})
	}
}
