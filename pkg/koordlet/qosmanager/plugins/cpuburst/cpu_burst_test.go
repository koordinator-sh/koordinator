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

package cpuburst

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	maframework "github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
	"github.com/koordinator-sh/koordinator/pkg/util/cache"
	"github.com/koordinator-sh/koordinator/pkg/util/sloconfig"
)

func newTestExecutor() resourceexecutor.ResourceUpdateExecutor {
	return &resourceexecutor.ResourceUpdateExecutorImpl{
		Config:        resourceexecutor.NewDefaultConfig(),
		ResourceCache: cache.NewCacheDefault(),
	}
}

func newTestCPUBurst(opt *framework.Options) *cpuBurst {
	return &cpuBurst{
		reconcileInterval:     time.Duration(opt.Config.ReconcileIntervalSeconds) * time.Second,
		metricCollectInterval: opt.MetricAdvisorConfig.CollectResUsedInterval,
		statesInformer:        opt.StatesInformer,
		metricCache:           opt.MetricCache,
		executor:              newTestExecutor(),
		cgroupReader:          resourceexecutor.NewCgroupReader(),
		containerLimiter:      make(map[string]*burstLimiter),
	}
}

type testThrottledMetrics struct {
	count           int
	aggregateValues map[metriccache.AggregationType]float64
}

var (
	testNodeInfo = &metriccache.NodeCPUInfo{
		ProcessorInfos: []util.ProcessorInfo{
			{CPUID: 0, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 1, CoreID: 0, SocketID: 0, NodeID: 0},
			{CPUID: 2, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 3, CoreID: 1, SocketID: 0, NodeID: 0},
			{CPUID: 4, CoreID: 2, SocketID: 1, NodeID: 0},
			{CPUID: 5, CoreID: 2, SocketID: 1, NodeID: 0},
			{CPUID: 6, CoreID: 3, SocketID: 1, NodeID: 0},
			{CPUID: 7, CoreID: 3, SocketID: 1, NodeID: 0},
			{CPUID: 8, CoreID: 4, SocketID: 2, NodeID: 1},
			{CPUID: 9, CoreID: 4, SocketID: 2, NodeID: 1},
			{CPUID: 10, CoreID: 5, SocketID: 2, NodeID: 1},
			{CPUID: 11, CoreID: 5, SocketID: 2, NodeID: 1},
			{CPUID: 12, CoreID: 6, SocketID: 3, NodeID: 1},
			{CPUID: 13, CoreID: 6, SocketID: 3, NodeID: 1},
			{CPUID: 14, CoreID: 7, SocketID: 3, NodeID: 1},
			{CPUID: 15, CoreID: 7, SocketID: 3, NodeID: 1},
		},
	}

	defaultAutoBurstCfg = slov1alpha1.CPUBurstConfig{
		Policy:                     slov1alpha1.CPUBurstAuto,
		CPUBurstPercent:            ptr.To[int64](1000),
		CFSQuotaBurstPercent:       ptr.To[int64](300),
		CFSQuotaBurstPeriodSeconds: ptr.To[int64](-1),
	}

	defaultAutoBurstStrategy = &slov1alpha1.CPUBurstStrategy{
		CPUBurstConfig:            defaultAutoBurstCfg,
		SharePoolThresholdPercent: ptr.To[int64](50),
	}
)

func genTestDefaultContainerNameByPod(podName string) string {
	return podName + "-test-container"
}

func genTestDefaultContainerIDByPod(podName string) string {
	containerName := genTestDefaultContainerNameByPod(podName)
	return genTestContainerIDByName(containerName)
}

func newTestPodWithQOS(name string, qos apiext.QoSClass, cpuMilli, memoryBytes int64) *corev1.Pod {
	containerName := genTestDefaultContainerNameByPod(name)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				apiext.LabelPodQoS: string(qos),
			},
			UID: types.UID(name),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: containerName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(memoryBytes, resource.BinarySI),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
							corev1.ResourceMemory: *resource.NewQuantity(memoryBytes, resource.BinarySI),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        containerName,
					ContainerID: genTestContainerIDByName(containerName),
					State:       corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				},
			},
			Phase: corev1.PodRunning,
		},
	}
}

func initPodCPUBurst(podMeta *statesinformer.PodMeta, value int64, helper *system.FileTestUtil) {
	helper.WriteCgroupFileContents(podMeta.CgroupDir, system.CPUBurst, strconv.FormatInt(value, 10))
}

func initContainerCPUBurst(podMeta *statesinformer.PodMeta, value int64, helper *system.FileTestUtil) {
	for i := range podMeta.Pod.Status.ContainerStatuses {
		containerStat := &podMeta.Pod.Status.ContainerStatuses[i]
		containerPath, _ := util.GetContainerCgroupParentDir(podMeta.CgroupDir, containerStat)
		helper.WriteCgroupFileContents(containerPath, system.CPUBurst, strconv.FormatInt(value, 10))
	}
}

func initPodCFSQuota(podMeta *statesinformer.PodMeta, value int64, helper *system.FileTestUtil) {
	helper.WriteCgroupFileContents(podMeta.CgroupDir, system.CPUCFSQuota, strconv.FormatInt(value, 10))
}

func initContainerCFSQuota(podMeta *statesinformer.PodMeta, containersNameValue map[string]int64,
	helper *system.FileTestUtil) {
	for i := range podMeta.Pod.Status.ContainerStatuses {
		containerStat := &podMeta.Pod.Status.ContainerStatuses[i]
		containerPath, _ := util.GetContainerCgroupParentDir(podMeta.CgroupDir, containerStat)
		value := containersNameValue[containerStat.Name]
		helper.WriteCgroupFileContents(containerPath, system.CPUCFSQuota, strconv.FormatInt(value, 10))
	}
}

func getPodCPUBurst(podDir string, helper *system.FileTestUtil) int64 {
	valueStr := helper.ReadCgroupFileContents(podDir, system.CPUBurst)
	value, _ := strconv.ParseInt(valueStr, 10, 64)
	return value
}

func getContainerCPUBurst(podDir string, containerStat *corev1.ContainerStatus, helper *system.FileTestUtil) int64 {
	containerPath, _ := util.GetContainerCgroupParentDir(podDir, containerStat)
	valueStr := helper.ReadCgroupFileContents(containerPath, system.CPUBurst)
	value, _ := strconv.ParseInt(valueStr, 10, 64)
	return value
}

func getPodCFSQuota(podMeta *statesinformer.PodMeta, helper *system.FileTestUtil) int64 {
	content := helper.ReadCgroupFileContents(podMeta.CgroupDir, system.CPUCFSQuota)
	val, _ := strconv.ParseInt(content, 10, 64)
	return val
}

func getContainerCFSQuota(podDir string, containerStat *corev1.ContainerStatus, helper *system.FileTestUtil) int64 {
	containerPath, _ := util.GetContainerCgroupParentDir(podDir, containerStat)
	content := helper.ReadCgroupFileContents(containerPath, system.CPUCFSQuota)
	val, _ := strconv.ParseInt(content, 10, 64)
	return val
}

func genTestContainerIDByName(containerName string) string {
	return fmt.Sprintf("docker://%s-id", containerName)
}

func createPodMetaByResource(podName string, containersRes map[string]corev1.ResourceRequirements) *statesinformer.PodMeta {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
			UID:  types.UID(podName + "-uid"),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{},
		},
	}
	for containerName, resource := range containersRes {
		container := corev1.Container{
			Name:      containerName,
			Resources: resource,
		}
		containerStat := corev1.ContainerStatus{
			Name:        containerName,
			ContainerID: genTestContainerIDByName(containerName),
			State:       corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}
		pod.Spec.Containers = append(pod.Spec.Containers, container)
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, containerStat)
	}
	return &statesinformer.PodMeta{
		Pod:       pod,
		CgroupDir: util.GetPodCgroupParentDir(pod),
	}
}

func TestNewCPUBurst(t *testing.T) {
	assert.NotPanics(t, func() {
		b := New(&framework.Options{
			Config:              framework.NewDefaultConfig(),
			MetricAdvisorConfig: maframework.NewDefaultConfig(),
			CgroupReader:        resourceexecutor.NewCgroupReader(),
		})
		assert.NotNil(t, b)
	})
}

func TestCPUBurst_getNodeStateForBurst(t *testing.T) {

	type podMetricSample struct {
		UID     string
		CPUUsed float64
	}
	type fields struct {
		nodeCPUUsed *resource.Quantity
		podsMetric  map[string]podMetricSample
		nodeCPUInfo *metriccache.NodeCPUInfo
	}
	type args struct {
		sharePoolThresholdPercent int64
		pods                      []*corev1.Pod
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   nodeStateForBurst
	}{
		{
			name: "get-unknown-status-because-of-nil-node-metric",
			fields: fields{
				nodeCPUUsed: nil,
				podsMetric:  nil,
				nodeCPUInfo: testNodeInfo,
			},
			args: args{
				sharePoolThresholdPercent: 50,
				pods:                      nil,
			},
			want: nodeBurstUnknown,
		},
		{
			name: "get-unknown-status-because-of-lack-node-info",
			fields: fields{
				nodeCPUUsed: resource.NewQuantity(6, resource.DecimalSI),
				podsMetric:  nil,
				nodeCPUInfo: nil,
			},
			args: args{
				sharePoolThresholdPercent: 50,
				pods:                      nil,
			},
			want: nodeBurstUnknown,
		},
		{
			// share-pool=16-8(LSR.Req)=8, share-pool-usage=10-7(LSR.Usage)=3; share-pool-usage-ratio=3/8; threshold = 50%
			name: "get-idle-status-with-lsr-pod",
			fields: fields{
				nodeCPUUsed: resource.NewQuantity(10, resource.DecimalSI),
				podsMetric: map[string]podMetricSample{
					"lsr-pod-1": {UID: "lsr-pod-1", CPUUsed: 7},
					"ls-pod-2":  {UID: "ls-pod-2", CPUUsed: 0.2},
				},
				nodeCPUInfo: testNodeInfo,
			},
			args: args{
				sharePoolThresholdPercent: 50,
				pods: []*corev1.Pod{
					newTestPodWithQOS("lsr-pod-1", apiext.QoSLSR, 8000, 8000),
					newTestPodWithQOS("ls-pod-2", apiext.QoSLS, 1000, 1000),
				},
			},
			want: nodeBurstIdle,
		},
		{
			// share-pool=16-8(LSR.Req)=8, share-pool-usage=10-5(LSR.Usage)=5; share-pool-usage-ratio=5/8; threshold = 50%
			name: "get-overload-status-with-lsr-pod",
			fields: fields{
				nodeCPUUsed: resource.NewQuantity(10, resource.DecimalSI),
				podsMetric: map[string]podMetricSample{
					"lsr-pod-1": {UID: "lsr-pod-1", CPUUsed: 5},
					"ls-pod-2":  {UID: "ls-pod-2", CPUUsed: 3},
				},
				nodeCPUInfo: testNodeInfo,
			},
			args: args{
				sharePoolThresholdPercent: 50,
				pods: []*corev1.Pod{
					newTestPodWithQOS("lsr-pod-1", apiext.QoSLSR, 8000, 8000),
					newTestPodWithQOS("ls-pod-2", apiext.QoSLS, 3000, 3000),
				},
			},
			want: nodeBurstOverload,
		},
		{
			// share-pool=16-8(LSR.Req)=8, share-pool-usage=10-6.25(LSR.Usage)=3.75; share-pool-usage-ratio=3.75/8; threshold = 50%
			name: "get-cooling-status-with-lsr-pod",
			fields: fields{
				nodeCPUUsed: resource.NewQuantity(10, resource.DecimalSI),
				podsMetric: map[string]podMetricSample{
					"lsr-pod-1": {UID: "lsr-pod-1", CPUUsed: 6.25},
					"ls-pod-2":  {UID: "ls-pod-2", CPUUsed: 1},
				},
				nodeCPUInfo: testNodeInfo,
			},
			args: args{
				sharePoolThresholdPercent: 50,
				pods: []*corev1.Pod{
					newTestPodWithQOS("lsr-pod-1", apiext.QoSLSR, 8000, 8000),
					newTestPodWithQOS("ls-pod-2", apiext.QoSLS, 2000, 2000),
				},
			},
			want: nodeBurstCooling,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			podMetas := testutil.GetPodMetas(tt.args.pods)
			ctl := gomock.NewController(t)
			mockstatesinformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockstatesinformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(tt.args.pods)).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)

			metriccache.DefaultAggregateResultFactory = mockResultFactory

			mockQuerier := mock_metriccache.NewMockQuerier(ctl)
			nodeResult := mock_metriccache.NewMockAggregateResult(ctl)
			if tt.fields.nodeCPUUsed == nil {
				nodeResult.EXPECT().Count().Return(0).AnyTimes()
			} else {
				nodeResult.EXPECT().Count().Return(1).AnyTimes()
				nodeResult.EXPECT().Value(gomock.Any()).Return(float64(tt.fields.nodeCPUUsed.Value()), nil).AnyTimes()
			}
			nodeQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			mockResultFactory.EXPECT().New(nodeQueryMeta).Return(nodeResult).AnyTimes()

			mockQuerier.EXPECT().QueryAndClose(nodeQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *nodeResult).Return(nil).AnyTimes()
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			for _, podMetric := range tt.fields.podsMetric {
				podQueryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
				assert.NoError(t, err)
				testutil.BuildMockQueryResult(ctl, mockQuerier, mockResultFactory, podQueryMeta, podMetric.CPUUsed)
			}
			mockMetricCache.EXPECT().Get(metriccache.NodeCPUInfoKey).Return(tt.fields.nodeCPUInfo, true).AnyTimes()

			//fakeRecorder := &FakeRecorder{}
			//client := clientsetfake.NewSimpleClientset()
			opt := &framework.Options{
				StatesInformer: mockstatesinformer,
				MetricCache:    mockMetricCache,
				// eventRecorder:  fakeRecorder,
				// kubeClient:     client,
				Config:              framework.NewDefaultConfig(),
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}
			b := newTestCPUBurst(opt)
			got := b.getNodeStateForBurst(tt.args.sharePoolThresholdPercent, podMetas)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_genPodBurstConfig(t *testing.T) {

	type args struct {
		podNamespace string
		podCfg       *slov1alpha1.CPUBurstConfig
		nodeCfg      *slov1alpha1.CPUBurstConfig
	}

	tests := []struct {
		name string
		args args
		want *slov1alpha1.CPUBurstConfig
	}{
		{
			name: "use-node-config",
			args: args{
				podCfg: nil,
				nodeCfg: &slov1alpha1.CPUBurstConfig{
					Policy:                     slov1alpha1.CPUBurstAuto,
					CPUBurstPercent:            ptr.To[int64](1000),
					CFSQuotaBurstPercent:       ptr.To[int64](300),
					CFSQuotaBurstPeriodSeconds: ptr.To[int64](600),
				},
			},
			want: &slov1alpha1.CPUBurstConfig{
				Policy:                     slov1alpha1.CPUBurstAuto,
				CPUBurstPercent:            ptr.To[int64](1000),
				CFSQuotaBurstPercent:       ptr.To[int64](300),
				CFSQuotaBurstPeriodSeconds: ptr.To[int64](600),
			},
		},
		{
			name: "use-pod-config",
			args: args{
				podCfg: &slov1alpha1.CPUBurstConfig{
					Policy:                     slov1alpha1.CPUBurstAuto,
					CPUBurstPercent:            ptr.To[int64](1000),
					CFSQuotaBurstPercent:       ptr.To[int64](300),
					CFSQuotaBurstPeriodSeconds: ptr.To[int64](600),
				},
				nodeCfg: nil,
			},
			want: &slov1alpha1.CPUBurstConfig{
				Policy:                     slov1alpha1.CPUBurstAuto,
				CPUBurstPercent:            ptr.To[int64](1000),
				CFSQuotaBurstPercent:       ptr.To[int64](300),
				CFSQuotaBurstPeriodSeconds: ptr.To[int64](600),
			},
		},
		{
			name: "merge-pod-config",
			args: args{
				podCfg: &slov1alpha1.CPUBurstConfig{
					Policy:          slov1alpha1.CPUBurstOnly,
					CPUBurstPercent: ptr.To[int64](500),
				},
				nodeCfg: &slov1alpha1.CPUBurstConfig{
					Policy:                     slov1alpha1.CPUBurstAuto,
					CPUBurstPercent:            ptr.To[int64](1000),
					CFSQuotaBurstPercent:       ptr.To[int64](300),
					CFSQuotaBurstPeriodSeconds: ptr.To[int64](600),
				},
			},
			want: &slov1alpha1.CPUBurstConfig{
				Policy:                     slov1alpha1.CPUBurstOnly,
				CPUBurstPercent:            ptr.To[int64](500),
				CFSQuotaBurstPercent:       ptr.To[int64](300),
				CFSQuotaBurstPeriodSeconds: ptr.To[int64](600),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   tt.args.podNamespace,
					Annotations: make(map[string]string),
				},
			}
			if tt.args.podCfg != nil {
				annoStr, _ := json.Marshal(tt.args.podCfg)
				pod.Annotations[slov1alpha1.AnnotationPodCPUBurst] = string(annoStr)
			}
			if got := genPodBurstConfig(pod, tt.args.nodeCfg); !reflect.DeepEqual(got, tt.want) {
				gotStr, _ := json.Marshal(got)
				wantStr, _ := json.Marshal(tt.want)
				t.Errorf("genPodBurstConfig() =\n%v\nwant =\n%v", string(gotStr), string(wantStr))
			}
		})
	}
}

func TestCPUBurst_applyCPUBurst(t *testing.T) {
	type fields struct {
		podName      string
		containerRes map[string]corev1.ResourceRequirements
	}
	type args struct {
		burstCfg slov1alpha1.CPUBurstConfig
	}
	type want struct {
		containerBurstVal map[string]int64
		podBurstVal       int64
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   want
	}{
		{
			name: "apply-by-default-burst-config",
			fields: fields{
				podName: "test-pod-1",
				containerRes: map[string]corev1.ResourceRequirements{
					"test-container-1": {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(3000, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(5000, resource.DecimalSI),
						},
					},
					"test-container-2": {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(3000, resource.DecimalSI),
						},
					},
				},
			},
			args: args{
				burstCfg: defaultAutoBurstCfg,
			},
			want: want{
				containerBurstVal: map[string]int64{
					"test-container-1": 5 * 10 * system.CFSBasePeriodValue,
					"test-container-2": 3 * 10 * system.CFSBasePeriodValue,
				},
				podBurstVal: (5 + 3) * 10 * system.CFSBasePeriodValue,
			},
		},
		{
			name: "apply-by-specified-burst-config",
			fields: fields{
				podName: "test-pod-1",
				containerRes: map[string]corev1.ResourceRequirements{
					"test-container-1": {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(3000, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(5000, resource.DecimalSI),
						},
					},
					"test-container-2": {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(3000, resource.DecimalSI),
						},
					},
				},
			},
			args: args{
				burstCfg: slov1alpha1.CPUBurstConfig{
					Policy:          slov1alpha1.CPUBurstAuto,
					CPUBurstPercent: ptr.To[int64](500),
				},
			},
			want: want{
				containerBurstVal: map[string]int64{
					"test-container-1": 5 * 5 * system.CFSBasePeriodValue,
					"test-container-2": 3 * 5 * system.CFSBasePeriodValue,
				},
				podBurstVal: (5 + 3) * 5 * system.CFSBasePeriodValue,
			},
		},
		{
			name: "apply-by-disabled-burst-config",
			fields: fields{
				podName: "test-pod-1",
				containerRes: map[string]corev1.ResourceRequirements{
					"test-container-1": {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(3000, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(5000, resource.DecimalSI),
						},
					},
					"test-container-2": {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(3000, resource.DecimalSI),
						},
					},
				},
			},
			args: args{
				burstCfg: slov1alpha1.CPUBurstConfig{
					Policy:          slov1alpha1.CFSQuotaBurstOnly,
					CPUBurstPercent: ptr.To[int64](500),
				},
			},
			want: want{
				containerBurstVal: map[string]int64{
					"test-container-1": 0,
					"test-container-2": 0,
				},
				podBurstVal: 0,
			},
		},
		{
			name: "apply-by-unlimited-container",
			fields: fields{
				podName: "test-pod-1",
				containerRes: map[string]corev1.ResourceRequirements{
					"test-container-1": {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(3000, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{},
					},
					"test-container-2": {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Limits: corev1.ResourceList{},
					},
				},
			},
			args: args{
				burstCfg: defaultAutoBurstCfg,
			},
			want: want{
				containerBurstVal: map[string]int64{
					"test-container-1": 0,
					"test-container-2": 0,
				},
				podBurstVal: 0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := system.NewFileTestUtil(t)

			b := &cpuBurst{
				executor: newTestExecutor(),
			}

			stop := make(chan struct{})
			b.init(stop)
			defer func() { stop <- struct{}{} }()

			podMeta := createPodMetaByResource(tt.fields.podName, tt.fields.containerRes)

			initPodCPUBurst(podMeta, 0, testHelper)
			initContainerCPUBurst(podMeta, 0, testHelper)

			b.applyCPUBurst(&tt.args.burstCfg, podMeta)

			for i := range podMeta.Pod.Status.ContainerStatuses {
				containerStat := &podMeta.Pod.Status.ContainerStatuses[i]
				want := tt.want.containerBurstVal[containerStat.Name]
				got := getContainerCPUBurst(podMeta.CgroupDir, containerStat, testHelper)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("container %v applyCPUBurst() = %v, want = %v", containerStat.Name, got, want)
				}
			}

			gotPod := getPodCPUBurst(podMeta.CgroupDir, testHelper)
			if !reflect.DeepEqual(gotPod, tt.want.podBurstVal) {
				t.Errorf("pod %v applyCPUBurst() = %v, want = %v", podMeta.Pod.Name, gotPod, tt.want.podBurstVal)
			}
		})
	}
}

func TestCPUBurst_applyCFSQuotaBurst(t *testing.T) {
	testPodName1 := "test-pod-1"
	testContainerName1 := "test-container-1"
	testContainerName2 := "test-container-2"
	testContainerID1 := genTestContainerIDByName(testContainerName1)
	testContainerID2 := genTestContainerIDByName(testContainerName2)
	type containerMetricSample struct {
		ContainerID string
		CPUUsed     float64
	}
	type fields struct {
		podName              string
		containerRes         map[string]corev1.ResourceRequirements
		podCurCFSQuota       int64
		containerCurCFSQuota map[string]int64
		containerMetric      map[string]containerMetricSample
		containersThrottled  map[string]testThrottledMetrics
	}
	type args struct {
		burstCfg  slov1alpha1.CPUBurstConfig
		nodeState nodeStateForBurst
	}
	type want struct {
		podCFSQuotaVal       int64
		containerCFSQuotaVal map[string]int64
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   want
	}{
		{
			name: "scale-reset-for-burst-config-disabled-on-idle-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: 2 * 2 * system.CFSBasePeriodValue,
				containerCurCFSQuota: map[string]int64{
					testContainerName1: 2 * 2 * system.CFSBasePeriodValue,
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg: slov1alpha1.CPUBurstConfig{
					Policy:                     slov1alpha1.CPUBurstOnly,
					CPUBurstPercent:            ptr.To[int64](1000),
					CFSQuotaBurstPercent:       ptr.To[int64](300),
					CFSQuotaBurstPeriodSeconds: ptr.To[int64](-1),
				},
				nodeState: nodeBurstIdle,
			},
			want: want{
				podCFSQuotaVal: int64(2 * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-remain-for-throttled-pod-on-cooling-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				containerCurCFSQuota: map[string]int64{
					testContainerName1: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg:  defaultAutoBurstCfg,
				nodeState: nodeBurstCooling,
			},
			want: want{
				podCFSQuotaVal: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				},
			},
		},

		{
			name: "scale-down-to-base-for-throttled-pod-on-overload-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: int64(2 * 1.01 * float64(system.CFSBasePeriodValue)),
				containerCurCFSQuota: map[string]int64{
					testContainerName1: int64(2 * 1.01 * float64(system.CFSBasePeriodValue)),
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg:  defaultAutoBurstCfg,
				nodeState: nodeBurstOverload,
			},
			want: want{
				podCFSQuotaVal: int64(2 * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-down-for-throttled-pod-on-overload-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				containerCurCFSQuota: map[string]int64{
					testContainerName1: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg:  defaultAutoBurstCfg,
				nodeState: nodeBurstOverload,
			},
			want: want{
				podCFSQuotaVal: int64(2 * 2 * cfsDecreaseStep * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * 2 * cfsDecreaseStep * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-down-because-burst-period-config-zero-on-idle-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				containerCurCFSQuota: map[string]int64{
					testContainerName1: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg: slov1alpha1.CPUBurstConfig{
					Policy:                     slov1alpha1.CPUBurstAuto,
					CPUBurstPercent:            ptr.To[int64](1000),
					CFSQuotaBurstPercent:       ptr.To[int64](300),
					CFSQuotaBurstPeriodSeconds: ptr.To[int64](0),
				},
				nodeState: nodeBurstIdle,
			},
			want: want{
				podCFSQuotaVal: int64(2 * 2 * cfsDecreaseStep * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * 2 * cfsDecreaseStep * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-reset-because-burst-percent-config-illegal-on-idle-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				containerCurCFSQuota: map[string]int64{
					testContainerName1: int64(2 * 2 * float64(system.CFSBasePeriodValue)),
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg: slov1alpha1.CPUBurstConfig{
					Policy:                     slov1alpha1.CPUBurstAuto,
					CPUBurstPercent:            ptr.To[int64](1000),
					CFSQuotaBurstPercent:       ptr.To[int64](90),
					CFSQuotaBurstPeriodSeconds: ptr.To[int64](-1),
				},
				nodeState: nodeBurstIdle,
			},
			want: want{
				podCFSQuotaVal: int64(2 * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-remain-because-not-throttled-on-idle-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: int64(2 * 2.9 * float64(system.CFSBasePeriodValue)),
				containerCurCFSQuota: map[string]int64{
					testContainerName1: int64(2 * 2.9 * float64(system.CFSBasePeriodValue)),
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0,
						},
					},
				},
			},
			args: args{
				burstCfg:  defaultAutoBurstCfg,
				nodeState: nodeBurstIdle,
			},
			want: want{
				podCFSQuotaVal: int64(2 * 2.9 * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * 2.9 * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-up-limit-by-ceil-on-idle-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: int64(2 * 2.9 * float64(system.CFSBasePeriodValue)),
				containerCurCFSQuota: map[string]int64{
					testContainerName1: int64(2 * 2.9 * float64(system.CFSBasePeriodValue)),
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg:  defaultAutoBurstCfg,
				nodeState: nodeBurstIdle,
			},
			want: want{
				podCFSQuotaVal: int64(2 * 3 * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * 3 * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-up-from-base-on-idle-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: 2 * system.CFSBasePeriodValue,
				containerCurCFSQuota: map[string]int64{
					testContainerName1: 2 * system.CFSBasePeriodValue,
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg:  defaultAutoBurstCfg,
				nodeState: nodeBurstIdle,
			},
			want: want{
				podCFSQuotaVal: int64(2 * cfsIncreaseStep * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * cfsIncreaseStep * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-up-two-container-from-base-on-idle-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
					testContainerName2: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(3000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: (2 + 3) * system.CFSBasePeriodValue,
				containerCurCFSQuota: map[string]int64{
					testContainerName1: 2 * system.CFSBasePeriodValue,
					testContainerName2: 3 * system.CFSBasePeriodValue,
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
					testContainerName2: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg:  defaultAutoBurstCfg,
				nodeState: nodeBurstIdle,
			},
			want: want{
				podCFSQuotaVal: int64((2 + 3) * cfsIncreaseStep * float64(system.CFSBasePeriodValue)),
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * cfsIncreaseStep * float64(system.CFSBasePeriodValue)),
					testContainerName2: int64(3 * cfsIncreaseStep * float64(system.CFSBasePeriodValue)),
				},
			},
		},
		{
			name: "scale-up-one-container-in-unlimited-pod-from-base-on-idle-state",
			fields: fields{
				podName: testPodName1,
				containerRes: map[string]corev1.ResourceRequirements{
					testContainerName1: {
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(2000, resource.DecimalSI),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
					testContainerName2: {
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: *resource.NewMilliQuantity(1000, resource.DecimalSI),
						},
					},
				},
				podCurCFSQuota: -1,
				containerCurCFSQuota: map[string]int64{
					testContainerName1: 2 * system.CFSBasePeriodValue,
					testContainerName2: -1,
				},
				containerMetric: map[string]containerMetricSample{
					testContainerName1: {testContainerID1, 1.5},
					testContainerName2: {testContainerID2, 1.5},
				},
				containersThrottled: map[string]testThrottledMetrics{
					testContainerName1: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
					testContainerName2: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			args: args{
				burstCfg:  defaultAutoBurstCfg,
				nodeState: nodeBurstIdle,
			},
			want: want{
				podCFSQuotaVal: -1,
				containerCFSQuotaVal: map[string]int64{
					testContainerName1: int64(2 * cfsIncreaseStep * float64(system.CFSBasePeriodValue)),
					testContainerName2: -1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHelper := system.NewFileTestUtil(t)

			stop := make(chan struct{})
			defer func() { stop <- struct{}{} }()

			podMeta := createPodMetaByResource(tt.fields.podName, tt.fields.containerRes)

			ctl := gomock.NewController(t)
			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)

			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			for _, containerMetric := range tt.fields.containerMetric {
				querMeta, err := metriccache.ContainerCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Container(containerMetric.ContainerID))
				assert.NoError(t, err)
				testutil.BuildMockQueryResult(ctl, mockQuerier, mockResultFactory, querMeta, containerMetric.CPUUsed)
			}
			for containerID, containerMetric := range tt.fields.containersThrottled {
				result := mock_metriccache.NewMockAggregateResult(ctl)
				result.EXPECT().Count().Return(containerMetric.count).AnyTimes()
				for aggregateType, value := range containerMetric.aggregateValues {
					result.EXPECT().Value(aggregateType).Return(value, nil).AnyTimes()
				}

				queryMeta, err := metriccache.ContainerCPUThrottledMetric.BuildQueryMeta(
					metriccache.MetricPropertiesFunc.Container(genTestContainerIDByName(containerID)))
				assert.NoError(t, err)

				mockResultFactory.EXPECT().New(queryMeta).Return(result).AnyTimes()
				mockQuerier.EXPECT().QueryAndClose(queryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			}

			initPodCFSQuota(podMeta, tt.fields.podCurCFSQuota, testHelper)
			initContainerCFSQuota(podMeta, tt.fields.containerCurCFSQuota, testHelper)

			b := &cpuBurst{
				statesInformer:   mockStatesInformer,
				metricCache:      mockMetricCache,
				executor:         newTestExecutor(),
				cgroupReader:     resourceexecutor.NewCgroupReader(),
				containerLimiter: make(map[string]*burstLimiter),
			}
			b.init(stop)
			b.applyCFSQuotaBurst(&tt.args.burstCfg, podMeta, tt.args.nodeState)

			gotPod := getPodCFSQuota(podMeta, testHelper)
			if !reflect.DeepEqual(gotPod, tt.want.podCFSQuotaVal) {
				t.Errorf("pod %v applyCFSQuotaBurst() = %v, want = %v",
					podMeta.Pod.Name, gotPod, tt.want.podCFSQuotaVal)
			}
			for i := range podMeta.Pod.Status.ContainerStatuses {
				containerStat := &podMeta.Pod.Status.ContainerStatuses[i]
				want := tt.want.containerCFSQuotaVal[containerStat.Name]
				got := getContainerCFSQuota(podMeta.CgroupDir, containerStat, testHelper)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("container %v applyCFSQuotaBurst() = %v, want = %v", containerStat.Name, got, want)
				}
			}
		})
	}
}

func Test_burstLimiter_Allow(t *testing.T) {
	type fields struct {
		burstPeriodSec    int64
		maxScalePercent   int64
		initSizeRatio     float64
		lastUpdateSeconds int64
	}
	type args struct {
		currentUsageScalePercent int64
	}
	tests := []struct {
		name         string
		fields       fields
		args         args
		wantAllow    bool
		wantCurToken int64
	}{
		{
			name: "consume-enough-token-with-full-init",
			fields: fields{
				burstPeriodSec:    100,
				maxScalePercent:   150,
				initSizeRatio:     1,
				lastUpdateSeconds: 10,
			},
			args: args{
				currentUsageScalePercent: 150,
			},
			wantAllow: true,
			// capacity=100*50=5000, current-size=5000, need=50*10=500, size-after-consume=4500
			wantCurToken: 4500,
		},
		{
			name: "consume-not-enough-token-with-part-init",
			fields: fields{
				burstPeriodSec:    100,
				maxScalePercent:   150,
				initSizeRatio:     0.01,
				lastUpdateSeconds: 10,
			},
			args: args{
				currentUsageScalePercent: 200,
			},
			wantAllow: false,
			// capacity=100*50=5000, current-size=5000*0.01=50, need=100*10=1000, size-after-consume=-950
			wantCurToken: -950,
		},
		{
			name: "consume-not-enough-token-to-bottom-with-part-init",
			fields: fields{
				burstPeriodSec:    10,
				maxScalePercent:   150,
				initSizeRatio:     0.01,
				lastUpdateSeconds: 10,
			},
			args: args{
				currentUsageScalePercent: 200,
			},
			wantAllow: false,
			// capacity=10*50=500, current-size=500*0.01=5, need=100*10=1000, size-after-consume=-500
			wantCurToken: -500,
		},
		{
			name: "accumulate-token-with-part-init",
			fields: fields{
				burstPeriodSec:    100,
				maxScalePercent:   150,
				initSizeRatio:     0.1,
				lastUpdateSeconds: 10,
			},
			args: args{
				currentUsageScalePercent: 40,
			},
			wantAllow: true,
			// capacity=100*50=5000, current-size=5000*0.1=500, accumulate=60*10=600, size-after-accumulate=1100
			wantCurToken: 1100,
		},
		{
			name: "accumulate-token-to-ceil-with-part-init",
			fields: fields{
				burstPeriodSec:    100,
				maxScalePercent:   150,
				initSizeRatio:     0.9,
				lastUpdateSeconds: 10,
			},
			args: args{
				currentUsageScalePercent: 40,
			},
			wantAllow: true,
			// capacity=100*50=5000, current-size=5000*0.9=4500, accumulate=60*10=600, size-after-accumulate=5000
			wantCurToken: 5000,
		},
	}
	for _, tt := range tests {
		now := time.Now()
		t.Run(tt.name, func(t *testing.T) {
			l := newBurstLimiter(tt.fields.burstPeriodSec, tt.fields.maxScalePercent)
			l.currentToken = int64(float64(l.bucketCapacity) * tt.fields.initSizeRatio)
			l.lastUpdateTime = now.Add(-time.Duration(tt.fields.lastUpdateSeconds) * time.Second)
			gotAllow, gotCurToken := l.Allow(now, tt.args.currentUsageScalePercent)
			if gotAllow != tt.wantAllow {
				t.Errorf("Allow() gotAllow = %v, wantAllow %v", gotAllow, tt.wantAllow)
			}
			if gotCurToken != tt.wantCurToken {
				t.Errorf("Allow() gotCurToken = %v, wantCurToken %v", gotCurToken, tt.wantCurToken)
			}
		})
	}
}

func Test_burstLimiter_UpdateIfChanged(t *testing.T) {
	type fields struct {
		oldBurstPeriodSec  int64
		oldMaxScalePercent int64
	}
	type args struct {
		burstPeriodSec  int64
		maxScalePercent int64
	}
	type want struct {
		newCapactiy int64
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   want
	}{
		{
			name: "update-limiter",
			fields: fields{
				oldBurstPeriodSec:  600,
				oldMaxScalePercent: 300,
			},
			args: args{
				burstPeriodSec:  300,
				maxScalePercent: 200,
			},
			want: want{
				newCapactiy: 300 * 100,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newBurstLimiter(tt.fields.oldBurstPeriodSec, tt.fields.oldMaxScalePercent)
			l.UpdateIfChanged(tt.args.burstPeriodSec, tt.args.maxScalePercent)
			if l.bucketCapacity != tt.want.newCapactiy {
				t.Errorf("UpdateIfChanged() gotCapacity = %v, wantCapacity %v", l.bucketCapacity, tt.want.newCapactiy)
			}
		})
	}
}

func TestCPUBurst_start(t *testing.T) {
	lsrPodName := "lsr-pod-1"
	lsPodName := "ls-pod-2"
	lsrContainerName := genTestDefaultContainerNameByPod(lsrPodName)
	lsContainerName := genTestDefaultContainerNameByPod(lsPodName)
	lsrContainerID := genTestDefaultContainerIDByPod(lsrPodName)
	lsContainerID := genTestDefaultContainerIDByPod(lsPodName)
	type podMetricSample struct {
		UID     string
		CPUUsed float64
	}

	type fields struct {
		nodeCPUUsed          *resource.Quantity
		podsMetric           map[string]podMetricSample
		nodeCPUInfo          *metriccache.NodeCPUInfo
		pods                 []*corev1.Pod
		nodeSLO              *slov1alpha1.NodeSLO
		podsCurCFSQuota      map[string]int64
		containerCurCFSQuota map[string]int64
		containersThrottled  map[string]testThrottledMetrics
	}
	type want struct {
		podBurstVal          map[string]int64
		podCFSQuotaVal       map[string]int64
		containerBurstVal    map[string]int64
		containerCFSQuotaVal map[string]int64
	}
	tests := []struct {
		name   string
		fields fields
		want   want
	}{
		{
			name: "scale-up-for-normal-pod",
			fields: fields{
				nodeCPUUsed: resource.NewQuantity(10, resource.DecimalSI),
				podsMetric: map[string]podMetricSample{
					lsrPodName: {lsrPodName, 7},
					lsPodName:  {lsPodName, 0.2},
				},
				nodeCPUInfo: testNodeInfo,
				pods: []*corev1.Pod{
					newTestPodWithQOS(lsrPodName, apiext.QoSLSR, 8000, 8000),
					newTestPodWithQOS(lsPodName, apiext.QoSLS, 1000, 1000),
				},
				nodeSLO: &slov1alpha1.NodeSLO{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-1",
					},
					Spec: slov1alpha1.NodeSLOSpec{
						CPUBurstStrategy: defaultAutoBurstStrategy,
					},
				},
				podsCurCFSQuota: map[string]int64{
					lsrPodName: -1,
					lsPodName:  2 * system.CFSBasePeriodValue,
				},
				containerCurCFSQuota: map[string]int64{
					lsrContainerName: -1,
					lsContainerName:  2 * system.CFSBasePeriodValue,
				},
				containersThrottled: map[string]testThrottledMetrics{
					lsrContainerID: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0,
						},
					},
					lsContainerID: {
						count: 1,
						aggregateValues: map[metriccache.AggregationType]float64{
							metriccache.AggregationTypeLast: 0.5,
						},
					},
				},
			},
			want: want{
				podBurstVal: map[string]int64{
					lsrPodName: 0,
					lsPodName:  1 * 10 * system.CFSBasePeriodValue,
				},
				podCFSQuotaVal: map[string]int64{
					lsrPodName: -1,
					lsPodName:  int64(2 * cfsIncreaseStep * float64(system.CFSBasePeriodValue)),
				},
				containerBurstVal: map[string]int64{
					lsrContainerName: 0,
					lsContainerName:  1 * 10 * system.CFSBasePeriodValue,
				},
				containerCFSQuotaVal: map[string]int64{
					lsrContainerName: -1,
					lsContainerName:  int64(2 * cfsIncreaseStep * float64(system.CFSBasePeriodValue)),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			podMetas := testutil.GetPodMetas(tt.fields.pods)
			ctl := gomock.NewController(t)
			mockStatesInformer := mock_statesinformer.NewMockStatesInformer(ctl)
			mockStatesInformer.EXPECT().GetAllPods().Return(testutil.GetPodMetas(tt.fields.pods)).AnyTimes()
			mockStatesInformer.EXPECT().GetNodeSLO().Return(tt.fields.nodeSLO).AnyTimes()

			mockResultFactory := mock_metriccache.NewMockAggregateResultFactory(ctl)
			metriccache.DefaultAggregateResultFactory = mockResultFactory
			mockQuerier := mock_metriccache.NewMockQuerier(ctl)

			nodeCPUAggregateResult := mock_metriccache.NewMockAggregateResult(ctl)
			nodeCPUAggregateResult.EXPECT().Value(gomock.Any()).Return(float64(tt.fields.nodeCPUUsed.Value()), nil).AnyTimes()
			nodeCPUAggregateResult.EXPECT().Count().Return(1).AnyTimes()

			nodeCPUQueryMeta, err := metriccache.NodeCPUUsageMetric.BuildQueryMeta(nil)
			assert.NoError(t, err)
			mockResultFactory.EXPECT().New(nodeCPUQueryMeta).Return(nodeCPUAggregateResult).AnyTimes()
			mockQuerier.EXPECT().QueryAndClose(nodeCPUQueryMeta, gomock.Any(), gomock.Any()).SetArg(2, *nodeCPUAggregateResult).Return(nil).AnyTimes()

			mockMetricCache := mock_metriccache.NewMockMetricCache(ctl)
			mockMetricCache.EXPECT().Querier(gomock.Any(), gomock.Any()).Return(mockQuerier, nil).AnyTimes()

			for _, podMetric := range tt.fields.podsMetric {
				queryMeta, err := metriccache.PodCPUUsageMetric.BuildQueryMeta(metriccache.MetricPropertiesFunc.Pod(podMetric.UID))
				assert.NoError(t, err)
				testutil.BuildMockQueryResult(ctl, mockQuerier, mockResultFactory, queryMeta, podMetric.CPUUsed)
			}

			mockMetricCache.EXPECT().Get(metriccache.NodeCPUInfoKey).Return(tt.fields.nodeCPUInfo, true).AnyTimes()
			for containerID, containerMetric := range tt.fields.containersThrottled {
				result := mock_metriccache.NewMockAggregateResult(ctl)
				result.EXPECT().Count().Return(containerMetric.count).AnyTimes()
				for aggregateType, value := range containerMetric.aggregateValues {
					result.EXPECT().Value(aggregateType).Return(value, nil).AnyTimes()
				}

				queryMeta, err := metriccache.ContainerCPUThrottledMetric.BuildQueryMeta(
					metriccache.MetricPropertiesFunc.Container(containerID))
				assert.NoError(t, err)

				mockResultFactory.EXPECT().New(queryMeta).Return(result).AnyTimes()
				mockQuerier.EXPECT().QueryAndClose(queryMeta, gomock.Any(), gomock.Any()).SetArg(2, *result).Return(nil).AnyTimes()
			}

			opt := &framework.Options{
				Config:              framework.NewDefaultConfig(),
				StatesInformer:      mockStatesInformer,
				MetricCache:         mockMetricCache,
				MetricAdvisorConfig: maframework.NewDefaultConfig(),
			}

			testHelper := system.NewFileTestUtil(t)

			b := newTestCPUBurst(opt)
			stop := make(chan struct{})
			b.init(stop)
			defer func() { stop <- struct{}{} }()

			for _, podMeta := range podMetas {
				podCurCFSQuota := tt.fields.podsCurCFSQuota[podMeta.Pod.Name]
				initPodCPUBurst(podMeta, 0, testHelper)
				initContainerCPUBurst(podMeta, 0, testHelper)
				initPodCFSQuota(podMeta, podCurCFSQuota, testHelper)
				initContainerCFSQuota(podMeta, tt.fields.containerCurCFSQuota, testHelper)
			}

			b.start()

			for _, podMeta := range podMetas {
				wantPodCPUBurst := tt.want.podBurstVal[podMeta.Pod.Name]
				gotPodCPUBurst := getPodCPUBurst(podMeta.CgroupDir, testHelper)
				if !reflect.DeepEqual(gotPodCPUBurst, wantPodCPUBurst) {
					t.Errorf("pod %v cpu burst after start() = %v, want = %v",
						podMeta.Pod.Name, gotPodCPUBurst, wantPodCPUBurst)
				}

				wantPodCFSQuota := tt.want.podCFSQuotaVal[podMeta.Pod.Name]
				gotPodCFSQuota := getPodCFSQuota(podMeta, testHelper)
				if !reflect.DeepEqual(gotPodCFSQuota, wantPodCFSQuota) {
					t.Errorf("pod %v cfs quota after start() = %v, want = %v",
						podMeta.Pod.Name, gotPodCFSQuota, wantPodCFSQuota)
				}

				for i := range podMeta.Pod.Status.ContainerStatuses {
					containerStat := &podMeta.Pod.Status.ContainerStatuses[i]

					wantContainerCPUBurst := tt.want.containerBurstVal[containerStat.Name]
					gotContainerCPUBurst := getContainerCPUBurst(podMeta.CgroupDir, containerStat, testHelper)
					if !reflect.DeepEqual(gotContainerCPUBurst, wantContainerCPUBurst) {
						t.Errorf("container %v cpu burst after start() = %v, wantContainerCPUBurst = %v",
							containerStat.Name, gotContainerCPUBurst, wantContainerCPUBurst)
					}

					wantContainerCFSQuota := tt.want.containerCFSQuotaVal[containerStat.Name]
					gotContainerCFSQuota := getContainerCFSQuota(podMeta.CgroupDir, containerStat, testHelper)
					if !reflect.DeepEqual(gotContainerCFSQuota, wantContainerCFSQuota) {
						t.Errorf("container %v cfs quota after start() = %v, wantContainerCFSQuota = %v",
							containerStat.Name, gotContainerCFSQuota, wantContainerCFSQuota)
					}
				}
			}
		})
	}
}

func TestCPUBurst_Recycle(t *testing.T) {
	expireLimiterName := "expire-limiter"
	notExpireLimiterName := "not-expire-limiter"
	expireDuration := int64(600)
	type fields struct {
		containerLimiter             map[string]*burstLimiter
		limiterLastUpdatePastSeconds map[string]int64
	}
	type want struct {
		notExpireLimiterNames []string
	}
	tests := []struct {
		name   string
		fields fields
		want   want
	}{
		{
			name: "delete-expire-limiter",
			fields: fields{
				containerLimiter: map[string]*burstLimiter{
					expireLimiterName:    newBurstLimiter(expireDuration/2, 200),
					notExpireLimiterName: newBurstLimiter(expireDuration/2, 200),
				},
				limiterLastUpdatePastSeconds: map[string]int64{
					expireLimiterName:    expireDuration + 10,
					notExpireLimiterName: expireDuration - 10,
				},
			},
			want: want{
				notExpireLimiterNames: []string{notExpireLimiterName},
			},
		},
	}
	for _, tt := range tests {
		now := time.Now()
		t.Run(tt.name, func(t *testing.T) {
			b := &cpuBurst{
				containerLimiter: tt.fields.containerLimiter,
			}
			for name, lastUpdatePastSeconds := range tt.fields.limiterLastUpdatePastSeconds {
				limiter := b.containerLimiter[name]
				limiter.lastUpdateTime = now.Add(-time.Duration(lastUpdatePastSeconds) * time.Second)
			}
			b.Recycle()

			if len(b.containerLimiter) != len(tt.want.notExpireLimiterNames) {
				t.Errorf("limiter size got after Recycle() %v, want %v",
					len(b.containerLimiter), len(tt.want.notExpireLimiterNames))
			}
			for _, notExpireName := range tt.want.notExpireLimiterNames {
				if _, exist := b.containerLimiter[notExpireName]; !exist {
					t.Errorf("limiter %v not exist after Recycle()", notExpireName)
				}
			}
		})
	}
}

type cpuBurstGreyCtrlPlugin struct{}

func (p *cpuBurstGreyCtrlPlugin) Setup(kubeClient clientset.Interface) error {
	return nil
}

func (p *cpuBurstGreyCtrlPlugin) Run(stopCh <-chan struct{}) {
	return
}

func (p *cpuBurstGreyCtrlPlugin) InjectPodPolicy(pod *corev1.Pod, policyType framework.QOSPolicyType, greyCtlCfgIf *interface{}) (bool, error) {
	injected := false
	greyCtlCfg := &slov1alpha1.CPUBurstConfig{}
	if pod.Namespace == "allow-ns" {
		greyCtlCfg.Policy = slov1alpha1.CPUBurstAuto
		injected = true
	} else if pod.Namespace == "block-ns" {
		greyCtlCfg.Policy = slov1alpha1.CPUBurstNone
		injected = true
	}
	if injected {
		*greyCtlCfgIf = greyCtlCfg
	}
	return injected, nil
}

func (p *cpuBurstGreyCtrlPlugin) name() string {
	return "cpu-burst-test-plugin"
}

func Test_genPodBurstConfigWithPlugin(t *testing.T) {
	type args struct {
		pod            *corev1.Pod
		podCPUBurstCfg *slov1alpha1.CPUBurstConfig
		nodeCfg        slov1alpha1.CPUBurstConfig
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.CPUBurstConfig
	}{
		{
			name: "inject by allowed ns list",
			args: args{
				pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "allow-ns",
					},
				},
				podCPUBurstCfg: nil,
				nodeCfg:        sloconfig.DefaultCPUBurstConfig(),
			},
			want: &slov1alpha1.CPUBurstConfig{
				Policy:                     slov1alpha1.CPUBurstAuto,
				CPUBurstPercent:            ptr.To[int64](1000),
				CFSQuotaBurstPercent:       ptr.To[int64](300),
				CFSQuotaBurstPeriodSeconds: ptr.To[int64](-1),
			},
		},
	}
	p := &cpuBurstGreyCtrlPlugin{}
	framework.ClearQOSGreyCtrlPlugin()
	framework.RegisterQOSGreyCtrlPlugin(p.name(), p)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.args.podCPUBurstCfg != nil {
				podCPUBurstCfgStr, _ := json.Marshal(tt.args.podCPUBurstCfg)
				tt.args.pod.Annotations = map[string]string{
					slov1alpha1.AnnotationPodCPUBurst: string(podCPUBurstCfgStr),
				}
			}
			gotCfg := genPodBurstConfig(tt.args.pod, &tt.args.nodeCfg)
			assert.Equal(t, tt.want, gotCfg)
		})
	}
	framework.UnregisterQOSGreyCtrlPlugin(p.name())
}
