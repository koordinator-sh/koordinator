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

package validating

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

type mockManager struct {
	ctrl.Manager
	scheme *runtime.Scheme
}

func (m *mockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func TestProfileValidateBuilder(t *testing.T) {
	scheme := runtime.NewScheme()
	mgr := &mockManager{scheme: scheme}

	builder := &profileValidateBuilder{}
	builder.WithControllerManager(mgr)

	handler := builder.Build()
	assert.NotNil(t, handler)
	assert.IsType(t, &ElasticQuotaProfileValidatingHandler{}, handler)
}
