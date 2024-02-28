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

package nodenumaresource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	schedulingconfig "github.com/koordinator-sh/koordinator/pkg/scheduler/apis/config"
)

func Test_getCPUBindPolicy(t *testing.T) {
	tests := []struct {
		name            string
		kubeletPolicy   *extension.KubeletCPUManagerPolicy
		nodePolicy      extension.NodeCPUBindPolicy
		requiredPolicy  schedulingconfig.CPUBindPolicy
		preferredPolicy schedulingconfig.CPUBindPolicy
		wantPolicy      schedulingconfig.CPUBindPolicy
		wantRequired    bool
		wantError       bool
	}{
		{
			name: "kubelet enables FullPCPUsOnly",
			kubeletPolicy: &extension.KubeletCPUManagerPolicy{
				Policy: extension.KubeletCPUManagerPolicyStatic,
				Options: map[string]string{
					extension.KubeletCPUManagerPolicyFullPCPUsOnlyOption: "true",
				},
			},
			nodePolicy:      "",
			requiredPolicy:  "",
			preferredPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			wantPolicy:      schedulingconfig.CPUBindPolicyFullPCPUs,
			wantRequired:    true,
			wantError:       false,
		},
		{
			name:            "node enables FullPCPUsOnly",
			nodePolicy:      extension.NodeCPUBindPolicyFullPCPUsOnly,
			requiredPolicy:  "",
			preferredPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			wantPolicy:      schedulingconfig.CPUBindPolicyFullPCPUs,
			wantRequired:    true,
			wantError:       false,
		},
		{
			name:           "pod enables required FullPCPUsOnly",
			requiredPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			wantPolicy:     schedulingconfig.CPUBindPolicyFullPCPUs,
			wantRequired:   true,
			wantError:      false,
		},
		{
			name:            "pod enables preferred FullPCPUsOnly",
			preferredPolicy: schedulingconfig.CPUBindPolicyFullPCPUs,
			wantPolicy:      schedulingconfig.CPUBindPolicyFullPCPUs,
			wantRequired:    false,
			wantError:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topologyOpts := &TopologyOptions{
				Policy: tt.kubeletPolicy,
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			}
			if tt.nodePolicy != "" {
				node.Labels[extension.LabelNodeCPUBindPolicy] = string(tt.nodePolicy)
			}
			policy, required, err := getCPUBindPolicy(topologyOpts, node, tt.requiredPolicy, tt.preferredPolicy)
			assert.Equal(t, tt.wantPolicy, policy)
			assert.Equal(t, tt.wantRequired, required)
			if tt.wantError != (err != nil) {
				t.Errorf("wantErr=%v, but got err=%v", tt.wantError, err)
			}
		})
	}
}
