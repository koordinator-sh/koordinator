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

package metrics

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestWebhookDurationCollectors(t *testing.T) {
	t.Run("test not panic", func(t *testing.T) {
		RecordWebhookDurationMilliseconds(MutatingWebhook, Pod, "CREATE", nil, "test-plugin", 0.1)
	})
}

func TestGetErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: StatusAllowed,
		},
		{
			name:     "generic error",
			err:      errors.New("generic error"),
			expected: StatusRejected,
		},
		{
			name:     "context deadline exceeded",
			err:      fmt.Errorf("wrap error: %w", context.DeadlineExceeded),
			expected: StatusTimeout,
		},
		{
			name:     "invalid API error",
			err:      apierrors.NewInvalid(schema.GroupKind{}, "", field.ErrorList{}),
			expected: StatusValidationFailed,
		},
		{
			name:     "timeout API error",
			err:      apierrors.NewTimeoutError("timeout", 0),
			expected: StatusTimeout,
		},
		{
			name:     "forbidden API error",
			err:      apierrors.NewForbidden(schema.GroupResource{}, "", errors.New("forbidden")),
			expected: StatusPolicyDenied,
		},
		{
			name:     "internal error API error",
			err:      apierrors.NewInternalError(errors.New("internal")),
			expected: StatusInternalError,
		},
		{
			name:     "other API error (conflict)",
			err:      apierrors.NewConflict(schema.GroupResource{}, "", errors.New("conflict")),
			expected: "Conflict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getErrorCode(tt.err)
			if got != tt.expected {
				t.Errorf("getErrorCode() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
