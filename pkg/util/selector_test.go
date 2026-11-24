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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func TestGetFastLabelSelector(t *testing.T) {
	tests := []struct {
		name          string
		labelSelector *metav1.LabelSelector
		wantError     bool
		testLabels    labels.Set
		wantMatch     bool
	}{
		{
			name: "Match-label-only selector",
			labelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"foo": "bar",
				},
			},
			wantError: false,
			testLabels: labels.Set{
				"foo": "bar",
				"baz": "qux",
			},
			wantMatch: true,
		},
		{
			name: "Match-label-only selector not matching",
			labelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"foo": "bar",
				},
			},
			wantError: false,
			testLabels: labels.Set{
				"foo": "baz",
			},
			wantMatch: false,
		},
		{
			name: "Selector with MatchExpressions",
			labelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "env",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"prod", "staging"},
					},
				},
			},
			wantError: false,
			testLabels: labels.Set{
				"env": "prod",
			},
			wantMatch: true,
		},
		{
			name:          "Empty selector",
			labelSelector: &metav1.LabelSelector{},
			wantError:     false,
			testLabels: labels.Set{
				"anything": "works",
			},
			wantMatch: true,
		},
		{
			name: "Invalid match expression",
			labelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "invalid-key",
						Operator: "InvalidOp",
						Values:   []string{"val"},
					},
				},
			},
			wantError:  true,
			testLabels: nil,
			wantMatch:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector, err := GetFastLabelSelector(tt.labelSelector)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, selector)
				assert.Equal(t, tt.wantMatch, selector.Matches(tt.testLabels))
			}
		})
	}
}
