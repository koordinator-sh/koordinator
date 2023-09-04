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

package reconciler

import (
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/tools/cache"

	ext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/runtimehooks/protocol"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

func Test_hostReconciler_parseHostAppAndGet(t *testing.T) {
	type fields struct {
		hostAppMap map[string]*slov1alpha1.HostApplicationSpec
	}
	type args struct {
		hostApps []slov1alpha1.HostApplicationSpec
	}
	type wants struct {
		hostApp map[string]*slov1alpha1.HostApplicationSpec
		updated bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		wants  wants
	}{
		{
			name: "update app with new",
			fields: fields{
				hostAppMap: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name:       "test-app",
						CgroupPath: nil,
					},
				},
			},
			args: args{
				hostApps: []slov1alpha1.HostApplicationSpec{
					{
						Name:       "new-app",
						CgroupPath: nil,
					},
				},
			},
			wants: wants{
				hostApp: map[string]*slov1alpha1.HostApplicationSpec{
					"new-app": {
						Name:       "new-app",
						CgroupPath: nil,
					},
				},
				updated: true,
			},
		},
		{
			name: "update app with same",
			fields: fields{
				hostAppMap: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name:       "test-app",
						CgroupPath: nil,
					},
				},
			},
			args: args{
				hostApps: []slov1alpha1.HostApplicationSpec{
					{
						Name:       "test-app",
						CgroupPath: nil,
					},
				},
			},
			wants: wants{
				hostApp: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name:       "test-app",
						CgroupPath: nil,
					},
				},
				updated: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &hostReconciler{
				hostAppMap: tt.fields.hostAppMap,
			}
			gotUpdated := r.parseHostApp(tt.args.hostApps)
			assert.Equal(t, tt.wants.updated, gotUpdated)
			if gotUpdated {
				gotApps := r.getHostApps()
				assert.Equal(t, tt.wants.hostApp, gotApps)
			}
		})
	}
}

func Test_hostReconciler_doHostAppCgroup(t *testing.T) {
	stopCh := make(chan struct{}, 1)
	stopFn := func() {
		close(stopCh)
	}

	hostAppOutput := map[string]string{}
	hostAppReconcilerGetQoSFn := func(proto protocol.HooksProtocol) error {
		appCtx := proto.(*protocol.HostAppContext)
		qos := appCtx.Request.QOSClass
		hostAppOutput[appCtx.Request.Name] = string(qos)
		stopFn()
		return nil
	}
	badReconcilerFn := func(proto protocol.HooksProtocol) error {
		return fmt.Errorf("mock reconcile error")
	}

	type fields struct {
		hostAppMap     map[string]*slov1alpha1.HostApplicationSpec
		registerFn     reconcileFunc
		registerFnDesc string
	}
	type wants struct {
		hostAppOutput map[string]string
	}
	tests := []struct {
		name   string
		fields fields
		wants  wants
	}{
		{
			name: "reconcile host app to get qos",
			fields: fields{
				hostAppMap: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name: "test-app",
						QoS:  ext.QoSLS,
					},
				},
				registerFn:     hostAppReconcilerGetQoSFn,
				registerFnDesc: "get host app qos",
			},
			wants: wants{
				hostAppOutput: map[string]string{
					"test-app": string(ext.QoSLS),
				},
			},
		},
		{
			name: "reconcile host app with error",
			fields: fields{
				hostAppMap: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name: "test-app",
						QoS:  ext.QoSLS,
					},
				},
				registerFn:     badReconcilerFn,
				registerFnDesc: "get host app qos",
			},
			wants: wants{
				hostAppOutput: map[string]string{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &hostReconciler{
				hostAppMap: tt.fields.hostAppMap,
				appUpdated: make(chan struct{}, 1),
				executor:   resourceexecutor.NewResourceUpdateExecutor(),
			}
			RegisterHostAppReconciler(system.CPUBVTWarpNs, tt.fields.registerFnDesc, tt.fields.registerFn, &ReconcilerOption{})
			defer func() {
				globalHostAppReconcilers.hostApps = []*hostAppReconciler{}
				hostAppOutput = map[string]string{}
			}()
			r.appUpdated <- struct{}{}
			r.doHostAppCgroup(stopCh)
			assert.Equal(t, tt.wants.hostAppOutput, hostAppOutput)

		})
	}
}

func Test_hostReconciler_appRefreshCallback(t *testing.T) {
	type args struct {
		target         *statesinformer.CallbackTarget
		currentHostApp map[string]*slov1alpha1.HostApplicationSpec
	}
	type wants struct {
		hostApp    map[string]*slov1alpha1.HostApplicationSpec
		appUpdated bool
	}
	tests := []struct {
		name  string
		args  args
		wants wants
	}{
		{
			name: "update app with new",
			args: args{
				target: &statesinformer.CallbackTarget{
					HostApplications: []slov1alpha1.HostApplicationSpec{
						{
							Name: "new-app",
						},
					},
				},
				currentHostApp: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name: "test-app",
					},
				},
			},
			wants: wants{
				hostApp: map[string]*slov1alpha1.HostApplicationSpec{
					"new-app": {
						Name: "new-app",
					},
				},
				appUpdated: true,
			},
		},
		{
			name: "update app with old",
			args: args{
				target: &statesinformer.CallbackTarget{
					HostApplications: []slov1alpha1.HostApplicationSpec{
						{
							Name: "test-app",
						},
					},
				},
				currentHostApp: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name: "test-app",
					},
				},
			},
			wants: wants{
				hostApp: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name: "test-app",
					},
				},
				appUpdated: false,
			},
		},
		{
			name: "update app with nil",
			args: args{
				target: nil,
				currentHostApp: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name: "test-app",
					},
				},
			},
			wants: wants{
				hostApp: map[string]*slov1alpha1.HostApplicationSpec{
					"test-app": {
						Name: "test-app",
					},
				},
				appUpdated: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &hostReconciler{
				appUpdated: make(chan struct{}, 1),
				hostAppMap: tt.args.currentHostApp,
			}
			r.appRefreshCallback(statesinformer.RegisterTypeNodeSLOSpec, nil, tt.args.target)
			assert.Equal(t, tt.wants.hostApp, r.getHostApps())
			assert.Equal(t, tt.wants.appUpdated, len(r.appUpdated) == 1)
		})
	}
}

func Test_hostReconciler_reconcile(t *testing.T) {
	r := &hostReconciler{
		appUpdated:        make(chan struct{}, 1),
		reconcileInterval: time.Second,
	}
	stopCh := make(chan struct{}, 1)
	defer close(stopCh)
	go r.reconcile(stopCh)
	checkAppUpdated := func() bool {
		return len(r.appUpdated) == 1
	}
	assert.True(t, cache.WaitForCacheSync(stopCh, checkAppUpdated))
}

func TestNewHostAppReconciler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	si := mock_statesinformer.NewMockStatesInformer(ctrl)
	si.EXPECT().RegisterCallbacks(statesinformer.RegisterTypeNodeSLOSpec, gomock.Any(), gomock.Any(), gomock.Any())
	ctx := Context{
		StatesInformer:    si,
		Executor:          resourceexecutor.NewResourceUpdateExecutor(),
		ReconcileInterval: time.Second,
	}
	r := NewHostAppReconciler(ctx)
	stop := make(chan struct{}, 1)
	defer close(stop)
	assert.NoError(t, r.Run(stop))
}
