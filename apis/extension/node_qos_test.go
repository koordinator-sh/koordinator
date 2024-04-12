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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetNodeTotalBandwidth(t *testing.T) {
	tests := []struct {
		name    string
		node    *corev1.Node
		want    *resource.Quantity
		wantErr bool
	}{
		{
			name: "normal",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeBandwidth: "10M",
					},
				},
			},
			want:    resource.NewScaledQuantity(10, 6),
			wantErr: false,
		},
		{
			name: "node has empty annotation",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name: "node has wrong-formatted annotation value",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationNodeBandwidth: "wrong-format",
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := GetNodeTotalBandwidth(tt.node.Annotations)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, t, got)
				assert.Equal(t, tt.want.Value(), got.Value())
			}
			assert.Equal(t, tt.wantErr, gotErr != nil)
		})
	}
}
