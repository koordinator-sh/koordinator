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

package transformer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTransformMetadata(t *testing.T) {
	tests := []struct {
		name    string
		pod     *metav1.PartialObjectMetadata
		wantPod *metav1.PartialObjectMetadata
	}{
		{
			name: "normal pod metadata transform",
			pod: &metav1.PartialObjectMetadata{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Pod",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:          "name",
					Namespace:     "ns",
					ManagedFields: []metav1.ManagedFieldsEntry{},
				},
			},

			wantPod: &metav1.PartialObjectMetadata{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Pod",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:          "name",
					Namespace:     "ns",
					ManagedFields: nil,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := TransformMeta(tt.pod)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantPod, obj)
		})
	}
}

func TestTransformMetadataError(t *testing.T) {
	_, err := TransformMeta(&metav1.PartialObjectMetadata{})
	assert.Nil(t, err)
}
