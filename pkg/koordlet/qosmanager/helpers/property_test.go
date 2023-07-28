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

package helpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/testutil"
)

func Test_getPodResourceQoSByQoSClass(t *testing.T) {
	type args struct {
		pod      *corev1.Pod
		strategy *slov1alpha1.ResourceQOSStrategy
	}
	tests := []struct {
		name string
		args args
		want *slov1alpha1.ResourceQOS
	}{
		{
			name: "return nil",
			args: args{},
			want: nil,
		},
		{
			name: "get qos=LS config",
			args: args{
				pod:      testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSLS).Pod,
				strategy: testutil.DefaultQOSStrategy(),
			},
			want: testutil.DefaultQOSStrategy().LSClass,
		},
		{
			name: "get qos=None kubeQoS=Burstable config",
			args: args{
				pod:      testutil.MockTestPodWithQOS(corev1.PodQOSBurstable, apiext.QoSNone).Pod,
				strategy: testutil.DefaultQOSStrategy(),
			},
			want: testutil.DefaultQOSStrategy().LSClass,
		},
		{
			name: "get qos=None kubeQoS=Besteffort config",
			args: args{
				pod:      testutil.MockTestPodWithQOS(corev1.PodQOSBestEffort, apiext.QoSNone).Pod,
				strategy: testutil.DefaultQOSStrategy(),
			},
			want: testutil.DefaultQOSStrategy().BEClass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPodResourceQoSByQoSClass(tt.args.pod, tt.args.strategy)
			assert.Equal(t, tt.want, got)
		})
	}
}
