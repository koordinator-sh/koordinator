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

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/fields"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFieldIndexName(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  string
	}{
		{
			name:  "plain field",
			field: "metadata.namespace",
			want:  "field:metadata.namespace",
		},
		{
			name:  "nested field",
			field: "spec.nodeName",
			want:  "field:spec.nodeName",
		},
		{
			name:  "empty field",
			field: "",
			want:  "field:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FieldIndexName(tt.field))
		})
	}
}

func TestKeyToNamespacedKey(t *testing.T) {
	tests := []struct {
		name    string
		ns      string
		baseKey string
		want    string
	}{
		{
			name:    "non-empty namespace",
			ns:      "default",
			baseKey: "myvalue",
			want:    "default/myvalue",
		},
		{
			name:    "empty namespace uses all-namespaces sentinel",
			ns:      "",
			baseKey: "myvalue",
			want:    "__all_namespaces/myvalue",
		},
		{
			name:    "non-empty namespace with compound baseKey",
			ns:      "kube-system",
			baseKey: "node-1",
			want:    "kube-system/node-1",
		},
		{
			name:    "empty namespace with empty baseKey",
			ns:      "",
			baseKey: "",
			want:    "__all_namespaces/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, KeyToNamespacedKey(tt.ns, tt.baseKey))
		})
	}
}

func TestRequiresExactMatch(t *testing.T) {
	tests := []struct {
		name         string
		selector     fields.Selector
		wantField    string
		wantVal      string
		wantRequired bool
	}{
		{
			name:         "single equals requirement",
			selector:     fields.OneTermEqualSelector("spec.nodeName", "node-1"),
			wantField:    "spec.nodeName",
			wantVal:      "node-1",
			wantRequired: true,
		},
		{
			name:         "no requirements returns false",
			selector:     fields.Everything(),
			wantField:    "",
			wantVal:      "",
			wantRequired: false,
		},
		{
			name:         "multiple requirements returns false",
			selector:     fields.SelectorFromSet(fields.Set{"key1": "val1", "key2": "val2"}),
			wantField:    "",
			wantVal:      "",
			wantRequired: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotField, gotVal, gotRequired := requiresExactMatch(tt.selector)
			assert.Equal(t, tt.wantField, gotField)
			assert.Equal(t, tt.wantVal, gotVal)
			assert.Equal(t, tt.wantRequired, gotRequired)
		})
	}
}

func TestIsDisableDeepCopy(t *testing.T) {
	tests := []struct {
		name string
		opts []runtimeclient.ListOption
		want bool
	}{
		{
			name: "empty options",
			opts: []runtimeclient.ListOption{},
			want: false,
		},
		{
			name: "DisableDeepCopy sentinel only",
			opts: []runtimeclient.ListOption{DisableDeepCopy},
			want: true,
		},
		{
			name: "DisableDeepCopy among other options",
			opts: []runtimeclient.ListOption{&runtimeclient.ListOptions{}, DisableDeepCopy},
			want: true,
		},
		{
			name: "other options only",
			opts: []runtimeclient.ListOption{&runtimeclient.ListOptions{}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isDisableDeepCopy(tt.opts))
		})
	}
}
