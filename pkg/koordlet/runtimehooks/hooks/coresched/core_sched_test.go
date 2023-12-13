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

package coresched

import (
	"sync"
	"testing"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func TestPlugin(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		p := newPlugin()
		assert.NotNil(t, p)
		p.Register(hooks.Options{
			Reader: resourceexecutor.NewCgroupReader(),
		})
	})
}

func TestPluginSystemSupported(t *testing.T) {
	type fields struct {
		prepareFn func(helper *sysutil.FileTestUtil)
	}
	type wants struct {
		systemSupported bool
		supportMsg      string
	}
	tests := []struct {
		name   string
		fields fields
		wants  wants
	}{
		{
			name: "plugin unsupported since no sched features file",
			wants: wants{
				systemSupported: false,
				supportMsg:      "core sched not supported",
			},
		},
		{
			name: "plugin unsupported since no core sched in sched features",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C`)
				},
			},
			wants: wants{
				systemSupported: false,
				supportMsg:      "core sched not supported",
			},
		},
		{
			name: "plugin supported since core sched disabled but can be enabled by sysctl",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					sysctlFeaturePath := sysutil.GetProcSysFilePath(sysutil.KernelSchedCore)
					helper.WriteFileContents(sysctlFeaturePath, "0\n")
				},
			},
			wants: wants{
				systemSupported: true,
				supportMsg:      "",
			},
		},
		{
			name: "plugin supported since core sched in sched features",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					featuresPath := sysutil.SchedFeatures.Path("")
					helper.WriteFileContents(featuresPath, `FEATURE_A FEATURE_B FEATURE_C CORE_SCHED`)
				},
			},
			wants: wants{
				systemSupported: true,
			},
		},
		{
			name: "plugin supported since core sched enabled by sysctl",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					sysctlFeaturePath := sysutil.GetProcSysFilePath(sysutil.KernelSchedCore)
					helper.WriteFileContents(sysctlFeaturePath, "1\n")
				},
			},
			wants: wants{
				systemSupported: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}

			p := newPlugin()
			p.Setup(hooks.Options{
				Reader:   resourceexecutor.NewCgroupReader(),
				Executor: resourceexecutor.NewTestResourceExecutor(),
			})
			sysSupported, supportMsg := p.SystemSupported()
			assert.Equal(t, tt.wants.systemSupported, sysSupported)
			assert.Equal(t, tt.wants.supportMsg, supportMsg)
		})
	}
}

func TestPlugin_SetContainerCookie(t *testing.T) {
	type fields struct {
		prepareFn       func(helper *sysutil.FileTestUtil)
		plugin          *Plugin
		preparePluginFn func(p *Plugin)
		cse             sysutil.CoreSchedExtendedInterface
		groupID         string
	}
	type wantFields struct {
		cookieToPIDs  map[uint64][]uint32
		groupToCookie map[string]uint64
	}
	tests := []struct {
		name       string
		fields     fields
		arg        protocol.HooksProtocol
		wantErr    bool
		wantFields wantFields
	}{
		{
			name:    "container context invalid",
			arg:     (*protocol.ContainerContext)(nil),
			wantErr: true,
		},
		{
			name: "invalid cgroup parent",
			fields: fields{
				plugin: newPlugin(),
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					CgroupParent: "",
				},
			},
			wantErr: true,
		},
		{
			name: "missing container ID",
			fields: fields{
				plugin: newPlugin(),
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: true,
		},
		{
			name: "rule has not initialized",
			fields: fields{
				plugin: newPlugin(),
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					ContainerMeta: protocol.ContainerMeta{
						ID: "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
		},
		{
			name: "add cookie for LS container correctly",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(1000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12344: 0,
					12345: 0,
					12346: 0,
				}, map[uint32]uint32{
					1:     1,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
				groupID: "group-xxx-expeller",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						12344,
						12345,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
			},
		},
		{
			name: "failed to add cookie for LS container when core sched add failed",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(1000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12344: 0,
					12345: 0,
					12346: 0,
				}, map[uint32]uint32{
					1:     1,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12344: true,
					12345: true,
					12346: true,
				}),
				groupID: "group-xxx-expeller",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {},
				},
				groupToCookie: map[string]uint64{},
			},
		},
		{
			name: "failed to add cookie for BE container when PIDs no longer exist",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(1000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12344: 0,
					12345: 0,
					12346: 0,
				}, map[uint32]uint32{
					1:     1,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
				groupID: "group-xxx",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs:  map[uint64][]uint32{},
				groupToCookie: map[string]uint64{},
			},
		},
		{
			name: "assign cookie for LS container correctly",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
					p.cookieCache.SetDefault("group-xxx-expeller", newCookieCacheEntry(1000000, 1000, 1001, 1002))
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					1000:  1000000,
					1001:  1000000,
					1002:  1000000,
					12344: 0,
					12345: 0,
					12346: 0,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
				groupID: "group-xxx-expeller",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						1000,
						1001,
						1002,
						12344,
						12345,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
			},
		},
		{
			name: "failed to assign cookie for LS container but fallback to add correctly",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
					p.cookieCache.SetDefault("group-xxx-expeller", newCookieCacheEntry(1000000, 1000, 1001, 1002))
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12344: 0,
					12345: 0,
					12346: 0,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					1000:  true,
					1001:  true,
					1002:  true,
					12346: true,
				}),
				groupID: "group-xxx-expeller",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {},
					2000000: {
						12344,
						12345,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 2000000,
				},
			},
		},
		{
			name: "failed to assign cookie for LS container neither add",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
					p.cookieCache.SetDefault("group-xxx-expeller", newCookieCacheEntry(1000000, 1000, 1001, 1002))
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12344: 0,
					12345: 0,
					12346: 0,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					1000:  true,
					1001:  true,
					1002:  true,
					12344: true,
					12345: true,
					12346: true,
				}),
				groupID: "group-xxx-expeller",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {},
				},
				groupToCookie: map[string]uint64{},
			},
		},
		{
			name: "clear cookie for LS container correctly",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
					p.cookieCache.SetDefault("group-xxx-expeller", newCookieCacheEntry(1000000, 1000, 1001, 1002, 12344))
					p.groupCache.SetDefault("xxxxxx/containerd://yyyyyy", "group-xxx-expeller")
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					1000:  1000000,
					1001:  1000000,
					1002:  1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
				groupID: "group-xxx-expeller",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: slov1alpha1.CoreSchedGroupIDNone,
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						1000,
						1001,
						1002,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
			},
		},
		{
			name: "clear cookie for LSR container correctly",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					p.rule.podQOSParams[extension.QoSLSR] = Param{
						IsPodEnabled: true,
						IsExpeller:   false,
						IsCPUIdle:    false,
					}
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
					p.cookieCache.SetDefault("group-xxx", newCookieCacheEntry(1000000, 1000, 1001, 1002, 12344))
					p.groupCache.SetDefault("xxxxxx/containerd://yyyyyy", "group-xxx")
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					1000:  1000000,
					1001:  1000000,
					1002:  1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
				groupID: "group-xxx",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: slov1alpha1.CoreSchedGroupIDNone,
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLSR),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						1000,
						1001,
						1002,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx": 1000000,
				},
			},
		},
		{
			name: "clear cookie for BE container correctly",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
					p.cookieCache.SetDefault("group-xxx", newCookieCacheEntry(1000000, 1000, 1001, 1002, 12344))
					p.groupCache.SetDefault("xxxxxx/containerd://yyyyyy", "group-xxx")
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					1000:  1000000,
					1001:  1000000,
					1002:  1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
				groupID: "group-xxx",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: slov1alpha1.CoreSchedGroupIDNone,
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						1000,
						1001,
						1002,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx": 1000000,
				},
			},
		},
		{
			name: "failed to clear cookie for LS container when not enabled before",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12344: 0,
					12345: 0,
					12346: 0,
				}, map[uint32]uint32{
					1:     1,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
				groupID: "group-xxx-expeller",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: slov1alpha1.CoreSchedGroupIDNone,
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs:  map[uint64][]uint32{},
				groupToCookie: map[string]uint64{},
			},
		},
		{
			name: "aborted to clear cookie for BE container since PID not found",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
					p.cookieCache.SetDefault("group-xxx", newCookieCacheEntry(1000000, 1000, 1001, 1002))
					p.groupCache.SetDefault("xxxxxx/containerd://yyyyyy", "group-xxx")
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					1000:  1000000,
					1001:  1000000,
					1002:  1000000,
					12344: 0,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12344: true,
					12345: true,
					12346: true,
				}),
				groupID: "group-xxx",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: slov1alpha1.CoreSchedGroupIDNone,
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSBE),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						1000,
						1001,
						1002,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx": 1000000,
				},
			},
		},
		{
			name: "add cookie for LS container migrated between groups",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents("kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope", sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(1000000)
					p.cookieCache.SetDefault("group-yyy-expeller", newCookieCacheEntry(999999, 12344, 12345, 12346))
					p.groupCache.SetDefault("xxxxxx/containerd://yyyyyy", "group-yyy-expeller")
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12344: 999999,
					12345: 999999,
				}, map[uint32]uint32{
					1:     1,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
				groupID: "group-xxx-expeller",
			},
			arg: &protocol.ContainerContext{
				Request: protocol.ContainerRequest{
					PodMeta: protocol.PodMeta{
						Name: "test-pod",
						UID:  "xxxxxx",
					},
					PodAnnotations: map[string]string{
						slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
					},
					PodLabels: map[string]string{
						extension.LabelPodQoS: string(extension.QoSLS),
					},
					ContainerMeta: protocol.ContainerMeta{
						Name: "test-container",
						ID:   "containerd://yyyyyy",
					},
					CgroupParent: "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope",
				},
			},
			wantErr: false,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						12344,
						12345,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			p := tt.fields.plugin
			if tt.fields.cse != nil {
				p.cse = tt.fields.cse
			}
			if tt.fields.preparePluginFn != nil {
				tt.fields.preparePluginFn(p)
			}

			gotErr := p.SetContainerCookie(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			for cookie, pids := range tt.wantFields.cookieToPIDs {
				for _, pid := range pids {
					if tt.fields.cse != nil {
						got, gotErr := tt.fields.cse.Get(sysutil.CoreSchedScopeThread, pid)
						assert.NoError(t, gotErr)
						assert.Equal(t, cookie, got)
					}
				}
			}
			for groupID, cookieID := range tt.wantFields.groupToCookie {
				if cookieID <= 0 {
					_, ok := p.cookieCache.Get(tt.fields.groupID)
					assert.False(t, ok, groupID)
					continue
				}

				entryIf, ok := p.cookieCache.Get(tt.fields.groupID)
				assert.True(t, ok)
				entry, ok := entryIf.(*CookieCacheEntry)
				assert.True(t, ok)
				assert.Equal(t, cookieID, entry.CookieID)
				assert.Equal(t, len(tt.wantFields.cookieToPIDs[cookieID]), len(entry.GetAllPIDs()),
					"expect [%v] but got [%v]", tt.wantFields.cookieToPIDs[cookieID], entry.GetAllPIDs())
				for _, pid := range tt.wantFields.cookieToPIDs[cookieID] {
					assert.True(t, entry.HasPID(pid))
				}
			}
		})
	}
}

func TestPlugin_LoadAllCookies(t *testing.T) {
	type fields struct {
		prepareFn       func(helper *sysutil.FileTestUtil)
		plugin          *Plugin
		preparePluginFn func(p *Plugin)
		cse             sysutil.CoreSchedExtendedInterface
	}
	type wantFields struct {
		cookieToPIDs  map[uint64][]uint32
		groupToCookie map[string]uint64
	}
	tests := []struct {
		name       string
		fields     fields
		arg        []*statesinformer.PodMeta
		want       bool
		wantFields wantFields
	}{
		{
			name: "sync pods failed for no pod PID available",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					sandboxContainerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "")
					containerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
			},
			arg: []*statesinformer.PodMeta{
				{
					CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod",
							UID:  "xxxxxx",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodRunning,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://yyyyyy",
									State: corev1.ContainerState{
										Running: &corev1.ContainerStateRunning{},
									},
								},
							},
						},
					},
				},
				{
					CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod-1",
							UID:  "nnnnnn",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSR),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodFailed,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://mmmmmm",
								},
							},
						},
					},
				},
			},
			want: false,
			wantFields: wantFields{
				cookieToPIDs:  map[uint64][]uint32{},
				groupToCookie: map[string]uint64{},
			},
		},
		{
			name: "sync pods partially correct",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					sandboxContainerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					containerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12345: true,
				}),
			},
			arg: []*statesinformer.PodMeta{
				{
					CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod",
							UID:  "xxxxxx",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodRunning,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://yyyyyy",
									State: corev1.ContainerState{
										Running: &corev1.ContainerStateRunning{},
									},
								},
							},
						},
					},
				},
				{
					CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod-1",
							UID:  "nnnnnn",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSR),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodFailed,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://mmmmmm",
								},
							},
						},
					},
				},
			},
			want: true,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						12340,
						12344,
						12346,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
			},
		},
		{
			name: "sync pods correctly for single pod",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					sandboxContainerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					containerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
				}, map[uint32]bool{
					12346: true,
				}),
			},
			arg: []*statesinformer.PodMeta{
				{
					CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod",
							UID:  "xxxxxx",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodRunning,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://yyyyyy",
									State: corev1.ContainerState{
										Running: &corev1.ContainerStateRunning{},
									},
								},
							},
						},
					},
				},
				{
					CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod-1",
							UID:  "nnnnnn",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSR),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodFailed,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://mmmmmm",
								},
							},
						},
					},
				},
			},
			want: true,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						12340,
						12344,
						12345,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
			},
		},
		{
			name: "sync pods correctly for multiple pods",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					// test-pod
					sandboxContainerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					containerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
					// test-pod-2
					sandboxContainerCgroupDir1, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podcccccc.slice", "containerd://dddddd")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, sysutil.CPUProcs, "32760\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir1, sysutil.CPUProcsV2, "32760\n")
					containerCgroupDir1, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podcccccc.slice", "containerd://zzzzzz")
					helper.WriteCgroupFileContents(containerCgroupDir1, sysutil.CPUProcs, "32768\n32770\n32771\n")
					helper.WriteCgroupFileContents(containerCgroupDir1, sysutil.CPUProcsV2, "32768\n32770\n32771\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
					32760: 1000000,
					32768: 1000000,
					32770: 1000000,
					32772: 1000000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
					32760: 32760,
					32768: 32768,
					32770: 32768,
					32772: 32768,
				}, map[uint32]bool{
					12346: true,
					32771: true,
				}),
			},
			arg: []*statesinformer.PodMeta{
				{
					CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod",
							UID:  "xxxxxx",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSR),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodRunning,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://yyyyyy",
									State: corev1.ContainerState{
										Running: &corev1.ContainerStateRunning{},
									},
								},
							},
						},
					},
				},
				{
					CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod-1",
							UID:  "nnnnnn",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSR),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodFailed,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://mmmmmm",
								},
							},
						},
					},
				},
				{
					CgroupDir: "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-podcccccc.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod-2",
							UID:  "cccccc",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-2",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodRunning,
							QOSClass: corev1.PodQOSBurstable,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container-2",
									ContainerID: "containerd://zzzzzz",
									State: corev1.ContainerState{
										Running: &corev1.ContainerStateRunning{},
									},
								},
							},
						},
					},
				},
			},
			want: true,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						12340,
						12344,
						12345,
						32760,
						32768,
						32770,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
			},
		},
		{
			name: "sync pods correctly for multiple containers with inconsistent cookies",
			fields: fields{
				prepareFn: func(helper *sysutil.FileTestUtil) {
					sandboxContainerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://aaaaaa")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcs, "12340\n")
					helper.WriteCgroupFileContents(sandboxContainerCgroupDir, sysutil.CPUProcsV2, "12340\n")
					containerCgroupDir, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://yyyyyy")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcs, "12344\n12345\n12346\n")
					helper.WriteCgroupFileContents(containerCgroupDir, sysutil.CPUProcsV2, "12344\n12345\n12346\n")
					containerCgroupDir1, _ := util.GetContainerCgroupParentDirByID("kubepods.slice/kubepods-podxxxxxx.slice", "containerd://zzzzzz")
					helper.WriteCgroupFileContents(containerCgroupDir1, sysutil.CPUProcs, "12350\n")
					helper.WriteCgroupFileContents(containerCgroupDir1, sysutil.CPUProcsV2, "12350\n")
				},
				plugin: testGetEnabledPlugin(),
				preparePluginFn: func(p *Plugin) {
					f := p.cse.(*sysutil.FakeCoreSchedExtended)
					f.SetNextCookieID(2000000)
				},
				cse: sysutil.NewFakeCoreSchedExtended(map[uint32]uint64{
					1:     0,
					10:    0,
					12340: 1000000,
					12344: 1000000,
					12345: 1000000,
					12346: 1000000,
					12350: 1100000,
				}, map[uint32]uint32{
					1:     1,
					1000:  1000,
					1001:  1001,
					1002:  1001,
					12340: 12340,
					12344: 12344,
					12345: 12344,
					12346: 12346,
					12350: 12350,
				}, map[uint32]bool{
					12346: true,
				}),
			},
			arg: []*statesinformer.PodMeta{
				{
					CgroupDir: "kubepods.slice/kubepods-podxxxxxx.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod",
							UID:  "xxxxxx",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-xxx",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLS),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("2"),
											corev1.ResourceMemory: resource.MustParse("4Gi"),
										},
									},
								}, {
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("1Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("1Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodRunning,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://yyyyyy",
									State: corev1.ContainerState{
										Running: &corev1.ContainerStateRunning{},
									},
								}, {
									Name:        "test-container-1",
									ContainerID: "containerd://zzzzzz",
									State: corev1.ContainerState{
										Running: &corev1.ContainerStateRunning{},
									},
								},
							},
						},
					},
				},
				{
					CgroupDir: "kubepods.slice/kubepods-podnnnnnn.slice",
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-pod-1",
							UID:  "nnnnnn",
							Annotations: map[string]string{
								slov1alpha1.AnnotationCoreSchedGroupID: "group-nnn",
							},
							Labels: map[string]string{
								extension.LabelPodQoS: string(extension.QoSLSR),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:    corev1.PodFailed,
							QOSClass: corev1.PodQOSGuaranteed,
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container",
									ContainerID: "containerd://mmmmmm",
								},
							},
						},
					},
				},
			},
			want: true,
			wantFields: wantFields{
				cookieToPIDs: map[uint64][]uint32{
					1000000: {
						12340,
						12344,
						12345,
					},
					0: {
						12350,
					},
				},
				groupToCookie: map[string]uint64{
					"group-xxx-expeller": 1000000,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.prepareFn != nil {
				tt.fields.prepareFn(helper)
			}
			p := tt.fields.plugin
			if tt.fields.cse != nil {
				p.cse = tt.fields.cse
			}
			if tt.fields.preparePluginFn != nil {
				tt.fields.preparePluginFn(p)
			}

			got := p.LoadAllCookies(tt.arg)
			assert.Equal(t, tt.want, got)
			for groupID, cookieID := range tt.wantFields.groupToCookie {
				if cookieID <= 0 {
					_, ok := p.cookieCache.Get(groupID)
					assert.False(t, ok, groupID)
					continue
				}

				entryIf, ok := p.cookieCache.Get(groupID)
				assert.True(t, ok)
				entry, ok := entryIf.(*CookieCacheEntry)
				assert.True(t, ok)
				assert.Equal(t, cookieID, entry.CookieID)
				assert.Equal(t, len(tt.wantFields.cookieToPIDs[cookieID]), len(entry.GetAllPIDs()),
					"expect [%v] but got [%v]", tt.wantFields.cookieToPIDs[cookieID], entry.GetAllPIDs())
				for _, pid := range tt.wantFields.cookieToPIDs[cookieID] {
					assert.True(t, entry.HasPID(pid), pid)
				}
			}
		})
	}
}

func TestPlugin_SetKubeQOSCPUIdle(t *testing.T) {
	type fields struct {
		rule *Rule
	}
	tests := []struct {
		name      string
		fields    fields
		arg       protocol.HooksProtocol
		wantErr   bool
		wantField *protocol.KubeQOSContext
	}{
		{
			name:    "nil context",
			arg:     (*protocol.KubeQOSContext)(nil),
			wantErr: true,
		},
		{
			name: "rule not inited",
			fields: fields{
				rule: newRule(),
			},
			arg: &protocol.KubeQOSContext{
				Request: protocol.KubeQOSRequet{
					KubeQOSClass: corev1.PodQOSBurstable,
					CgroupParent: "kubepods.slice/kubepods-burstable.slice",
				},
			},
			wantErr: false,
			wantField: &protocol.KubeQOSContext{
				Request: protocol.KubeQOSRequet{
					KubeQOSClass: corev1.PodQOSBurstable,
					CgroupParent: "kubepods.slice/kubepods-burstable.slice",
				},
			},
		},
		{
			name: "cpu idle disabled",
			fields: fields{
				rule: testGetDisabledRule(),
			},
			arg: &protocol.KubeQOSContext{
				Request: protocol.KubeQOSRequet{
					KubeQOSClass: corev1.PodQOSBurstable,
					CgroupParent: "kubepods.slice/kubepods-burstable.slice",
				},
			},
			wantErr: false,
			wantField: &protocol.KubeQOSContext{
				Request: protocol.KubeQOSRequet{
					KubeQOSClass: corev1.PodQOSBurstable,
					CgroupParent: "kubepods.slice/kubepods-burstable.slice",
				},
				Response: protocol.KubeQOSResponse{
					Resources: protocol.Resources{
						CPUIdle: pointer.Int64(0),
					},
				},
			},
		},
		{
			name: "cpu idle enabled",
			fields: fields{
				rule: testGetAllEnabledRule(),
			},
			arg: &protocol.KubeQOSContext{
				Request: protocol.KubeQOSRequet{
					KubeQOSClass: corev1.PodQOSBestEffort,
					CgroupParent: "kubepods.slice/kubepods-besteffort.slice",
				},
			},
			wantErr: false,
			wantField: &protocol.KubeQOSContext{
				Request: protocol.KubeQOSRequet{
					KubeQOSClass: corev1.PodQOSBestEffort,
					CgroupParent: "kubepods.slice/kubepods-besteffort.slice",
				},
				Response: protocol.KubeQOSResponse{
					Resources: protocol.Resources{
						CPUIdle: pointer.Int64(1),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin()
			p.rule = tt.fields.rule
			gotErr := p.SetKubeQOSCPUIdle(tt.arg)
			assert.Equal(t, tt.wantErr, gotErr != nil, gotErr)
			if !tt.wantErr {
				assert.Equal(t, tt.wantField, tt.arg)
			}
		})
	}
}

func testGetEnabledPlugin() *Plugin {
	return &Plugin{
		rule:            testGetEnabledRule(),
		cookieCache:     gocache.New(defaultCacheExpiration, defaultCacheDeleteInterval),
		groupCache:      gocache.New(defaultCacheExpiration, defaultCacheDeleteInterval),
		reader:          resourceexecutor.NewCgroupReader(),
		executor:        resourceexecutor.NewTestResourceExecutor(),
		sysSupported:    pointer.Bool(true),
		allPodsSyncOnce: sync.Once{},
		initialized:     atomic.NewBool(true),
	}
}
