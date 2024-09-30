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

	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/resctrl"
)

func TestCreateResctrlProtocolUpdaterFunc(t *testing.T) {
	type args struct {
		u resctrl.ResctrlUpdater
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Default Resctrl Protocol Updater",
			args: args{u: &DefaultResctrlProtocolUpdater{
				hooksProtocol: &protocol.ContainerContext{},
				group:         "guaranteed",
				schemata:      "MB:0=0xff",
				updateFunc:    nil,
			}},
			wantErr: true,
		},
		{
			name: "Response resctrl is nil",
			args: args{u: &DefaultResctrlProtocolUpdater{
				hooksProtocol: &protocol.PodContext{
					Request: protocol.PodRequest{
						PodMeta: protocol.PodMeta{
							Namespace: "default",
							Name:      "test",
							UID:       "uid",
						},
						Resources: &protocol.Resources{},
					},
					Response: protocol.PodResponse{
						Resources: protocol.Resources{},
					},
				},
				group:      "guaranteed",
				schemata:   "MB:0=0xff",
				updateFunc: nil,
			}},
			wantErr: false,
		},
		{
			name: "Response resctrl is not nil",
			args: args{u: &DefaultResctrlProtocolUpdater{
				hooksProtocol: &protocol.PodContext{
					Request: protocol.PodRequest{
						PodMeta: protocol.PodMeta{
							Namespace: "default",
							Name:      "test",
							UID:       "uid",
						},
						Resources: &protocol.Resources{},
					},
					Response: protocol.PodResponse{
						Resources: protocol.Resources{
							Resctrl: &protocol.Resctrl{
								Schemata:   "",
								Closid:     "",
								NewTaskIds: nil,
							},
						},
					},
				},
				group:      "guaranteed",
				schemata:   "MB:0=0xff",
				updateFunc: nil,
			}},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CreateResctrlProtocolUpdaterFunc(tt.args.u); (err != nil) != tt.wantErr {
				t.Errorf("CreateResctrlProtocolUpdaterFunc() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewCreateResctrlProtocolUpdater(t *testing.T) {
	type args struct {
		hooksProtocol protocol.HooksProtocol
	}
	hooksProtocol := &protocol.PodContext{}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Default Resctrl Protocol Updater",
			args: args{
				hooksProtocol: hooksProtocol,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			NewCreateResctrlProtocolUpdater(tt.args.hooksProtocol)
		})
	}
}

func TestNewRemoveResctrlProtocolUpdater(t *testing.T) {
	type args struct {
		hooksProtocol protocol.HooksProtocol
	}
	hooksProtocol := &protocol.PodContext{}
	tests := []struct {
		name string
		args args
		want resctrl.ResctrlUpdater
	}{
		{
			name: "Default Remove Resctrl Protocol Updater",
			args: args{
				hooksProtocol: hooksProtocol,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			NewRemoveResctrlProtocolUpdater(tt.args.hooksProtocol)
		})
	}
}

func TestNewRemoveResctrlUpdater(t *testing.T) {
	type args struct {
		group string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "New Remove Resctrl Updater",
			args: args{group: "test"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			NewRemoveResctrlUpdater(tt.args.group)
		})
	}
}

func TestRemoveResctrlProtocolUpdaterFunc(t *testing.T) {
	type args struct {
		u resctrl.ResctrlUpdater
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name:    "not pod context",
			args:    args{u: &DefaultResctrlProtocolUpdater{hooksProtocol: &protocol.ContainerContext{}}},
			wantErr: true,
		},
		{
			name: "Default Remove Resctrl Protocol Updater",
			args: args{u: &DefaultResctrlProtocolUpdater{
				hooksProtocol: &protocol.PodContext{
					Request: protocol.PodRequest{
						PodMeta: protocol.PodMeta{
							Namespace: "default",
							Name:      "test",
							UID:       "uid",
						},
						Resources: &protocol.Resources{},
					},
					Response: protocol.PodResponse{
						Resources: protocol.Resources{
							Resctrl: &protocol.Resctrl{
								Schemata:   "",
								Closid:     "",
								NewTaskIds: nil,
							},
						},
					},
				},
				group:      "guaranteed",
				schemata:   "MB:0=0xff",
				updateFunc: nil,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := RemoveResctrlProtocolUpdaterFunc(tt.args.u); (err != nil) != tt.wantErr {
				t.Errorf("RemoveResctrlProtocolUpdaterFunc() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRemoveResctrlUpdaterFunc(t *testing.T) {
	type args struct {
		u resctrl.ResctrlUpdater
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "remove err",
			args: args{u: &DefaultResctrlProtocolUpdater{
				hooksProtocol: &protocol.PodContext{
					Request: protocol.PodRequest{
						PodMeta: protocol.PodMeta{
							Namespace: "default",
							Name:      "test",
							UID:       "uid",
						},
						Resources: &protocol.Resources{},
					},
					Response: protocol.PodResponse{
						Resources: protocol.Resources{
							Resctrl: &protocol.Resctrl{
								Schemata:   "",
								Closid:     "",
								NewTaskIds: nil,
							},
						},
					},
				},
				group:      "guaranteed",
				schemata:   "MB:0=0xff",
				updateFunc: nil,
			}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := RemoveResctrlUpdaterFunc(tt.args.u); (err != nil) != tt.wantErr {
				t.Errorf("RemoveResctrlUpdaterFunc() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
