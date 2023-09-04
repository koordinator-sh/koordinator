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

package impl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
)

func TestRegisterCallbacksAndRun(t *testing.T) {
	type args struct {
		objType     statesinformer.RegisterType
		name        string
		description string
	}
	type wants struct {
		callbackRunSucceec bool
	}
	tests := []struct {
		name  string
		args  args
		wants wants
	}{
		{
			name: "register RegisterTypeNodeSLOSpec and run",
			args: args{
				objType:     statesinformer.RegisterTypeNodeSLOSpec,
				name:        "set-bool-var",
				description: "set test bool var as true",
			},
			wants: wants{
				callbackRunSucceec: true,
			},
		},
		{
			name: "register RegisterTypeAllPods and run",
			args: args{
				objType:     statesinformer.RegisterTypeAllPods,
				name:        "set-bool-var",
				description: "set test bool var as true",
			},
			wants: wants{
				callbackRunSucceec: true,
			},
		},
		{
			name: "register RegisterTypeNodeSLOSpec and run",
			args: args{
				objType:     statesinformer.RegisterTypeNodeSLOSpec,
				name:        "set-bool-var",
				description: "set test bool var as true",
			},
			wants: wants{
				callbackRunSucceec: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testVar := pointer.Bool(false)
			callbackFn := func(t statesinformer.RegisterType, obj interface{}, target *statesinformer.CallbackTarget) {
				*testVar = true
			}
			si := &callbackRunner{
				stateUpdateCallbacks: map[statesinformer.RegisterType][]updateCallback{
					statesinformer.RegisterTypeNodeSLOSpec:  {},
					statesinformer.RegisterTypeAllPods:      {},
					statesinformer.RegisterTypeNodeTopology: {},
				},
				statesInformer: &statesInformer{
					states: &PluginState{
						informerPlugins: map[PluginName]informerPlugin{
							nodeSLOInformerName: &nodeSLOInformer{
								nodeSLO: &slov1alpha1.NodeSLO{},
							},
							podsInformerName: &podsInformer{
								podMap: map[string]*statesinformer.PodMeta{},
							},
						},
					},
				},
			}
			si.RegisterCallbacks(tt.args.objType, tt.args.name, tt.args.description, callbackFn)
			si.getObjByType(tt.args.objType, UpdateCbCtx{})
			si.runCallbacks(tt.args.objType, &slov1alpha1.NodeSLO{})
			assert.Equal(t, *testVar, tt.wants.callbackRunSucceec)
		})
	}
}

func Test_statesInformer_startCallbackRunners(t *testing.T) {
	output := make(chan string, 1)
	nodeSLO := &slov1alpha1.NodeSLO{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"test-label-key": "test-label-val1",
			},
		},
	}
	stopCh := make(chan struct{}, 1)
	type args struct {
		objType     statesinformer.RegisterType
		nodeSLO     *slov1alpha1.NodeSLO
		name        string
		description string
		fn          statesinformer.UpdateCbFn
	}
	tests := []struct {
		name       string
		args       args
		wantOutput string
	}{
		{
			name: "callback get nodeslo label",
			args: args{
				objType:     statesinformer.RegisterTypeNodeSLOSpec,
				nodeSLO:     nodeSLO,
				name:        "get value from node slo label",
				description: "get value from node slo label",
				fn: func(t statesinformer.RegisterType, obj interface{}, target *statesinformer.CallbackTarget) {
					output <- nodeSLO.Labels["test-label-key"]
					stopCh <- struct{}{}
				},
			},
			wantOutput: "test-label-val1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := &callbackRunner{
				callbackChans: map[statesinformer.RegisterType]chan UpdateCbCtx{
					tt.args.objType: make(chan UpdateCbCtx, 1),
				},
				stateUpdateCallbacks: map[statesinformer.RegisterType][]updateCallback{
					tt.args.objType: {},
				},
			}
			si := &statesInformer{
				states: &PluginState{
					callbackRunner: cr,
					informerPlugins: map[PluginName]informerPlugin{
						nodeSLOInformerName: &nodeSLOInformer{
							nodeSLO: tt.args.nodeSLO,
						},
						podsInformerName: &podsInformer{
							podMap: map[string]*statesinformer.PodMeta{},
						},
					},
				},
			}
			cr.Setup(si)
			cr.RegisterCallbacks(tt.args.objType, tt.args.name, tt.args.description, tt.args.fn)
			cr.Start(stopCh)
			cr.SendCallback(tt.args.objType)
			gotOutput := <-output
			assert.Equal(t, tt.wantOutput, gotOutput, "send callback for type %v got wrong",
				tt.args.objType.String())
		})
	}
}
