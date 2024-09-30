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

package core

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	st "k8s.io/kubernetes/pkg/scheduler/testing"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	fakepgclientset "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/clientset/versioned/fake"
	pgformers "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/informers/externalversions"
	pginformer "github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/generated/informers/externalversions/scheduling/v1alpha1"
	koordfake "github.com/koordinator-sh/koordinator/pkg/client/clientset/versioned/fake"
	koordinformers "github.com/koordinator-sh/koordinator/pkg/client/informers/externalversions"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling/util"
)

type Mgr struct {
	pgMgr      *PodGroupManager
	pgInformer pginformer.PodGroupInformer
}

func NewManagerForTest() *Mgr {
	pgClient := fakepgclientset.NewSimpleClientset()
	pgInformerFactory := pgformers.NewSharedInformerFactory(pgClient, 0)
	pgInformer := pgInformerFactory.Scheduling().V1alpha1().PodGroups()

	podClient := clientsetfake.NewSimpleClientset()
	informerFactory := informers.NewSharedInformerFactory(podClient, 0)

	koordClient := koordfake.NewSimpleClientset()
	koordInformerFactory := koordinformers.NewSharedInformerFactory(koordClient, 0)

	args := &config.CoschedulingArgs{DefaultTimeout: metav1.Duration{Duration: 300 * time.Second}}

	pgManager := NewPodGroupManager(args, pgClient, pgInformerFactory, informerFactory, koordInformerFactory)
	return &Mgr{
		pgMgr:      pgManager,
		pgInformer: pgInformer,
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

func TestPlugin_PreFilter_ResetScheduleTime(t *testing.T) {
	mgr := NewManagerForTest().pgMgr

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "pod1",
			Annotations: map[string]string{
				extension.AnnotationGangName:   "gangB",
				extension.AnnotationGangMinNum: "2",
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "pod2",
			Annotations: map[string]string{
				extension.AnnotationGangName:   "gangB",
				extension.AnnotationGangMinNum: "2",
			},
		},
	}
	mgr.OnPodAdd(pod1)
	mgr.OnPodAdd(pod2)

	gang := mgr.GetGangByPod(pod1)
	lastScheduleTime1 := gang.GangGroupInfo.LastScheduleTime
	assert.Equal(t, 2, len(gang.GangGroupInfo.ChildrenLastScheduleTime))
	assert.Equal(t, lastScheduleTime1, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod1"])
	assert.Equal(t, lastScheduleTime1, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod2"])

	mgr.PreFilter(context.TODO(), framework.NewCycleState(), pod1)
	lastScheduleTime2 := gang.GangGroupInfo.LastScheduleTime
	assert.Equal(t, 2, len(gang.GangGroupInfo.ChildrenLastScheduleTime))
	assert.Equal(t, lastScheduleTime2, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod1"])
	assert.Equal(t, lastScheduleTime1, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod2"])

	mgr.PreFilter(context.TODO(), framework.NewCycleState(), pod1)
	lastScheduleTime2 = gang.GangGroupInfo.LastScheduleTime
	assert.Equal(t, 2, len(gang.GangGroupInfo.ChildrenLastScheduleTime))
	assert.Equal(t, lastScheduleTime2, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod1"])
	assert.Equal(t, lastScheduleTime1, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod2"])

	mgr.PreFilter(context.TODO(), framework.NewCycleState(), pod2)
	lastScheduleTime2 = gang.GangGroupInfo.LastScheduleTime
	assert.Equal(t, 2, len(gang.GangGroupInfo.ChildrenLastScheduleTime))
	assert.Equal(t, lastScheduleTime2, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod1"])
	assert.Equal(t, lastScheduleTime2, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod2"])

	mgr.PreFilter(context.TODO(), framework.NewCycleState(), pod2)
	lastScheduleTime3 := gang.GangGroupInfo.LastScheduleTime
	assert.Equal(t, 2, len(gang.GangGroupInfo.ChildrenLastScheduleTime))
	assert.Equal(t, lastScheduleTime2, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod1"])
	assert.Equal(t, lastScheduleTime3, gang.GangGroupInfo.ChildrenLastScheduleTime["default/pod2"])
}

func TestPlugin_PreEnqueue(t *testing.T) {
	gangACreatedTime := time.Now()
	mgr := NewManagerForTest().pgMgr
	tests := []struct {
		name string
		// test pod
		pod *corev1.Pod
		// neighbor pods, make the condition ready for the test pod
		pods []*corev1.Pod
		pgs  *v1alpha1.PodGroup
		// assert value
		// expectedErrorMessage is "" represents that error is nil
		expectedErrorMessage       string
		expectedChildCycleMap      map[string]int
		expectedScheduleCycle      int
		expectedScheduleCycleValid bool
		expectStateData            *stateData
		// case value
		// next two are set before pg created
		totalNum          int
		isNonStrictMode   bool
		resourceSatisfied bool
		// next tow are set before test pod run
		shouldSetValidToFalse         bool
		shouldSetCycleEqualWithGlobal bool
		shouldSkipCheckScheduleCycle  bool
	}{
		{
			name:                 "pod does not belong to any gang",
			pod:                  st.MakePod().Name("pod1").UID("pod1").Namespace("ns1").Obj(),
			pods:                 []*corev1.Pod{},
			expectedErrorMessage: "",
		},
		{
			name:                 "pod belongs to a non-existing pg",
			pod:                  st.MakePod().Name("pod2").UID("pod2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "wenshiqi222").Obj(),
			expectedErrorMessage: "gang has not init, gangName: gangA_ns/wenshiqi222, podName: gangA_ns/pod2",
			expectedChildCycleMap: map[string]int{
				"gangA_ns/pod2": 1,
			},
			expectedScheduleCycleValid: true,
			expectedScheduleCycle:      1,
			expectStateData: &stateData{
				skipSetCycleInvalid: true,
			},
		},
		{
			name:                       "gang ResourceSatisfied",
			pod:                        st.MakePod().Name("podq").UID("podq").Namespace("gangq_ns").Label(v1alpha1.PodGroupLabel, "gangq").Obj(),
			expectedChildCycleMap:      map[string]int{},
			pgs:                        makePg("gangq", "gangq_ns", 4, &gangACreatedTime, nil),
			expectedScheduleCycleValid: true,
			expectedScheduleCycle:      1,
			resourceSatisfied:          true,
			expectStateData:            &stateData{},
		},
		{
			name: "pod count less than minMember",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			},
			pgs:                        makePg("ganga", "ganga_ns", 4, &gangACreatedTime, nil),
			expectedErrorMessage:       "gang child pod not collect enough, gangName: ganga_ns/ganga, podName: ganga_ns/pod3",
			expectedScheduleCycle:      1,
			expectedChildCycleMap:      map[string]int{},
			expectedScheduleCycleValid: true,
			expectStateData: &stateData{
				skipSetCycleInvalid: true,
			},
		},
		{
			name: "pods count equal with minMember,but is NonStrictMode",
			pod:  st.MakePod().Name("pod5").UID("pod5").Namespace("gangb_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod5-1").UID("pod5-1").Namespace("gangb_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
				st.MakePod().Name("pod5-2").UID("pod5-2").Namespace("gangb_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
				st.MakePod().Name("pod5-3").UID("pod5-3").Namespace("gangb_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
			},
			pgs:                  makePg("gangb", "gangb_ns", 4, &gangACreatedTime, nil),
			expectedErrorMessage: "",
			isNonStrictMode:      true,
			expectStateData:      &stateData{},
		},
		{
			name: "due to reschedule pod6's podScheduleCycle is equal with the gangScheduleCycle, but pod6's nominatedNodeName is not empty",
			pod: st.MakePod().Name("pod6").UID("pod6").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").
				NominatedNodeName("N1").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod6-1").UID("pod6-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
				st.MakePod().Name("pod6-2").UID("pod6-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
				st.MakePod().Name("pod6-3").UID("pod6-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
			},
			pgs:                           makePg("gangc", "ganga_ns", 4, &gangACreatedTime, nil),
			shouldSetCycleEqualWithGlobal: true,
			totalNum:                      5,
			expectedScheduleCycle:         1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod6":   1,
				"ganga_ns/pod6-1": 1,
				"ganga_ns/pod6-2": 1,
				"ganga_ns/pod6-3": 1,
			},
			expectedErrorMessage:       "",
			expectedScheduleCycleValid: true,
			expectStateData:            &stateData{},
		},
		{
			name: "pods count equal with minMember,is StrictMode, disable check scheduleCycle even if the gang's scheduleCycle is not valid",
			pod:  st.MakePod().Name("pod7").UID("pod7").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod7-1").UID("pod7-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
				st.MakePod().Name("pod7-2").UID("pod7-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
				st.MakePod().Name("pod7-3").UID("pod7-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
			},
			pgs:                   makePg("gangd", "ganga_ns", 4, &gangACreatedTime, nil),
			expectedScheduleCycle: 1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod7": 1,
			},
			expectedScheduleCycleValid:   false,
			expectedErrorMessage:         "",
			shouldSetValidToFalse:        true,
			shouldSkipCheckScheduleCycle: true,
			expectStateData:              &stateData{},
		},
		{
			name: "pods count equal with minMember,is StrictMode,scheduleCycle valid,but childrenNum is not reach to total num",
			pod:  st.MakePod().Name("pod8").UID("pod8").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gange").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod8-1").UID("pod8-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gange").Obj(),
				st.MakePod().Name("pod8-2").UID("pod8-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gange").Obj(),
				st.MakePod().Name("pod8-3").UID("pod8-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gange").Obj(),
			},
			pgs:                   makePg("gange", "ganga_ns", 4, &gangACreatedTime, nil),
			totalNum:              5,
			expectedScheduleCycle: 1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod8": 1,
			},
			expectedScheduleCycleValid: true,
			expectedErrorMessage:       "",
			expectStateData:            &stateData{},
		},
		{
			name: "pods count more than minMember,is StrictMode,scheduleCycle valid,and childrenNum reach to total num",
			pod:  st.MakePod().Name("pod9").UID("pod9").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod9-1").UID("pod9-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
				st.MakePod().Name("pod9-2").UID("pod9-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
				st.MakePod().Name("pod9-3").UID("pod9-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
				st.MakePod().Name("pod9-4").UID("pod9-4").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			},
			totalNum:              5,
			expectedScheduleCycle: 1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod9":   1,
				"ganga_ns/pod9-1": 1,
				"ganga_ns/pod9-2": 1,
				"ganga_ns/pod9-3": 1,
				"ganga_ns/pod9-4": 1,
			},
			expectedErrorMessage:       "",
			expectedScheduleCycleValid: true,
			expectStateData:            &stateData{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gang *Gang
			// first create the podGroup
			if tt.pgs != nil {
				if tt.pgs.Annotations == nil {
					tt.pgs.Annotations = map[string]string{}
				}
				if tt.totalNum != 0 {
					totalNumStr := strconv.Itoa(tt.totalNum)
					tt.pgs.Annotations[extension.AnnotationGangTotalNum] = totalNumStr
				}
				if tt.isNonStrictMode {
					tt.pgs.Annotations[extension.AnnotationGangMode] = extension.GangModeNonStrict
				}
				mgr.cache.onPodGroupAdd(tt.pgs)
				gang = mgr.cache.getGangFromCacheByGangId(util.GetId(tt.pgs.Namespace, tt.pgs.Name), false)
			}
			ctx := context.TODO()

			// add each neighbor pods and run preFilter
			for _, pod := range tt.pods {
				mgr.cache.onPodAdd(pod)
				mgr.PreEnqueue(ctx, pod)

			}
			mgr.cache.onPodAdd(tt.pod)

			// set pre cases before test pod run
			if tt.shouldSetValidToFalse {
				gang.setScheduleCycleInvalid()
			}
			if tt.shouldSetCycleEqualWithGlobal {
				gang.setChildScheduleCycle(tt.pod, 1)
			}
			if tt.resourceSatisfied {
				gang.setResourceSatisfied()
			}
			if tt.shouldSkipCheckScheduleCycle {
				mgr.args.SkipCheckScheduleCycle = true
				defer func() {
					mgr.args.SkipCheckScheduleCycle = false
				}()
			}
			// run the case
			// cycleState := framework.NewCycleState()
			// err := mgr.PreFilter(ctx, cycleState, tt.pod)
			err := mgr.PreEnqueue(ctx, tt.pod)
			var returnMessage string
			if err == nil {
				returnMessage = ""
			} else {
				returnMessage = err.Error()
			}

			assert.Equal(t, tt.expectedErrorMessage, returnMessage)
		})
	}
}

func TestPlugin_PreFilter(t *testing.T) {
	gangACreatedTime := time.Now()
	mgr := NewManagerForTest().pgMgr
	tests := []struct {
		name string
		// test pod
		pod *corev1.Pod
		// neighbor pods, make the condition ready for the test pod
		pods []*corev1.Pod
		pgs  *v1alpha1.PodGroup
		// assert value
		// expectedErrorMessage is "" represents that error is nil
		expectedErrorMessage       string
		expectedChildCycleMap      map[string]int
		expectedScheduleCycle      int
		expectedScheduleCycleValid bool
		expectStateData            *stateData
		// case value
		// next two are set before pg created
		totalNum          int
		isNonStrictMode   bool
		resourceSatisfied bool
		// next tow are set before test pod run
		shouldSetValidToFalse         bool
		shouldSetCycleEqualWithGlobal bool
		shouldSkipCheckScheduleCycle  bool
	}{
		{
			name:                 "pod does not belong to any gang",
			pod:                  st.MakePod().Name("pod1").UID("pod1").Namespace("ns1").Obj(),
			pods:                 []*corev1.Pod{},
			expectedErrorMessage: "",
		},
		{
			name:                 "pod belongs to a non-existing pg",
			pod:                  st.MakePod().Name("pod2").UID("pod2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "wenshiqi222").Obj(),
			expectedErrorMessage: "gang has not init, gangName: gangA_ns/wenshiqi222, podName: gangA_ns/pod2",
			expectedChildCycleMap: map[string]int{
				"gangA_ns/pod2": 1,
			},
			expectedScheduleCycleValid: true,
			expectedScheduleCycle:      1,
			expectStateData: &stateData{
				skipSetCycleInvalid: true,
			},
		},
		{
			name:                       "gang ResourceSatisfied",
			pod:                        st.MakePod().Name("podq").UID("podq").Namespace("gangq_ns").Label(v1alpha1.PodGroupLabel, "gangq").Obj(),
			expectedChildCycleMap:      map[string]int{},
			pgs:                        makePg("gangq", "gangq_ns", 4, &gangACreatedTime, nil),
			expectedScheduleCycleValid: true,
			expectedScheduleCycle:      1,
			resourceSatisfied:          true,
			expectStateData:            &stateData{},
		},
		{
			name: "pod count less than minMember",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			},
			pgs:                        makePg("ganga", "ganga_ns", 4, &gangACreatedTime, nil),
			expectedErrorMessage:       "gang child pod not collect enough, gangName: ganga_ns/ganga, podName: ganga_ns/pod3",
			expectedScheduleCycle:      1,
			expectedChildCycleMap:      map[string]int{},
			expectedScheduleCycleValid: true,
			expectStateData: &stateData{
				skipSetCycleInvalid: true,
			},
		},
		{
			name: "pods count equal with minMember,but is NonStrictMode",
			pod:  st.MakePod().Name("pod5").UID("pod5").Namespace("gangb_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod5-1").UID("pod5-1").Namespace("gangb_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
				st.MakePod().Name("pod5-2").UID("pod5-2").Namespace("gangb_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
				st.MakePod().Name("pod5-3").UID("pod5-3").Namespace("gangb_ns").Label(v1alpha1.PodGroupLabel, "gangb").Obj(),
			},
			pgs:                  makePg("gangb", "gangb_ns", 4, &gangACreatedTime, nil),
			expectedErrorMessage: "",
			isNonStrictMode:      true,
			expectStateData:      &stateData{},
		},
		{
			name: "due to reschedule pod6's podScheduleCycle is equal with the gangScheduleCycle",
			pod:  st.MakePod().Name("pod6").UID("pod6").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod6-1").UID("pod6-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
				st.MakePod().Name("pod6-2").UID("pod6-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
				st.MakePod().Name("pod6-3").UID("pod6-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
			},
			pgs:                           makePg("gangc", "ganga_ns", 4, &gangACreatedTime, nil),
			shouldSetCycleEqualWithGlobal: true,
			totalNum:                      5,
			expectedScheduleCycle:         1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod6": 1,
			},
			expectedErrorMessage:       "pod's schedule cycle too large, gangName: ganga_ns/gangc, podName: ganga_ns/pod6, podCycle: 1, gangCycle: 1",
			expectedScheduleCycleValid: true,
			expectStateData:            &stateData{},
		},
		{
			name: "due to reschedule pod6's podScheduleCycle is equal with the gangScheduleCycle, but pod6's nominatedNodeName is not empty",
			pod: st.MakePod().Name("pod6").UID("pod6").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").
				NominatedNodeName("N1").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod6-1").UID("pod6-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
				st.MakePod().Name("pod6-2").UID("pod6-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
				st.MakePod().Name("pod6-3").UID("pod6-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangc").Obj(),
			},
			pgs:                           makePg("gangc", "ganga_ns", 4, &gangACreatedTime, nil),
			shouldSetCycleEqualWithGlobal: true,
			totalNum:                      5,
			expectedScheduleCycle:         1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod6":   1,
				"ganga_ns/pod6-1": 1,
				"ganga_ns/pod6-2": 1,
				"ganga_ns/pod6-3": 1,
			},
			expectedErrorMessage:       "",
			expectedScheduleCycleValid: true,
			expectStateData:            &stateData{},
		},
		{
			name: "pods count equal with minMember,is StrictMode,but the gang's scheduleCycle is not valid due to pre pod Filter Failed",
			pod:  st.MakePod().Name("pod7").UID("pod7").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod7-1").UID("pod7-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
				st.MakePod().Name("pod7-2").UID("pod7-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
				st.MakePod().Name("pod7-3").UID("pod7-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
			},
			pgs:                   makePg("gangd", "ganga_ns", 4, &gangACreatedTime, nil),
			expectedScheduleCycle: 1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod7": 1,
			},
			expectedScheduleCycleValid: false,
			expectedErrorMessage:       "gang scheduleCycle not valid, gangName: ganga_ns/gangd, podName: ganga_ns/pod7",
			shouldSetValidToFalse:      true,
			expectStateData: &stateData{
				skipReject: true,
			},
		},
		{
			name: "pods count equal with minMember,is StrictMode, disable check scheduleCycle even if the gang's scheduleCycle is not valid",
			pod:  st.MakePod().Name("pod7").UID("pod7").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod7-1").UID("pod7-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
				st.MakePod().Name("pod7-2").UID("pod7-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
				st.MakePod().Name("pod7-3").UID("pod7-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gangd").Obj(),
			},
			pgs:                   makePg("gangd", "ganga_ns", 4, &gangACreatedTime, nil),
			expectedScheduleCycle: 1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod7": 1,
			},
			expectedScheduleCycleValid:   false,
			expectedErrorMessage:         "",
			shouldSetValidToFalse:        true,
			shouldSkipCheckScheduleCycle: true,
			expectStateData:              &stateData{},
		},
		{
			name: "pods count equal with minMember,is StrictMode,scheduleCycle valid,but childrenNum is not reach to total num",
			pod:  st.MakePod().Name("pod8").UID("pod8").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gange").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod8-1").UID("pod8-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gange").Obj(),
				st.MakePod().Name("pod8-2").UID("pod8-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gange").Obj(),
				st.MakePod().Name("pod8-3").UID("pod8-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "gange").Obj(),
			},
			pgs:                   makePg("gange", "ganga_ns", 4, &gangACreatedTime, nil),
			totalNum:              5,
			expectedScheduleCycle: 1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod8": 1,
			},
			expectedScheduleCycleValid: true,
			expectedErrorMessage:       "",
			expectStateData:            &stateData{},
		},
		{
			name: "pods count more than minMember,is StrictMode,scheduleCycle valid,and childrenNum reach to total num",
			pod:  st.MakePod().Name("pod9").UID("pod9").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod9-1").UID("pod9-1").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
				st.MakePod().Name("pod9-2").UID("pod9-2").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
				st.MakePod().Name("pod9-3").UID("pod9-3").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
				st.MakePod().Name("pod9-4").UID("pod9-4").Namespace("ganga_ns").Label(v1alpha1.PodGroupLabel, "ganga").Obj(),
			},
			totalNum:              5,
			expectedScheduleCycle: 1,
			expectedChildCycleMap: map[string]int{
				"ganga_ns/pod9":   1,
				"ganga_ns/pod9-1": 1,
				"ganga_ns/pod9-2": 1,
				"ganga_ns/pod9-3": 1,
				"ganga_ns/pod9-4": 1,
			},
			expectedErrorMessage:       "",
			expectedScheduleCycleValid: true,
			expectStateData:            &stateData{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gang *Gang
			// first create the podGroup
			if tt.pgs != nil {
				if tt.pgs.Annotations == nil {
					tt.pgs.Annotations = map[string]string{}
				}
				if tt.totalNum != 0 {
					totalNumStr := strconv.Itoa(tt.totalNum)
					tt.pgs.Annotations[extension.AnnotationGangTotalNum] = totalNumStr
				}
				if tt.isNonStrictMode {
					tt.pgs.Annotations[extension.AnnotationGangMode] = extension.GangModeNonStrict
				}
				mgr.cache.onPodGroupAdd(tt.pgs)
				gang = mgr.cache.getGangFromCacheByGangId(util.GetId(tt.pgs.Namespace, tt.pgs.Name), false)
			}
			ctx := context.TODO()

			// add each neighbor pods and run preFilter
			for _, pod := range tt.pods {
				mgr.cache.onPodAdd(pod)
				mgr.PreFilter(ctx, framework.NewCycleState(), pod)
			}
			mgr.cache.onPodAdd(tt.pod)

			// set pre cases before test pod run
			if tt.shouldSetValidToFalse {
				gang.setScheduleCycleInvalid()
			}
			if tt.shouldSetCycleEqualWithGlobal {
				gang.setChildScheduleCycle(tt.pod, 1)
			}
			if tt.resourceSatisfied {
				gang.setResourceSatisfied()
			}
			if tt.shouldSkipCheckScheduleCycle {
				mgr.args.SkipCheckScheduleCycle = true
				defer func() {
					mgr.args.SkipCheckScheduleCycle = false
				}()
			}
			// run the case
			cycleState := framework.NewCycleState()
			err := mgr.PreFilter(ctx, cycleState, tt.pod)
			var returnMessage string
			if err == nil {
				returnMessage = ""
			} else {
				returnMessage = err.Error()
			}
			preFilterState := getPreFilterState(stateKey, cycleState)
			assert.Equal(t, tt.expectStateData, preFilterState)
			// assert
			assert.Equal(t, tt.expectedErrorMessage, returnMessage)
			if gang != nil && !tt.isNonStrictMode && !tt.shouldSkipCheckScheduleCycle {
				assert.Equal(t, tt.expectedScheduleCycle, gang.getScheduleCycle())
				assert.Equal(t, tt.expectedScheduleCycleValid, gang.isScheduleCycleValid())
				assert.Equal(t, tt.expectedChildCycleMap, gang.GangGroupInfo.ChildrenScheduleRoundMap)

				assert.Equal(t, tt.expectedChildCycleMap[util.GetId(tt.pod.Namespace, tt.pod.Name)],
					mgr.GetChildScheduleCycle(tt.pod))
			}
		})
	}
}

// PostFilter logic test in Coscheduling_test, because without the plugin and framework,we cannot assert the waitingPods

func TestPermit(t *testing.T) {

	gangACreatedTime := time.Now()
	tests := []struct {
		name          string
		pod           *corev1.Pod
		pgs           []*v1alpha1.PodGroup
		pods          []*corev1.Pod
		runningPods   []*corev1.Pod
		wantStatus    Status
		wantWaittime  time.Duration
		needGangGroup bool
		onceSatisfy   bool
		matchPolicy   string
		groupInfo     string
	}{
		{
			name:         "pod1 does not belong to any pg, allow",
			pod:          st.MakePod().Name("pod1").UID("pod1").Namespace("ns1").Obj(),
			wantStatus:   PodGroupNotSpecified,
			wantWaittime: 0,
		},
		{
			name:         "pod2 belongs to a non-existing pg",
			pod:          st.MakePod().Name("pod2").UID("pod2").Namespace("ns1").Label(v1alpha1.PodGroupLabel, "gangnonexist").Obj(),
			wantStatus:   Wait,
			wantWaittime: 0,
		},
		{
			name: "pod3 belongs to gangA that doesn't have enough assumed pods",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			pgs:          []*v1alpha1.PodGroup{makePg("gangA", "gangA_ns", 3, &gangACreatedTime, nil)},
			wantStatus:   Wait,
			wantWaittime: 10 * time.Second,
		},
		{
			name: "pod3 belongs to gangA that doesn't have enough assumed pods, but once satisfied",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			pgs:          []*v1alpha1.PodGroup{makePg("gangA", "gangA_ns", 3, &gangACreatedTime, nil)},
			onceSatisfy:  true,
			wantStatus:   Success,
			wantWaittime: 0,
		},
		{
			name: "pod3 belongs to gangA that doesn't have enough assumed pods, once satisfied, but matchPolicy not once satisfied",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			pgs:          []*v1alpha1.PodGroup{makePg("gangA", "gangA_ns", 3, &gangACreatedTime, nil)},
			onceSatisfy:  true,
			matchPolicy:  extension.GangMatchPolicyOnlyWaiting,
			wantStatus:   Wait,
			wantWaittime: 10 * time.Second,
		},
		{
			name: "pod3 belongs to gangA that doesn't have enough assumed pods, but with Running pods is enough, matchPolicy waiting-and-running",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			runningPods: []*corev1.Pod{
				st.MakePod().Name("pod3-2").UID("pod3-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Node("n1").Obj(),
			},
			pgs:          []*v1alpha1.PodGroup{makePg("gangA", "gangA_ns", 3, &gangACreatedTime, nil)},
			onceSatisfy:  true,
			matchPolicy:  extension.GangMatchPolicyOnlyWaiting,
			wantStatus:   Wait,
			wantWaittime: 10 * time.Second,
		},
		{
			name: "pod3 belongs to gangA that doesn't have enough assumed pods, but with Running pods is enough, matchPolicy waiting-and-running",
			pod:  st.MakePod().Name("pod3").UID("pod3").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod3-1").UID("pod3-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Obj(),
			},
			runningPods: []*corev1.Pod{
				st.MakePod().Name("pod3-2").UID("pod3-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangA").Node("n1").Obj(),
			},
			pgs:          []*v1alpha1.PodGroup{makePg("gangA", "gangA_ns", 3, &gangACreatedTime, nil)},
			onceSatisfy:  true,
			matchPolicy:  extension.GangMatchPolicyWaitingAndRunning,
			wantStatus:   Success,
			wantWaittime: 0,
		},
		{
			name: "pod4 belongs to gangB that gangA has resourceSatisfied",
			pod:  st.MakePod().Name("pod4").UID("pod4").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod4-1").UID("pod4-1").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
				st.MakePod().Name("pod4-2").UID("pod4-2").Namespace("gangA_ns").Label(v1alpha1.PodGroupLabel, "gangB").Obj(),
			},
			pgs: []*v1alpha1.PodGroup{makePg("gangB", "gangA_ns", 3, &gangACreatedTime, nil)},

			wantStatus:   Success,
			wantWaittime: 0,
		},
		{
			name: "pod5 belongs to gangC that gangC has resourceSatisfied, but gangD has not satisfied",
			pod:  st.MakePod().Name("pod5").UID("pod5").Namespace("gangC_ns").Label(v1alpha1.PodGroupLabel, "gangC").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod5-1").UID("pod5-1").Namespace("gangC_ns").Label(v1alpha1.PodGroupLabel, "gangC").Obj(),
				st.MakePod().Name("pod5-2").UID("pod5-2").Namespace("gangD_ns").Label(v1alpha1.PodGroupLabel, "gangD").Obj(),
			},
			needGangGroup: true,
			groupInfo:     "[\"gangC_ns/gangC\",\"gangD_ns/gangD\"]",
			pgs: []*v1alpha1.PodGroup{
				makePg("gangC", "gangC_ns", 2, &gangACreatedTime, nil),
				makePg("gangD", "gangD_ns", 2, &gangACreatedTime, nil),
			},
			wantStatus:   Wait,
			wantWaittime: 10 * time.Second,
		},
		{
			name: "pod6 belongs to gangE that gangE has resourceSatisfied, and gangF has satisfied too",
			pod:  st.MakePod().Name("pod6").UID("pod6").Namespace("gangE_ns").Label(v1alpha1.PodGroupLabel, "gangE").Obj(),
			pods: []*corev1.Pod{
				st.MakePod().Name("pod6-1").UID("pod6-1").Namespace("gangE_ns").Label(v1alpha1.PodGroupLabel, "gangE").Obj(),
				st.MakePod().Name("pod6-2").UID("pod6-2").Namespace("gangF_ns").Label(v1alpha1.PodGroupLabel, "gangF").Obj(),
				st.MakePod().Name("pod6-3").UID("pod6-3").Namespace("gangF_ns").Label(v1alpha1.PodGroupLabel, "gangF").Obj(),
				st.MakePod().Name("pod6-4").UID("pod6-4").Namespace("gangF_ns").Label(v1alpha1.PodGroupLabel, "gangF").Obj(),
			},
			pgs: []*v1alpha1.PodGroup{
				makePg("gangE", "gangE_ns", 2, &gangACreatedTime, nil),
				makePg("gangF", "gangF_ns", 3, &gangACreatedTime, nil),
			},
			needGangGroup: true,
			groupInfo:     "[\"gangE_ns/gangE\",\"gangF_ns/gangF\"]",
			wantStatus:    Success,
			wantWaittime:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewManagerForTest().pgMgr
			// pg create
			for _, pg := range tt.pgs {
				if tt.needGangGroup {
					if pg.Annotations == nil {
						pg.Annotations = map[string]string{}
					}
					pg.Annotations[extension.AnnotationGangGroups] = tt.groupInfo
				}
				mgr.cache.onPodGroupAdd(pg)
				gangId := util.GetId(pg.Namespace, pg.Name)
				gang := mgr.cache.getGangFromCacheByGangId(gangId, false)
				gang.lock.Lock()
				gang.GangGroupInfo.OnceResourceSatisfied = tt.onceSatisfy
				gang.GangMatchPolicy = tt.matchPolicy
				gang.lock.Unlock()
			}
			ctx := context.TODO()
			// create  pods
			for _, pod := range tt.pods {
				mgr.cache.onPodAdd(pod)
				mgr.Permit(ctx, pod)
			}
			for _, pod := range tt.runningPods {
				mgr.cache.onPodAdd(pod)
				mgr.PostBind(ctx, pod, "tmp")
			}
			if len(tt.runningPods) != 0 {
				assert.Equal(t, int32(len(tt.runningPods)), mgr.GetBoundPodNumber(util.GetId(tt.runningPods[0].Namespace, util.GetGangNameByPod(tt.runningPods[0]))))
			}
			mgr.cache.onPodAdd(tt.pod)
			timeout, status := mgr.Permit(ctx, tt.pod)
			assert.Equal(t, tt.wantWaittime, timeout)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}
