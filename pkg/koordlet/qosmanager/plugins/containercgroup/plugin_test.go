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

package containercgroup

import (
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mockstatesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	koordletutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
)

func TestParseOverrideSpec(t *testing.T) {
	t.Run("flat memory", func(t *testing.T) {
		spec, err := ParseOverrideSpec(`{"containerName":"app","memoryMax":"512Mi"}`)
		assert.NoError(t, err)
		assert.Equal(t, "app", spec.ContainerName)
		assert.Equal(t, "512Mi", spec.resources().MemoryMax())
		assert.True(t, spec.writebackEnabled())
	})
	t.Run("nested resources", func(t *testing.T) {
		spec, err := ParseOverrideSpec(`{"containerName":"app","resources":{"memory":{"max":"512Mi"},"cpu":{"quota":"200m","cpuset":"0-1"}}}`)
		assert.NoError(t, err)
		assert.Equal(t, "512Mi", spec.resources().MemoryMax())
		assert.Equal(t, "200m", spec.resources().CPUQuota())
		assert.Equal(t, "0-1", spec.resources().CPUSet())
	})
	t.Run("writeback false", func(t *testing.T) {
		spec, err := ParseOverrideSpec(`{"containerName":"app","cpuQuota":"200m","writebackOnDelete":false}`)
		assert.NoError(t, err)
		assert.Equal(t, ptr.To(false), spec.WritebackOnDelete)
		assert.False(t, spec.writebackEnabled())
	})
	t.Run("missing container", func(t *testing.T) {
		_, err := ParseOverrideSpec(`{"memoryMax":"1Gi"}`)
		assert.Error(t, err)
	})
	t.Run("empty resources", func(t *testing.T) {
		_, err := ParseOverrideSpec(`{"containerName":"app"}`)
		assert.Error(t, err)
	})
}

func TestParseOverrideSpecsArray(t *testing.T) {
	specs, err := ParseOverrideSpecs(`[
	  {"containerName":"main","memoryMax":"512Mi"},
	  {"containerName":"sidecar","cpuQuota":"100m"}
	]`)
	assert.NoError(t, err)
	assert.Len(t, specs, 2)
	assert.Equal(t, "main", specs[0].ContainerName)
	assert.Equal(t, "sidecar", specs[1].ContainerName)
}

func TestMemoryMaxToCgroupValue(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	v, err := MemoryMaxToCgroupValue("512Mi")
	assert.NoError(t, err)
	assert.Equal(t, "536870912", v)

	v, err = MemoryMaxToCgroupValue("max")
	assert.NoError(t, err)
	assert.Equal(t, "-1", v)
}

func TestCPUQuotaToCgroupValue(t *testing.T) {
	v, err := CPUQuotaToCgroupValue("200m")
	assert.NoError(t, err)
	assert.Equal(t, "20000", v)

	v, err = CPUQuotaToCgroupValue("1")
	assert.NoError(t, err)
	assert.Equal(t, "100000", v)

	v, err = CPUQuotaToCgroupValueWithPeriod("200m", 100000)
	assert.NoError(t, err)
	assert.Equal(t, "20000", v)
}

func TestApplyAndWritebackMemory(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	podMeta := testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS)
	podMeta.Pod.Annotations = map[string]string{
		apiext.AnnotationContainerCgroupOverride: `{"containerName":"main","memoryMax":"512Mi"}`,
	}

	containerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &podMeta.Pod.Status.ContainerStatuses[1])
	assert.NoError(t, err)

	baselineBytes := "1073741824"
	helper.WriteCgroupFileContents(containerDir, system.MemoryLimit, baselineBytes)
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSQuota, "100000")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSPeriod, "100000")

	si := mockstatesinformer.NewMockStatesInformer(ctrl)
	si.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{podMeta}).AnyTimes()
	si.EXPECT().GetNodeSLO().Return(nil).AnyTimes()

	p := NewPlugin()
	p.statesInformer = si
	p.executor = resourceexecutor.NewResourceUpdateExecutor()
	p.cgroupReader = resourceexecutor.NewCgroupReader()
	stopCh := make(chan struct{})
	defer close(stopCh)
	p.executor.Run(stopCh)

	p.reconcile()
	got := helper.ReadCgroupFileContents(containerDir, system.MemoryLimit)
	assert.Equal(t, "536870912", got)

	key := targetKey{id: "ann/" + string(podMeta.Pod.UID) + "/main"}
	p.mu.Lock()
	assert.Equal(t, baselineBytes, p.baselines[key].MemoryMax())
	p.mu.Unlock()

	delete(podMeta.Pod.Annotations, apiext.AnnotationContainerCgroupOverride)
	p.reconcile()
	got = helper.ReadCgroupFileContents(containerDir, system.MemoryLimit)
	assert.Equal(t, baselineBytes, got)

	p.mu.Lock()
	_, stillActive := p.active[key]
	p.mu.Unlock()
	assert.False(t, stillActive)
}

func TestApplyCPUQuota(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	podMeta := testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS)
	podMeta.Pod.Annotations = map[string]string{
		apiext.AnnotationContainerCgroupOverride: `{"containerName":"main","cpuQuota":"200m"}`,
	}
	containerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &podMeta.Pod.Status.ContainerStatuses[1])
	assert.NoError(t, err)

	helper.WriteCgroupFileContents(containerDir, system.CPUCFSQuota, "100000")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSPeriod, "100000")
	helper.WriteCgroupFileContents(containerDir, system.MemoryLimit, "1073741824")

	si := mockstatesinformer.NewMockStatesInformer(ctrl)
	si.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{podMeta}).AnyTimes()
	si.EXPECT().GetNodeSLO().Return(nil).AnyTimes()

	p := NewPlugin()
	p.statesInformer = si
	p.executor = resourceexecutor.NewResourceUpdateExecutor()
	p.cgroupReader = resourceexecutor.NewCgroupReader()
	stopCh := make(chan struct{})
	defer close(stopCh)
	p.executor.Run(stopCh)

	p.reconcile()
	got := helper.ReadCgroupFileContents(containerDir, system.CPUCFSQuota)
	assert.Equal(t, "20000", got)
}

func TestSkipAnnotation(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	podMeta := testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS)
	podMeta.Pod.Annotations = map[string]string{
		apiext.AnnotationContainerCgroupOverride:     `{"containerName":"main","memoryMax":"512Mi"}`,
		apiext.AnnotationContainerCgroupOverrideSkip: "true",
	}
	containerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &podMeta.Pod.Status.ContainerStatuses[1])
	assert.NoError(t, err)
	helper.WriteCgroupFileContents(containerDir, system.MemoryLimit, "1073741824")

	si := mockstatesinformer.NewMockStatesInformer(ctrl)
	si.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{podMeta}).AnyTimes()
	si.EXPECT().GetNodeSLO().Return(nil).AnyTimes()

	p := NewPlugin()
	p.statesInformer = si
	p.executor = resourceexecutor.NewResourceUpdateExecutor()
	p.cgroupReader = resourceexecutor.NewCgroupReader()
	stopCh := make(chan struct{})
	defer close(stopCh)
	p.executor.Run(stopCh)

	p.reconcile()
	got := helper.ReadCgroupFileContents(containerDir, system.MemoryLimit)
	assert.Equal(t, "1073741824", got)
	assert.Equal(t, 0, len(p.active))
}

func TestPluginInitFlags(t *testing.T) {
	p := NewPlugin()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	p.InitFlags(fs)
	assert.Equal(t, 2*time.Second, p.interval)
}

func TestApplyFromNodeSLOExtensions(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	podMeta := testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS)
	containerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &podMeta.Pod.Status.ContainerStatuses[1])
	assert.NoError(t, err)
	helper.WriteCgroupFileContents(containerDir, system.MemoryLimit, "1073741824")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSQuota, "100000")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSPeriod, "100000")

	nodeSLO := &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			Extensions: &slov1alpha1.ExtensionsMap{
				Object: map[string]interface{}{
					apiext.ExtensionContainerCgroupOverrides: &apiext.NodeCgroupOverrides{
						Items: []apiext.NodeCgroupOverrideItem{{
							Namespace:     "default",
							Name:          "cap",
							PodNamespace:  podMeta.Pod.Namespace,
							PodName:       podMeta.Pod.Name,
							PodUID:        string(podMeta.Pod.UID),
							ContainerName: "main",
							Resources: apiext.ContainerCgroupResources{
								Memory: &apiext.MemoryCgroupOverride{Max: "512Mi"},
							},
							WritebackOnDelete: true,
						}},
					},
				},
			},
		},
	}

	si := mockstatesinformer.NewMockStatesInformer(ctrl)
	si.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{podMeta}).AnyTimes()
	si.EXPECT().GetNodeSLO().Return(nodeSLO).AnyTimes()

	p := NewPlugin()
	p.statesInformer = si
	p.executor = resourceexecutor.NewResourceUpdateExecutor()
	p.cgroupReader = resourceexecutor.NewCgroupReader()
	stopCh := make(chan struct{})
	defer close(stopCh)
	p.executor.Run(stopCh)

	p.reconcile()
	got := helper.ReadCgroupFileContents(containerDir, system.MemoryLimit)
	assert.Equal(t, "536870912", got)

	p.reconcile()
	got = helper.ReadCgroupFileContents(containerDir, system.MemoryLimit)
	assert.Equal(t, "536870912", got)
}

func TestSetupAndRunPlugin(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	si := mockstatesinformer.NewMockStatesInformer(ctrl)
	si.EXPECT().GetAllPods().Return(nil).AnyTimes()
	si.EXPECT().GetNodeSLO().Return(nil).AnyTimes()

	p := NewPlugin()
	p.Setup(nil, nil, si)
	assert.NotNil(t, p.executor)
	assert.NotNil(t, p.cgroupReader)
	assert.NotNil(t, p.statesInformer)

	p.SetupDynamicClient(nil)
	assert.Nil(t, p.dynClient)

	stopCh := make(chan struct{})
	p.Run(stopCh)
	close(stopCh)
	time.Sleep(50 * time.Millisecond)
}

func TestHandlePendingDeleteWriteback(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	podMeta := testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS)
	containerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &podMeta.Pod.Status.ContainerStatuses[1])
	assert.NoError(t, err)
	helper.WriteCgroupFileContents(containerDir, system.MemoryLimit, "1073741824")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSQuota, "100000")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSPeriod, "100000")

	p := NewPlugin()
	p.executor = resourceexecutor.NewResourceUpdateExecutor()
	p.cgroupReader = resourceexecutor.NewCgroupReader()
	stopCh := make(chan struct{})
	defer close(stopCh)
	p.executor.Run(stopCh)

	key := targetKey{id: "cr/default/cap"}
	baseline := apiext.ContainerCgroupResources{
		Memory: &apiext.MemoryCgroupOverride{Max: "1073741824"},
		CPU:    &apiext.CPUCgroupOverride{Quota: "100000"},
	}
	p.mu.Lock()
	p.baselines[key] = baseline
	p.active[key] = activeEntry{
		containerName: "main",
		cgroupParent:  containerDir,
		resources: apiext.ContainerCgroupResources{
			Memory: &apiext.MemoryCgroupOverride{Max: "536870912"},
		},
	}
	p.mu.Unlock()

	item := &apiext.NodeCgroupOverrideItem{
		Namespace:         "default",
		Name:              "cap",
		ContainerName:     "main",
		WritebackOnDelete: true,
		Resources: apiext.ContainerCgroupResources{
			Memory: &apiext.MemoryCgroupOverride{Max: "536870912"},
		},
	}
	err = p.handlePendingDelete(podMeta, item, key)
	assert.NoError(t, err)

	got := helper.ReadCgroupFileContents(containerDir, system.MemoryLimit)
	assert.Equal(t, "1073741824", got)

	p.mu.Lock()
	_, aok := p.active[key]
	_, bok := p.baselines[key]
	p.mu.Unlock()
	assert.False(t, aok, "active entry should be removed after writeback")
	assert.False(t, bok, "baseline should be removed after writeback")
}

func TestResolveBaseline(t *testing.T) {
	p := NewPlugin()
	key := targetKey{id: "ann/x"}
	p.mu.Lock()
	p.baselines[key] = apiext.ContainerCgroupResources{Memory: &apiext.MemoryCgroupOverride{Max: "536870912"}}
	p.mu.Unlock()
	bl := p.resolveBaseline(key, nil)
	assert.Equal(t, "536870912", bl.MemoryMax())

	// stored baseline wins over external baseline
	external := &apiext.ContainerCgroupResources{CPU: &apiext.CPUCgroupOverride{Quota: "200m"}}
	bl = p.resolveBaseline(key, external)
	assert.Equal(t, "536870912", bl.MemoryMax())

	// no stored baseline -> falls back to external
	key2 := targetKey{id: "ann/y"}
	bl = p.resolveBaseline(key2, external)
	assert.Equal(t, "200m", bl.CPUQuota())

	// neither -> empty
	bl = p.resolveBaseline(targetKey{id: "ann/z"}, nil)
	assert.True(t, bl.Empty())
}

func TestWritebackRestore(t *testing.T) {
	p := NewPlugin()
	// empty container dir -> error
	err := p.writeback("", apiext.ContainerCgroupResources{}, apiext.ContainerCgroupResources{})
	assert.Error(t, err)

	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	podMeta := testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS)
	containerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &podMeta.Pod.Status.ContainerStatuses[1])
	assert.NoError(t, err)
	helper.WriteCgroupFileContents(containerDir, system.MemoryLimit, "1073741824")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSQuota, "100000")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSPeriod, "100000")
	helper.WriteCgroupFileContents(containerDir, system.CPUSet, "0-1")

	p.executor = resourceexecutor.NewResourceUpdateExecutor()
	p.cgroupReader = resourceexecutor.NewCgroupReader()
	stopCh := make(chan struct{})
	defer close(stopCh)
	p.executor.Run(stopCh)

	desired := apiext.ContainerCgroupResources{
		Memory: &apiext.MemoryCgroupOverride{Max: "536870912"},
		CPU:    &apiext.CPUCgroupOverride{Quota: "200m", CPUSet: "0-1"},
	}
	baseline := apiext.ContainerCgroupResources{
		Memory: &apiext.MemoryCgroupOverride{Max: "1073741824"},
		CPU:    &apiext.CPUCgroupOverride{Quota: "100000", CPUSet: "0-1"},
	}
	err = p.writeback(containerDir, desired, baseline)
	assert.NoError(t, err)

	assert.Equal(t, "1073741824", helper.ReadCgroupFileContents(containerDir, system.MemoryLimit))
	assert.Equal(t, "100000", helper.ReadCgroupFileContents(containerDir, system.CPUCFSQuota))
}

func TestWritebackMissingNoWriteback(t *testing.T) {
	p := NewPlugin()
	key := targetKey{id: "ann/x"}
	p.mu.Lock()
	p.active[key] = activeEntry{containerName: "main", writeback: false}
	p.baselines[key] = apiext.ContainerCgroupResources{}
	p.mu.Unlock()

	p.writebackMissing(map[targetKey]struct{}{})

	p.mu.Lock()
	_, a := p.active[key]
	_, b := p.baselines[key]
	p.mu.Unlock()
	assert.False(t, a, "entry without writeback should be dropped")
	assert.False(t, b)
}

func TestFormatMemoryLimitForWrite(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)
	assert.Equal(t, "-1", formatMemoryLimitForWrite(-1))
	assert.Equal(t, "536870912", formatMemoryLimitForWrite(536870912))

	helper.SetCgroupsV2(true)
	assert.Equal(t, "max", formatMemoryLimitForWrite(-1))
}

func TestMemoryMaxToCgroupValueV2(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(true)
	v, err := MemoryMaxToCgroupValue("max")
	assert.NoError(t, err)
	assert.Equal(t, "max", v)

	_, err = MemoryMaxToCgroupValue("not-a-quantity")
	assert.Error(t, err)

	// v1 for positive quantity already covered; ensure v2 positive too
	v, err = MemoryMaxToCgroupValue("1Gi")
	assert.NoError(t, err)
	assert.Equal(t, "1073741824", v)
}

func TestCPUQuotaToCgroupValueWithPeriodEdges(t *testing.T) {
	// quota <= 0 -> -1
	v, err := CPUQuotaToCgroupValueWithPeriod("0", 100000)
	assert.NoError(t, err)
	assert.Equal(t, "-1", v)
	// period <= 0 -> default period
	v, err = CPUQuotaToCgroupValueWithPeriod("200m", 0)
	assert.NoError(t, err)
	assert.Equal(t, "20000", v)
	// invalid quantity
	_, err = CPUQuotaToCgroupValueWithPeriod("abc", 100000)
	assert.Error(t, err)
}

func TestParseOverrideSpecsErrors(t *testing.T) {
	_, err := ParseOverrideSpecs("")
	assert.Error(t, err)
	_, err = ParseOverrideSpecs("[]")
	assert.Error(t, err)
	// array with an invalid element (missing containerName)
	_, err = ParseOverrideSpecs(`[{"memoryMax":"1Gi"}]`)
	assert.Error(t, err)
	// invalid JSON
	_, err = ParseOverrideSpecs("not-json")
	assert.Error(t, err)
}

func TestSkipNoOpApply(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	helper.SetCgroupsV2(false)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	podMeta := testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS)
	podMeta.Pod.Annotations = map[string]string{
		apiext.AnnotationContainerCgroupOverride: `{"containerName":"main","memoryMax":"512Mi"}`,
	}
	containerDir, err := koordletutil.GetContainerCgroupParentDir(podMeta.CgroupDir, &podMeta.Pod.Status.ContainerStatuses[1])
	assert.NoError(t, err)
	helper.WriteCgroupFileContents(containerDir, system.MemoryLimit, "536870912")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSQuota, "100000")
	helper.WriteCgroupFileContents(containerDir, system.CPUCFSPeriod, "100000")

	si := mockstatesinformer.NewMockStatesInformer(ctrl)
	si.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{podMeta}).AnyTimes()
	si.EXPECT().GetNodeSLO().Return(nil).AnyTimes()

	p := NewPlugin()
	p.statesInformer = si
	p.executor = resourceexecutor.NewResourceUpdateExecutor()
	p.cgroupReader = resourceexecutor.NewCgroupReader()
	stopCh := make(chan struct{})
	defer close(stopCh)
	p.executor.Run(stopCh)

	p.reconcile()
	got := helper.ReadCgroupFileContents(containerDir, system.MemoryLimit)
	assert.Equal(t, "536870912", got)
	key := targetKey{id: "ann/" + string(podMeta.Pod.UID) + "/main"}
	p.mu.Lock()
	_, ok := p.active[key]
	p.mu.Unlock()
	assert.True(t, ok)
}
