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

package framework

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestOption(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		testScheme := runtime.NewScheme()
		var testClient client.Client
		var testRecorder record.EventRecorder
		o := NewOption()
		assert.NotNil(t, o)
		assert.NotPanics(t, func() {
			o1 := o.WithClient(testClient).WithRecorder(testRecorder).WithScheme(testScheme)
			assert.Equal(t, o, o1)
		})
	})
}
