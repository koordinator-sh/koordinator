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

package oomscoreadj

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	sysutil "github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const testContainerCgroupParent = "kubepods.slice/kubepods-podxxxxxx.slice/cri-containerd-yyyyyy.scope"

func newTestPlugin(fake sysutil.OOMScoreAdjInterface) *Plugin {
	p := newPlugin()
	p.Setup(hooks.Options{
		Reader:   resourceexecutor.NewCgroupReader(),
		Executor: resourceexecutor.NewTestResourceExecutor(),
	})
	p.oomScoreAdjOperator = fake
	return p
}

func newTestContainerCtx(annotations map[string]string, sandbox bool) *protocol.ContainerContext {
	containerCtx := &protocol.ContainerContext{}
	containerCtx.Request.PodMeta = protocol.PodMeta{
		Namespace: "test-ns",
		Name:      "test-pod",
		UID:       "xxxxxx",
	}
	containerCtx.Request.ContainerMeta = protocol.ContainerMeta{
		Name:    "c1",
		ID:      "containerd://yyyyyy",
		Sandbox: sandbox,
	}
	containerCtx.Request.PodAnnotations = annotations
	containerCtx.Request.CgroupParent = testContainerCgroupParent
	return containerCtx
}

func TestObject(t *testing.T) {
	assert.NotNil(t, Object())
}

func TestPlugin_SetContainerOOMScoreAdj(t *testing.T) {
	type fields struct {
		pidToVal map[uint32]int64
		pidToErr map[uint32]bool
		procs    string
	}
	type args struct {
		annotations map[string]string
		sandbox     bool
	}
	tests := []struct {
		name     string
		fields   fields
		args     args
		wantVals map[uint32]int64
	}{
		{
			name: "no annotation is a no-op",
			fields: fields{
				pidToVal: map[uint32]int64{12: 0, 13: 0},
				procs:    "12\n13\n",
			},
			args: args{
				annotations: nil,
			},
			wantVals: map[uint32]int64{12: 0, 13: 0},
		},
		{
			name: "pod-level default applied to all pids",
			fields: fields{
				pidToVal: map[uint32]int64{12: 0, 13: -500},
				procs:    "12\n13\n",
			},
			args: args{
				annotations: map[string]string{
					extension.AnnotationOOMScoreAdj: "-500",
				},
			},
			wantVals: map[uint32]int64{12: -500, 13: -500},
		},
		{
			name: "container spec overrides pod-level default",
			fields: fields{
				pidToVal: map[uint32]int64{12: 0},
				procs:    "12\n",
			},
			args: args{
				annotations: map[string]string{
					extension.AnnotationOOMScoreAdj:     "-500",
					extension.AnnotationOOMScoreAdjSpec: `{"c1": 200}`,
				},
			},
			wantVals: map[uint32]int64{12: 200},
		},
		{
			name: "out-of-range value is rejected",
			fields: fields{
				pidToVal: map[uint32]int64{12: 0},
				procs:    "12\n",
			},
			args: args{
				annotations: map[string]string{
					extension.AnnotationOOMScoreAdj: "-2000",
				},
			},
			wantVals: map[uint32]int64{12: 0},
		},
		{
			name: "invalid annotation is a no-op",
			fields: fields{
				pidToVal: map[uint32]int64{12: 0},
				procs:    "12\n",
			},
			args: args{
				annotations: map[string]string{
					extension.AnnotationOOMScoreAdjSpec: `{invalid}`,
				},
			},
			wantVals: map[uint32]int64{12: 0},
		},
		{
			name: "sandbox container is skipped",
			fields: fields{
				pidToVal: map[uint32]int64{12: 0},
				procs:    "12\n",
			},
			args: args{
				annotations: map[string]string{
					extension.AnnotationOOMScoreAdj: "-500",
				},
				sandbox: true,
			},
			wantVals: map[uint32]int64{12: 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := sysutil.NewFileTestUtil(t)
			defer helper.Cleanup()
			helper.WriteCgroupFileContents(testContainerCgroupParent, sysutil.CPUProcs, tt.fields.procs)
			helper.WriteCgroupFileContents(testContainerCgroupParent, sysutil.CPUProcsV2, tt.fields.procs)

			fake := sysutil.NewFakeOOMScoreAdj(tt.fields.pidToVal, tt.fields.pidToErr)
			p := newTestPlugin(fake)

			containerCtx := newTestContainerCtx(tt.args.annotations, tt.args.sandbox)
			err := p.SetContainerOOMScoreAdj(containerCtx)
			assert.NoError(t, err)

			for pid, wantVal := range tt.wantVals {
				gotVal, err := fake.Get(pid)
				assert.NoError(t, err)
				assert.Equal(t, wantVal, gotVal, "pid %d value", pid)
			}
		})
	}
}

func TestPlugin_SetContainerOOMScoreAdj_invalidRequest(t *testing.T) {
	fake := sysutil.NewFakeOOMScoreAdj(nil, nil)
	p := newTestPlugin(fake)

	t.Run("invalid cgroup parent", func(t *testing.T) {
		containerCtx := newTestContainerCtx(nil, false)
		containerCtx.Request.CgroupParent = ""
		err := p.SetContainerOOMScoreAdj(containerCtx)
		assert.Error(t, err)
	})

	t.Run("empty container ID", func(t *testing.T) {
		containerCtx := newTestContainerCtx(map[string]string{
			extension.AnnotationOOMScoreAdj: "-500",
		}, false)
		containerCtx.Request.ContainerMeta.ID = ""
		err := p.SetContainerOOMScoreAdj(containerCtx)
		assert.NoError(t, err)
	})
}
