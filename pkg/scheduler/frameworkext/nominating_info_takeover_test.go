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

package frameworkext

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func TestTakeoverNominatingInfo(t *testing.T) {
	type args struct {
		podInfo        *framework.QueuedPodInfo
		status         *framework.Status
		nominatingInfo *framework.NominatingInfo
	}
	tests := []struct {
		name               string
		args               args
		wantNominatingInfo *framework.NominatingInfo
		want               bool
	}{
		{
			name: "job preemption reject waitingPod",
			args: args{
				nominatingInfo: &framework.NominatingInfo{NominatingMode: framework.ModeOverride, NominatedNodeName: ""},
				podInfo: &framework.QueuedPodInfo{
					PodInfo: &framework.PodInfo{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
							},
						},
					},
				},
				status: (framework.NewStatus(framework.Unschedulable)).WithFailedPlugin(JobPreemptionRejectPlugin),
			},
			wantNominatingInfo: &framework.NominatingInfo{NominatingMode: framework.ModeOverride, NominatedNodeName: "test-node"},
			want:               false,
		},
		{
			name: "job reject waitingPod",
			args: args{
				nominatingInfo: &framework.NominatingInfo{NominatingMode: framework.ModeOverride, NominatedNodeName: ""},
				podInfo: &framework.QueuedPodInfo{
					PodInfo: &framework.PodInfo{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
							},
						},
					},
				},
				status: (framework.NewStatus(framework.Unschedulable)).WithFailedPlugin(JobRejectPlugin),
			},
			wantNominatingInfo: &framework.NominatingInfo{NominatingMode: framework.ModeNoop},
			want:               false,
		},
		{
			name: "other plugin",
			args: args{
				nominatingInfo: &framework.NominatingInfo{NominatingMode: framework.ModeOverride, NominatedNodeName: ""},
				podInfo: &framework.QueuedPodInfo{
					PodInfo: &framework.PodInfo{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
							},
						},
					},
				},
				status: (framework.NewStatus(framework.Unschedulable)).WithFailedPlugin("other-plugin"),
			},
			wantNominatingInfo: &framework.NominatingInfo{NominatingMode: framework.ModeOverride, NominatedNodeName: ""},
			want:               false,
		},
		{
			name: "other plugin",
			args: args{
				nominatingInfo: &framework.NominatingInfo{NominatingMode: framework.ModeOverride, NominatedNodeName: ""},
				podInfo: &framework.QueuedPodInfo{
					PodInfo: &framework.PodInfo{
						Pod: &corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-pod",
							},
							Spec: corev1.PodSpec{
								NodeName: "test-node",
							},
						},
					},
				},
				status: &framework.Status{},
			},
			wantNominatingInfo: &framework.NominatingInfo{NominatingMode: framework.ModeOverride, NominatedNodeName: ""},
			want:               false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TakeoverNominatingInfo(context.TODO(), nil, tt.args.podInfo, tt.args.status, tt.args.nominatingInfo, time.Now()))
			assert.Equal(t, tt.wantNominatingInfo, tt.args.nominatingInfo)
		})
	}
}

func Test_getRejecterPlugin(t *testing.T) {
	type args struct {
		status *framework.Status
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "success",
			args: args{
				status: &framework.Status{},
			},
			want: "",
		},
		{
			name: "1.28 reject",
			args: args{
				status: (framework.NewStatus(framework.Unschedulable)).WithFailedPlugin("plugin"),
			},
			want: "plugin",
		},
		{
			name: "1.33 reject",
			args: args{
				status: (framework.NewStatus(framework.Unschedulable)).WithError(&framework.FitError{
					Diagnosis: framework.Diagnosis{
						UnschedulablePlugins: sets.New[string]("plugin"),
					},
				}),
			},
			want: "plugin",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, getRejecterPlugin(tt.args.status), "getRejecterPlugin(%v)", tt.args.status)
		})
	}
}
