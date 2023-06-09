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

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

func TestRequestedHostPorts(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want framework.HostPortInfo
	}{
		{
			name: "no ports",
			pod:  &corev1.Pod{},
			want: nil,
		},
		{
			name: "container request ports",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									HostPort: 8888,
									Protocol: corev1.ProtocolTCP,
								},
								{
									HostPort: 6666,
									Protocol: corev1.ProtocolUDP,
								},
							},
						},
					},
				},
			},
			want: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
					*framework.NewProtocolPort(string(corev1.ProtocolUDP), 6666): struct{}{},
				},
			},
		},
		{
			name: "container and init container request ports",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									HostPort: 8888,
									Protocol: corev1.ProtocolTCP,
								},
								{
									HostPort: 6666,
									Protocol: corev1.ProtocolUDP,
								},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									HostPort: 7777,
									Protocol: corev1.ProtocolTCP,
								},
							},
						},
					},
				},
			},
			want: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 7777): struct{}{},
					*framework.NewProtocolPort(string(corev1.ProtocolUDP), 6666): struct{}{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RequestedHostPorts(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResetHostPorts(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		ports   framework.HostPortInfo
		wantPod *corev1.Pod
	}{
		{
			name:    "no ports",
			pod:     &corev1.Pod{},
			ports:   nil,
			wantPod: &corev1.Pod{},
		},
		{
			name: "clean ports",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									HostPort: 8888,
									Protocol: corev1.ProtocolTCP,
								},
								{
									HostPort: 6666,
									Protocol: corev1.ProtocolUDP,
								},
							},
						},
					},
				},
			},
			ports: nil,
			wantPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{},
					},
				},
			},
		},
		{
			name: "reserve some ports",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									HostPort: 8888,
									Protocol: corev1.ProtocolTCP,
								},
								{
									HostPort: 6666,
									Protocol: corev1.ProtocolUDP,
								},
							},
						},
					},
				},
			},
			ports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			wantPod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									HostIP:   framework.DefaultBindAllHostIP,
									HostPort: 8888,
									Protocol: corev1.ProtocolTCP,
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetHostPorts(tt.pod, tt.ports)
			assert.Equal(t, tt.wantPod, tt.pod)
		})
	}
}

func TestCloneHostPorts(t *testing.T) {
	tests := []struct {
		name  string
		ports framework.HostPortInfo
		want  framework.HostPortInfo
	}{
		{
			name:  "no ports",
			ports: framework.HostPortInfo{},
			want:  nil,
		},
		{
			name: "clone ports",
			ports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			want: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CloneHostPorts(tt.ports)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAppendHostPorts(t *testing.T) {
	tests := []struct {
		name   string
		ports  framework.HostPortInfo
		nports framework.HostPortInfo
		want   framework.HostPortInfo
	}{
		{
			name:   "no ports to append",
			ports:  nil,
			nports: nil,
			want:   nil,
		},
		{
			name:  "append ports",
			ports: nil,
			nports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			want: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
		},
		{
			name: "append existing ports",
			ports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			nports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			want: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
		},
		{
			name: "append non-existing ports",
			ports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 6666): struct{}{},
				},
			},
			nports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			want: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 6666): struct{}{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppendHostPorts(tt.ports, tt.nports)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRemoveHostPorts(t *testing.T) {
	tests := []struct {
		name    string
		ports   framework.HostPortInfo
		removed framework.HostPortInfo
		want    framework.HostPortInfo
	}{
		{
			name: "no ports to removed",
			ports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			removed: nil,
			want: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
		},
		{
			name: "remove ports",
			ports: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			removed: framework.HostPortInfo{
				framework.DefaultBindAllHostIP: {
					*framework.NewProtocolPort(string(corev1.ProtocolTCP), 8888): struct{}{},
				},
			},
			want: framework.HostPortInfo{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RemoveHostPorts(tt.ports, tt.removed)
			assert.Equal(t, tt.want, tt.ports)
		})
	}
}
