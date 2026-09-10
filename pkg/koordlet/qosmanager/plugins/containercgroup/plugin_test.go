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
