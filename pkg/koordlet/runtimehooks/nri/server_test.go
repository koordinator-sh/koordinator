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

package nri

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

type mockPlugin struct{}

func (p *mockPlugin) Register(op hooks.Options) {
	hooks.Register(config.PreRemoveRunPodSandbox, "mockPlugin", "mockPlugin remove", p.Remove)
}

func (p *mockPlugin) Remove(proto protocol.HooksProtocol) error {
	return fmt.Errorf("mock error")
}

func getDisableStagesMap(stagesSlice []string) map[string]struct{} {
	stagesMap := map[string]struct{}{}
	for _, item := range stagesSlice {
		if _, ok := stagesMap[item]; !ok {
			stagesMap[item] = struct{}{}
		}
	}
	return stagesMap
}

func TestNriServer_Start(t *testing.T) {
	type fields struct {
		stub            stub.Stub
		mask            stub.EventMask
		options         Options
		runPodSandbox   func(*NriServer, *api.PodSandbox, *api.Container) error
		createContainer func(*NriServer, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
		updateContainer func(*NriServer, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name: "stub is nil",
			fields: fields{
				stub: nil,
				mask: api.EventMask(1),
				options: Options{
					NriConnectTimeout:   time.Second,
					PluginFailurePolicy: "Ignore",
					DisableStages:       getDisableStagesMap([]string{"PreRunPodSandbox"}),
					Executor:            nil,
				},
			},
			wantErr: true,
		},
		{
			fields: fields{
				stub: nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			s := &NriServer{
				stub:    tt.fields.stub,
				mask:    tt.fields.mask,
				options: tt.fields.options,
			}

			if err := s.Start(); (err != nil) != tt.wantErr {
				t.Errorf("Start() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewNriServer(t *testing.T) {
	type fields struct {
		isNriSocketExist bool
	}
	type args struct {
		opt Options
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *NriServer
		wantErr bool
	}{
		{
			name: "nri socket not exit",
			fields: fields{
				isNriSocketExist: false,
			},
			args: args{opt: Options{
				NriSocketPath:       "nri/nri.sock",
				PluginFailurePolicy: "Ignore",
				DisableStages:       getDisableStagesMap([]string{"PreRunPodSandbox"}),
				Executor:            nil,
			}},
			wantErr: true,
		},
		{
			name: "new a nri server",
			fields: fields{
				isNriSocketExist: true,
			},
			args: args{opt: Options{
				NriSocketPath:       "nri/nri.sock",
				PluginFailurePolicy: "Ignore",
				DisableStages:       getDisableStagesMap([]string{"PreRunPodSandbox"}),
				Executor:            nil,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)
			defer helper.Cleanup()
			if tt.fields.isNriSocketExist {
				helper.WriteFileContents("nri/nri.sock", "")
			}

			_, err := NewNriServer(tt.args.opt)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewNriServer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestNriServer_Configure(t *testing.T) {
	type fields struct {
		stub            stub.Stub
		mask            stub.EventMask
		options         Options
		runPodSandbox   func(*NriServer, *api.PodSandbox, *api.Container) error
		createContainer func(*NriServer, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
		updateContainer func(*NriServer, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
	}
	type args struct {
		config  string
		runtime string
		version string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    stub.EventMask
		wantErr bool
	}{
		{
			name: "config is empty",
			fields: fields{
				stub: nil,
				mask: 0,
				options: Options{
					PluginFailurePolicy: "Ignore",
					DisableStages:       getDisableStagesMap([]string{"PreRunPodSandbox"}),
					Executor:            nil},
				runPodSandbox:   nil,
				createContainer: nil,
				updateContainer: nil,
			},
			args: args{
				config:  "",
				runtime: "",
				version: "",
			},
		},
		{
			name: "config unmarshal error",
			fields: fields{
				stub: nil,
				mask: 0,
				options: Options{
					PluginFailurePolicy: "Ignore",
					DisableStages:       getDisableStagesMap([]string{"PreRunPodSandbox"}),
					Executor:            nil},
				runPodSandbox:   nil,
				createContainer: nil,
				updateContainer: nil,
			},
			args: args{
				config:  "{error: error}",
				runtime: "",
				version: "",
			},
		}, {
			name: "config parse success",
			fields: fields{
				stub: nil,
				mask: 0,
				options: Options{
					PluginFailurePolicy: "Ignore",
					DisableStages:       getDisableStagesMap([]string{"PreRunPodSandbox"}),
					Executor:            nil},
				runPodSandbox:   nil,
				createContainer: nil,
				updateContainer: nil,
			},
			args: args{
				config:  "events: [\"RunPodSandbox\"]",
				runtime: "",
				version: "",
			},
		},
		{
			name: "config parse failed",
			fields: fields{
				stub: nil,
				mask: 0,
				options: Options{
					PluginFailurePolicy: "Ignore",
					DisableStages:       getDisableStagesMap([]string{"PreRunPodSandbox"}),
					Executor:            nil,
				},
				runPodSandbox:   nil,
				createContainer: nil,
				updateContainer: nil,
			},
			args: args{
				config:  "events: [\"RunPodSandboxTest\"]",
				runtime: "",
				version: "",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &NriServer{
				stub:    tt.fields.stub,
				mask:    tt.fields.mask,
				options: tt.fields.options,
			}
			_, err := p.Configure(tt.args.config, tt.args.runtime, tt.args.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("Configure() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestNriServer_Synchronize(t *testing.T) {
	type fields struct {
		stub            stub.Stub
		mask            stub.EventMask
		options         Options
		runPodSandbox   func(*NriServer, *api.PodSandbox, *api.Container) error
		createContainer func(*NriServer, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
		updateContainer func(*NriServer, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
	}
	type args struct {
		pods       []*api.PodSandbox
		containers []*api.Container
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []*api.ContainerUpdate
		wantErr bool
	}{
		{
			name: "synchronize nil pods and containers",
			fields: fields{
				stub: nil,
				mask: 0,
			},
			args: args{
				pods:       nil,
				containers: nil,
			},
			want:    nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &NriServer{
				stub:    tt.fields.stub,
				mask:    tt.fields.mask,
				options: tt.fields.options,
			}
			got, err := p.Synchronize(tt.args.pods, tt.args.containers)
			if (err != nil) != tt.wantErr {
				t.Errorf("Synchronize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Synchronize() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNriServer_RunPodSandbox(t *testing.T) {
	type fields struct {
		stub            stub.Stub
		mask            stub.EventMask
		options         Options
		runPodSandbox   func(*NriServer, *api.PodSandbox, *api.Container) error
		createContainer func(*NriServer, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
		updateContainer func(*NriServer, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
	}
	pod := &api.PodSandbox{
		Id:          "test",
		Name:        "test",
		Uid:         "test",
		Namespace:   "test",
		Labels:      nil,
		Annotations: nil,
		Linux: &api.LinuxPodSandbox{
			PodOverhead:  nil,
			PodResources: nil,
			CgroupParent: "",
			CgroupsPath:  "",
			Namespaces:   nil,
			Resources:    nil,
		},
		Pid: 0,
	}
	type args struct {
		pod *api.PodSandbox
	}
	mask, _ := api.ParseEventMask(events)
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "RunPodSandbox success",
			fields: fields{
				stub: nil,
				mask: mask,
				options: Options{
					PluginFailurePolicy: config.PolicyIgnore,
					DisableStages:       getDisableStagesMap([]string{"PreRunPodSandbox"}),
					Executor:            resourceexecutor.NewTestResourceExecutor(),
				},
			},
			args: args{
				pod: pod,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &NriServer{
				stub:    tt.fields.stub,
				mask:    tt.fields.mask,
				options: tt.fields.options,
			}

			if err := p.RunPodSandbox(tt.args.pod); (err != nil) != tt.wantErr {
				t.Errorf("RunPodSandbox() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNriServer_CreateContainer(t *testing.T) {
	type fields struct {
		stub            stub.Stub
		mask            stub.EventMask
		options         Options
		runPodSandbox   func(*NriServer, *api.PodSandbox, *api.Container) error
		createContainer func(*NriServer, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
		updateContainer func(*NriServer, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
	}
	type args struct {
		pod       *api.PodSandbox
		container *api.Container
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *api.ContainerAdjustment
		want1   []*api.ContainerUpdate
		wantErr bool
	}{
		{
			name: "CreateContainer success",
			fields: fields{
				options: Options{
					Executor: resourceexecutor.NewTestResourceExecutor(),
				},
			},
			args: args{
				pod: &api.PodSandbox{
					Id:        "test",
					Name:      "test",
					Uid:       "test",
					Namespace: "test",
					Linux: &api.LinuxPodSandbox{
						CgroupParent: "",
						CgroupsPath:  "",
						Namespaces:   nil,
					},
					Pid: 0,
				},
			},
			want:    nil,
			want1:   nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &NriServer{
				stub:    tt.fields.stub,
				mask:    tt.fields.mask,
				options: tt.fields.options,
			}
			newStopCh := make(chan struct{})
			defer close(newStopCh)
			p.options.Executor.Run(newStopCh)
			_, _, err := p.CreateContainer(tt.args.pod, tt.args.container)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateContainer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestNriServer_UpdateContainer(t *testing.T) {
	type fields struct {
		stub            stub.Stub
		mask            stub.EventMask
		options         Options
		runPodSandbox   func(*NriServer, *api.PodSandbox, *api.Container) error
		createContainer func(*NriServer, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
		updateContainer func(*NriServer, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
	}
	type args struct {
		pod       *api.PodSandbox
		container *api.Container
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []*api.ContainerUpdate
		wantErr bool
	}{
		{
			name: "CreateContainer success",
			fields: fields{
				options: Options{
					Executor: resourceexecutor.NewTestResourceExecutor(),
				},
			},
			args: args{
				pod: &api.PodSandbox{
					Id:        "test",
					Name:      "test",
					Uid:       "test",
					Namespace: "test",
					Linux: &api.LinuxPodSandbox{
						CgroupParent: "",
						CgroupsPath:  "",
						Namespaces:   nil,
					},
					Pid: 0,
				},
			},
			want:    nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &NriServer{
				stub:    tt.fields.stub,
				mask:    tt.fields.mask,
				options: tt.fields.options,
			}
			_, err := p.UpdateContainer(tt.args.pod, tt.args.container)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateContainer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestNriServer_RemovePodSandbox(t *testing.T) {
	type fields struct {
		stub            stub.Stub
		mask            stub.EventMask
		options         Options
		runPodSandbox   func(*NriServer, *api.PodSandbox, *api.Container) error
		createContainer func(*NriServer, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
		updateContainer func(*NriServer, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
		plugin          *mockPlugin
	}
	pod := &api.PodSandbox{
		Id:          "test",
		Name:        "test",
		Uid:         "test",
		Namespace:   "test",
		Labels:      nil,
		Annotations: nil,
		Linux: &api.LinuxPodSandbox{
			PodOverhead:  nil,
			PodResources: nil,
			CgroupParent: "",
			CgroupsPath:  "",
			Namespaces:   nil,
			Resources:    nil,
		},
		Pid: 0,
	}
	type args struct {
		pod *api.PodSandbox
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "RemovePodSandbox success",
			fields: fields{
				stub: nil,
				options: Options{
					PluginFailurePolicy: config.PolicyIgnore,
					DisableStages:       getDisableStagesMap([]string{"PreRemovePodSandbox"}),
					Executor:            resourceexecutor.NewTestResourceExecutor(),
				},
			},
			args: args{
				pod: pod,
			},
			wantErr: false,
		},
		{
			name: "RemovePodSandbox fail",
			fields: fields{
				stub: nil,
				options: Options{
					PluginFailurePolicy: config.PolicyFail,
					DisableStages:       getDisableStagesMap([]string{"PreRemovePodSandbox"}),
					Executor:            resourceexecutor.NewTestResourceExecutor(),
				},
				plugin: &mockPlugin{},
			},
			args: args{
				pod: pod,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &NriServer{
				stub:    tt.fields.stub,
				mask:    tt.fields.mask,
				options: tt.fields.options,
			}
			if tt.fields.plugin != nil {
				tt.fields.plugin.Register(hooks.Options{})
			}
			if err := p.RemovePodSandbox(tt.args.pod); (err != nil) != tt.wantErr {
				t.Errorf("RemovePodSandbox() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
