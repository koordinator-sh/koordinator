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

package protocol

import (
	"encoding/json"
	"testing"

	"github.com/containerd/nri/pkg/api"
	"github.com/golang/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/koordinator-sh/koordinator/apis/extension"
	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
)

func TestPodContext_FromNri(t *testing.T) {
	type fields struct {
		Request  PodRequest
		Response PodResponse
		executor resourceexecutor.ResourceUpdateExecutor
	}
	testSpec := &extension.ExtendedResourceSpec{
		Containers: map[string]extension.ExtendedResourceContainerSpec{
			"test-container-1": {
				Requests: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("500"),
					extension.BatchMemory: resource.MustParse("1Gi"),
				},
				Limits: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("500"),
					extension.BatchMemory: resource.MustParse("1Gi"),
				},
			},
			"test-container-2": {
				Requests: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("500"),
					extension.BatchMemory: resource.MustParse("2Gi"),
				},
				Limits: corev1.ResourceList{
					extension.BatchCPU:    resource.MustParse("500"),
					extension.BatchMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	testBytes, _ := json.Marshal(testSpec)
	type args struct {
		pod *api.PodSandbox
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name:   "fromnri GetExtendedResourceSpec failed",
			fields: fields{},
			args: args{
				pod: &api.PodSandbox{
					Linux: &api.LinuxPodSandbox{},
					Annotations: map[string]string{
						"node.koordinator.sh/extended-resource-spec": "test",
					},
				},
			},
		},
		{
			name:   "fromnri success",
			fields: fields{},
			args: args{
				pod: &api.PodSandbox{
					Linux: &api.LinuxPodSandbox{},
					Annotations: map[string]string{
						"node.koordinator.sh/extended-resource-spec": string(testBytes),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PodContext{
				Request:  tt.fields.Request,
				Response: tt.fields.Response,
				executor: tt.fields.executor,
			}
			p.FromNri(tt.args.pod)
		})
	}
}

func TestPodContext_NriDone(t *testing.T) {
	type fields struct {
		Request  PodRequest
		Response PodResponse
		executor resourceexecutor.ResourceUpdateExecutor
	}
	type args struct {
		executor resourceexecutor.ResourceUpdateExecutor
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "nri done",
			fields: fields{
				executor: resourceexecutor.NewTestResourceExecutor(),
			},
			args: args{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PodContext{
				Request:  tt.fields.Request,
				Response: tt.fields.Response,
				executor: tt.fields.executor,
			}
			newStopCh := make(chan struct{})
			defer close(newStopCh)
			p.executor.Run(newStopCh)
			p.NriDone(tt.args.executor)
		})
	}
}

func TestPodContext_NriRemoveDone(t *testing.T) {
	type fields struct {
		Request  PodRequest
		Response PodResponse
		executor resourceexecutor.ResourceUpdateExecutor
	}
	type args struct {
		executor resourceexecutor.ResourceUpdateExecutor
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "nri remove done",
			fields: fields{
				executor: resourceexecutor.NewTestResourceExecutor(),
			},
			args: args{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PodContext{
				Request:  tt.fields.Request,
				Response: tt.fields.Response,
				executor: tt.fields.executor,
			}
			p.NriRemoveDone(tt.args.executor)
		})
	}
}

func TestPodContext_RecordEvent(t *testing.T) {
	type fields struct {
		Request        PodRequest
		Response       PodResponse
		executor       resourceexecutor.ResourceUpdateExecutor
		updaters       []resourceexecutor.ResourceUpdater
		RecorderEvents []RecorderEvent
	}
	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "EventType is not valid",
			fields: fields{
				Request:  PodRequest{},
				Response: PodResponse{},
				executor: nil,
				updaters: nil,
				RecorderEvents: []RecorderEvent{
					{EventType: "test"},
				},
			},
			args: args{
				pod: nil,
			},
		},
		{
			name: "EventType is valid",
			fields: fields{
				Request:  PodRequest{},
				Response: PodResponse{},
				executor: nil,
				updaters: nil,
				RecorderEvents: []RecorderEvent{
					{
						HookName:  "resctrl",
						EventType: corev1.EventTypeNormal,
						MsgFmt:    "test",
						Reason:    "test",
					},
					{
						HookName:  "cpuset",
						EventType: corev1.EventTypeNormal,
						MsgFmt:    "test",
						Reason:    "test",
					},
					{
						HookName:  "resctrl",
						EventType: corev1.EventTypeWarning,
						MsgFmt:    "test",
						Reason:    "test",
					},
				},
			},
		},
	}
	pod := testutil.MockTestPod(apiext.QoSBE, "test_be_pod")
	// env
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	fakeRecorder := &testutil.FakeRecorder{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PodContext{
				Request:        tt.fields.Request,
				Response:       tt.fields.Response,
				executor:       tt.fields.executor,
				updaters:       tt.fields.updaters,
				RecorderEvents: tt.fields.RecorderEvents,
			}
			p.RecordEvent(fakeRecorder, pod)
		})
	}
}
