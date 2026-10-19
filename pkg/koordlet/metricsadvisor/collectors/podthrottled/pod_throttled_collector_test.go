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

package podthrottled

import (
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_podThrottledCollector_collectPodThrottledInfo(t *testing.T) {
	testContainerID := "containerd://testContainerUID"
	testPodMetaDir := "kubepods.slice/kubepods-podtest-pod-uid.slice"
	testPodParentDir := "/kubepods.slice/kubepods-podtest-pod-uid.slice"
	testContainerParentDir := "/kubepods.slice/kubepods-podtest-pod-uid.slice/cri-containerd-testContainerUID.scope"
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test",
			UID:       "test-pod-uid",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test-container",
					ContainerID: testContainerID,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
			},
		},
	}
	testFailedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-failed-pod",
			Namespace: "test",
			UID:       "test-failed-pod-uid",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "test-container",
					ContainerID: testContainerID,
				},
			},
		},
	}
	type fields struct {
		podFilterOption       framework.PodFilter
		getPodMetas           []*statesinformer.PodMeta
		initPodLastStat       func(lastState *gocache.Cache)
		initContainerLastStat func(lastState *gocache.Cache)
		SetSysUtil            func(helper *system.FileTestUtil)
	}
	type args struct {
		podMeta *statesinformer.PodMeta
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "cgroup v1 format",
			fields: fields{
				podFilterOption: framework.DefaultPodFilter,
				getPodMetas: []*statesinformer.PodMeta{
					{
						CgroupDir: testPodMetaDir,
						Pod:       testPod,
					},
				},
				initPodLastStat: func(lastState *gocache.Cache) {
					lastState.Set(string(testPod.UID), &system.CPUStatRaw{
						NrPeriods:            0,
						NrThrottled:          0,
						ThrottledNanoSeconds: 0,
					}, gocache.DefaultExpiration)
				},
				initContainerLastStat: func(lastState *gocache.Cache) {
					lastState.Set(testContainerID, &system.CPUStatRaw{
						NrPeriods:            0,
						NrThrottled:          0,
						ThrottledNanoSeconds: 0,
					}, gocache.DefaultExpiration)
				},
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.WriteCgroupFileContents(testPodParentDir, system.CPUStat, "nr_periods 100\nnr_throttled 50\nthrottled_time 100000\n")
					helper.WriteCgroupFileContents(testContainerParentDir, system.CPUStat, "nr_periods 100\nnr_throttled 50\nthrottled_time 100000\n")
				},
			},
			args: args{},
		},
		{
			name: "cgroup v1, filter terminated pods",
			fields: fields{
				podFilterOption: &framework.TerminatedPodFilter{},
				getPodMetas: []*statesinformer.PodMeta{
					{
						CgroupDir: testPodMetaDir,
						Pod:       testPod,
					},
					{
						Pod: testFailedPod,
					},
				},
				initPodLastStat: func(lastState *gocache.Cache) {
					lastState.Set(string(testPod.UID), &system.CPUStatRaw{
						NrPeriods:            0,
						NrThrottled:          0,
						ThrottledNanoSeconds: 0,
					}, gocache.DefaultExpiration)
				},
				initContainerLastStat: func(lastState *gocache.Cache) {
					lastState.Set(testContainerID, &system.CPUStatRaw{
						NrPeriods:            0,
						NrThrottled:          0,
						ThrottledNanoSeconds: 0,
					}, gocache.DefaultExpiration)
				},
				SetSysUtil: func(helper *system.FileTestUtil) {
					helper.WriteCgroupFileContents(testPodParentDir, system.CPUStat, "nr_periods 100\nnr_throttled 50\nthrottled_time 100000\n")
					helper.WriteCgroupFileContents(testContainerParentDir, system.CPUStat, "nr_periods 100\nnr_throttled 50\nthrottled_time 100000\n")
				},
			},
			args: args{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.SetSysUtil != nil {
				tt.fields.SetSysUtil(helper)
			}

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			metricCache, err := metriccache.NewMetricCache(&metriccache.Config{
				TSDBPath:              helper.TempDir,
				TSDBEnablePromMetrics: false,
			})
			assert.NoError(t, err)
			defer func() {
				metricCache.Close()
			}()
			statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
			statesInformer.EXPECT().HasSynced().Return(true).AnyTimes()
			statesInformer.EXPECT().GetAllPods().Return(tt.fields.getPodMetas).Times(1)

			collector := New(&framework.Options{
				Config: &framework.Config{
					CollectResUsedInterval: time.Second,
				},
				StatesInformer: statesInformer,
				MetricCache:    metricCache,
				CgroupReader:   resourceexecutor.NewCgroupReader(),
				PodFilters: map[string]framework.PodFilter{
					CollectorName: tt.fields.podFilterOption,
				},
			})
			c := collector.(*podThrottledCollector)
			tt.fields.initPodLastStat(c.lastPodCPUThrottled)
			tt.fields.initContainerLastStat(c.lastContainerCPUThrottled)
			assert.NotPanics(t, func() {
				c.collectPodThrottledInfo()
			})
		})
	}
}

func Test_podThrottledCollector_collectContainerThrottledInfo_initContainers(t *testing.T) {
	podDir := "kubepods.slice/kubepods-podtest-pod-uid.slice"
	containerIDs := map[string]string{
		"main":         "containerd://mainUID",
		"init-running": "containerd://initRunningUID",
		"init-done":    "containerd://initDoneUID",
	}
	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	terminated := corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test", UID: "test-pod-uid"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			InitContainerStatuses: []corev1.ContainerStatus{
				{Name: "init-done", ContainerID: containerIDs["init-done"], State: terminated},
				{Name: "init-running", ContainerID: containerIDs["init-running"], State: running},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "main", ContainerID: containerIDs["main"], State: running},
			},
		},
	}

	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	// the completed init container keeps a readable cgroup so the test proves it is skipped by state
	for _, uid := range []string{"mainUID", "initRunningUID", "initDoneUID"} {
		helper.WriteCgroupFileContents("/"+podDir+"/cri-containerd-"+uid+".scope", system.CPUStat,
			"nr_periods 100\nnr_throttled 50\nthrottled_time 100000\n")
	}

	c := New(&framework.Options{
		Config:       &framework.Config{CollectResUsedInterval: time.Second},
		CgroupReader: resourceexecutor.NewCgroupReader(),
	}).(*podThrottledCollector)
	for _, id := range containerIDs {
		c.lastContainerCPUThrottled.Set(id, &system.CPUStatRaw{}, gocache.DefaultExpiration)
	}

	samples := c.collectContainerThrottledInfo(&statesinformer.PodMeta{Pod: pod, CgroupDir: podDir})

	got := map[string]struct{}{}
	for _, s := range samples {
		assert.Equal(t, string(metriccache.ContainerMetricCPUThrottled), s.GetKind())
		got[s.GetProperties()[string(metriccache.MetricPropertyContainerID)]] = struct{}{}
	}
	assert.Equal(t, map[string]struct{}{
		containerIDs["main"]:         {},
		containerIDs["init-running"]: {},
	}, got)
}
