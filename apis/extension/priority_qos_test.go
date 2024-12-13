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

package extension

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestGetPodPriorityQoSClassRaw(t *testing.T) {
	tests := []struct {
		name string
		arg  *corev1.Pod
		want PriorityQoSClass
	}{
		{
			name: "koord priorityQos class not specified",
			arg:  nil,
			want: PriorityQoSClassNone,
		},
		{
			name: "koord priorityQos class not specified",
			arg:  &corev1.Pod{},
			want: PriorityQoSClass(string(PriorityProd) + PriorityQoSSeparator + string(QoSLS)),
		},
		{
			name: "just qos class specified",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSLSE),
					},
				},
			},
			want: PriorityQoSClass(string(PriorityProd) + PriorityQoSSeparator + string(QoSLSE)),
		},
		{
			name: "just priority class specified",
			arg: &corev1.Pod{
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityProdValueMin + PriorityProdValueMax) / 2),
				},
			},
			want: PriorityQoSClass(string(PriorityProd) + PriorityQoSSeparator + string(QoSLS)),
		},
		{
			name: "wrong match: prod + BE",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityProdValueMin + PriorityProdValueMax) / 2),
				},
			},
			want: PriorityQoSClassNone,
		},
		{
			name: "pod qosClass is SYSTEM and has none priorityClass",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSSystem),
					},
				},
			},
			want: PriorityQoSClassSystem,
		},
		{
			name: "pod qosClass is SYSTEM and has prod priorityClass",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSSystem),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityProdValueMin + PriorityProdValueMax) / 2),
				},
			},
			want: PriorityQoSClassSystem,
		},
		{
			name: "wrong match: batch + LSR",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityBatchValueMin + PriorityBatchValueMax) / 2),
				},
			},
			want: PriorityQoSClassNone,
		},
		{
			name: "prod + LSR",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSLSR),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityProdValueMin + PriorityProdValueMax) / 2),
				},
			},
			want: PriorityQoSClass(string(PriorityProd) + PriorityQoSSeparator + string(QoSLSR)),
		},
		{
			name: "prod + LS",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityProdValueMin + PriorityProdValueMax) / 2),
				},
			},
			want: PriorityQoSClass(string(PriorityProd) + PriorityQoSSeparator + string(QoSLS)),
		},
		{
			name: "mid + LS",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSLS),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityMidValueMin + PriorityMidValueMax) / 2),
				},
			},
			want: PriorityQoSClass(string(PriorityMid) + PriorityQoSSeparator + string(QoSLS)),
		},
		{
			name: "mid + BE",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityMidValueMin + PriorityMidValueMax) / 2),
				},
			},
			want: PriorityQoSClass(string(PriorityMid) + PriorityQoSSeparator + string(QoSBE)),
		},
		{
			name: "batch + BE",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityBatchValueMin + PriorityBatchValueMax) / 2),
				},
			},
			want: PriorityQoSClass(string(PriorityBatch) + PriorityQoSSeparator + string(QoSBE)),
		},
		{
			name: "free + BE",
			arg: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						LabelPodQoS: string(QoSBE),
					},
				},
				Spec: corev1.PodSpec{
					Priority: pointer.Int32((PriorityFreeValueMin + PriorityFreeValueMax) / 2),
				},
			},
			want: PriorityQoSClass(string(PriorityFree) + PriorityQoSSeparator + string(QoSBE)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPodPriorityQoSClassRaw(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}
