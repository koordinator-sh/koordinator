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
	"github.com/containerd/nri/pkg/api"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"testing"
)

func TestPodContext_FromNri(t *testing.T) {
	type fields struct {
		Request  PodRequest
		Response PodResponse
		executor resourceexecutor.ResourceUpdateExecutor
	}
	type args struct {
		pod *api.PodSandbox
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "",
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
			name:   "nri done",
			fields: fields{},
			args:   args{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PodContext{
				Request:  tt.fields.Request,
				Response: tt.fields.Response,
				executor: tt.fields.executor,
			}
			p.NriDone(tt.args.executor)
		})
	}
}
