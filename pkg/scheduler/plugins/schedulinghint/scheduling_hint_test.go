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

package schedulinghint

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/scheduler/framework"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext/hinter"
)

func TestPlugin_Name(t *testing.T) {
	p := &Plugin{}
	assert.Equal(t, Name, p.Name())
}

func TestPlugin_PreFilter(t *testing.T) {
	p := &Plugin{}
	state := framework.NewCycleState()
	pod := &corev1.Pod{}
	res, status := p.PreFilter(context.TODO(), state, pod)
	assert.Nil(t, res)
	assert.Nil(t, status)

	hintPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				extension.AnnotationSchedulingHint: "{\"nodeNames\":[\"node-1\",\"node-2\"]}",
			},
		},
	}
	state = framework.NewCycleState()
	_, _, _ = p.BeforePreFilter(context.TODO(), state, hintPod)
	res, status = p.PreFilter(context.TODO(), state, hintPod)
	assert.Nil(t, status)
	assert.Equal(t, &framework.PreFilterResult{NodeNames: sets.New("node-1", "node-2")}, res)
	_ = p.AfterPreFilter(context.TODO(), state, hintPod, res)
}

func TestPlugin_PreFilterExtensions(t *testing.T) {
	p := &Plugin{}
	assert.Nil(t, p.PreFilterExtensions())
}

func TestPlugin_AfterPreFilter(t *testing.T) {
	p := &Plugin{}
	state := framework.NewCycleState()
	pod := &corev1.Pod{}
	status := p.AfterPreFilter(context.TODO(), state, pod, nil)
	assert.Nil(t, status)
}

func TestPlugin_BeforePreFilter(t *testing.T) {
	tests := []struct {
		name           string
		pod            *corev1.Pod
		wantModified   bool
		wantStatusNil  bool
		wantStatusCode framework.Code
		wantHintState  *hinter.SchedulingHintStateData
	}{
		{
			name:           "nil pod",
			pod:            nil,
			wantModified:   false,
			wantStatusNil:  true,
			wantHintState:  nil,
			wantStatusCode: framework.Success,
		},
		{
			name: "no scheduling hint annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"other-annotation": "value",
					},
				},
			},
			wantModified:   false,
			wantStatusNil:  true,
			wantHintState:  nil,
			wantStatusCode: framework.Success,
		},
		{
			name: "valid scheduling hint with node names and extensions",
			pod: func() *corev1.Pod {
				h := &extension.SchedulingHint{
					NodeNames: []string{"node-1", "node-2"},
					Extensions: map[string]interface{}{
						"key": "value",
					},
				}
				b, _ := json.Marshal(h)
				return &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							extension.AnnotationSchedulingHint: string(b),
						},
					},
				}
			}(),
			wantModified:  false,
			wantStatusNil: true,
			wantHintState: &hinter.SchedulingHintStateData{
				PreFilterNodes: []string{"node-1", "node-2"},
				Extensions:     map[string]interface{}{"key": "value"},
			},
			wantStatusCode: framework.Success,
		},
		{
			name: "invalid scheduling hint json",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						extension.AnnotationSchedulingHint: "invalid-json",
					},
				},
			},
			wantModified:   false,
			wantStatusNil:  false,
			wantHintState:  nil,
			wantStatusCode: framework.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			state := framework.NewCycleState()

			retPod, modified, status := p.BeforePreFilter(context.TODO(), state, tt.pod)

			assert.Nil(t, retPod)
			assert.Equal(t, tt.wantModified, modified)

			if tt.wantStatusNil {
				assert.Nil(t, status)
			} else {
				assert.NotNil(t, status)
				assert.Equal(t, tt.wantStatusCode, status.Code())
			}

			hintState := hinter.GetSchedulingHintState(state)
			if tt.wantHintState == nil {
				assert.Nil(t, hintState)
			} else {
				assert.NotNil(t, hintState)
				assert.Equal(t, tt.wantHintState.PreFilterNodes, hintState.PreFilterNodes)
				assert.Equal(t, tt.wantHintState.Extensions, hintState.Extensions)
			}
		})
	}
}

func TestNew_HandleIsNotExtendedHandle(t *testing.T) {
	var handle framework.Handle = nil
	p, err := New(nil, handle)
	assert.Nil(t, p)
	assert.Error(t, err)
}
