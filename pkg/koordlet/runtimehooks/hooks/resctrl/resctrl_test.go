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

package resctrl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/tools/record"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/hooks"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/resctrl"
)

func TestObject(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		p := Object()
		assert.Equal(t, &plugin{rule: newRule()}, p)
	})
}

func Test_plugin_Register(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		p := newPlugin()
		p.Register(hooks.Options{})
	})
}

func Test_plugin_RemovePodResctrlResources(t *testing.T) {
	type fields struct {
		rule           *Rule
		engine         resctrl.ResctrlEngine
		executor       resourceexecutor.ResourceUpdateExecutor
		statesInformer statesinformer.StatesInformer
		EventRecorder  record.EventRecorder
	}
	type args struct {
		proto protocol.HooksProtocol
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &plugin{
				rule:           tt.fields.rule,
				engine:         tt.fields.engine,
				executor:       tt.fields.executor,
				statesInformer: tt.fields.statesInformer,
				EventRecorder:  tt.fields.EventRecorder,
			}
			if err := p.RemovePodResctrlResources(tt.args.proto); (err != nil) != tt.wantErr {
				t.Errorf("RemovePodResctrlResources() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_plugin_RemoveUnusedResctrlPath(t *testing.T) {
	type fields struct {
		rule           *Rule
		engine         resctrl.ResctrlEngine
		executor       resourceexecutor.ResourceUpdateExecutor
		statesInformer statesinformer.StatesInformer
		EventRecorder  record.EventRecorder
	}
	type args struct {
		protos []protocol.HooksProtocol
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &plugin{
				rule:           tt.fields.rule,
				engine:         tt.fields.engine,
				executor:       tt.fields.executor,
				statesInformer: tt.fields.statesInformer,
				EventRecorder:  tt.fields.EventRecorder,
			}
			if err := p.RemoveUnusedResctrlPath(tt.args.protos); (err != nil) != tt.wantErr {
				t.Errorf("RemoveUnusedResctrlPath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_plugin_SetContainerResctrlResources(t *testing.T) {
	type fields struct {
		rule           *Rule
		engine         resctrl.ResctrlEngine
		executor       resourceexecutor.ResourceUpdateExecutor
		statesInformer statesinformer.StatesInformer
		EventRecorder  record.EventRecorder
	}
	type args struct {
		proto protocol.HooksProtocol
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &plugin{
				rule:           tt.fields.rule,
				engine:         tt.fields.engine,
				executor:       tt.fields.executor,
				statesInformer: tt.fields.statesInformer,
				EventRecorder:  tt.fields.EventRecorder,
			}
			if err := p.SetContainerResctrlResources(tt.args.proto); (err != nil) != tt.wantErr {
				t.Errorf("SetContainerResctrlResources() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_plugin_SetPodResctrlResourcesForHooks(t *testing.T) {
	type fields struct {
		rule           *Rule
		engine         resctrl.ResctrlEngine
		executor       resourceexecutor.ResourceUpdateExecutor
		statesInformer statesinformer.StatesInformer
		EventRecorder  record.EventRecorder
	}
	type args struct {
		proto protocol.HooksProtocol
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &plugin{
				rule:           tt.fields.rule,
				engine:         tt.fields.engine,
				executor:       tt.fields.executor,
				statesInformer: tt.fields.statesInformer,
				EventRecorder:  tt.fields.EventRecorder,
			}
			if err := p.SetPodResctrlResourcesForHooks(tt.args.proto); (err != nil) != tt.wantErr {
				t.Errorf("SetPodResctrlResourcesForHooks() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_plugin_SetPodResctrlResourcesForReconciler(t *testing.T) {
	type fields struct {
		rule           *Rule
		engine         resctrl.ResctrlEngine
		executor       resourceexecutor.ResourceUpdateExecutor
		statesInformer statesinformer.StatesInformer
		EventRecorder  record.EventRecorder
	}
	type args struct {
		proto protocol.HooksProtocol
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &plugin{
				rule:           tt.fields.rule,
				engine:         tt.fields.engine,
				executor:       tt.fields.executor,
				statesInformer: tt.fields.statesInformer,
				EventRecorder:  tt.fields.EventRecorder,
			}
			if err := p.SetPodResctrlResourcesForReconciler(tt.args.proto); (err != nil) != tt.wantErr {
				t.Errorf("SetPodResctrlResourcesForReconciler() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_plugin_UpdatePodTaskIds(t *testing.T) {
	type fields struct {
		rule           *Rule
		engine         resctrl.ResctrlEngine
		executor       resourceexecutor.ResourceUpdateExecutor
		statesInformer statesinformer.StatesInformer
		EventRecorder  record.EventRecorder
	}
	type args struct {
		proto protocol.HooksProtocol
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &plugin{
				rule:           tt.fields.rule,
				engine:         tt.fields.engine,
				executor:       tt.fields.executor,
				statesInformer: tt.fields.statesInformer,
				EventRecorder:  tt.fields.EventRecorder,
			}
			if err := p.UpdatePodTaskIds(tt.args.proto); (err != nil) != tt.wantErr {
				t.Errorf("UpdatePodTaskIds() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_plugin_setPodResctrlResources(t *testing.T) {
	type fields struct {
		rule           *Rule
		engine         resctrl.ResctrlEngine
		executor       resourceexecutor.ResourceUpdateExecutor
		statesInformer statesinformer.StatesInformer
		EventRecorder  record.EventRecorder
	}
	type args struct {
		proto   protocol.HooksProtocol
		fromNRI bool
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &plugin{
				rule:           tt.fields.rule,
				engine:         tt.fields.engine,
				executor:       tt.fields.executor,
				statesInformer: tt.fields.statesInformer,
				EventRecorder:  tt.fields.EventRecorder,
			}
			if err := p.setPodResctrlResources(tt.args.proto, tt.args.fromNRI); (err != nil) != tt.wantErr {
				t.Errorf("setPodResctrlResources() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
