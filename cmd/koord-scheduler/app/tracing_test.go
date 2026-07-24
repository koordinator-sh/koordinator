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

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
)

func TestSetupTracing(t *testing.T) {
	t.Run("empty config file keeps tracing disabled", func(t *testing.T) {
		previous := otel.GetTracerProvider()
		defer otel.SetTracerProvider(previous)

		shutdown, err := setupTracing(context.Background(), "")
		assert.NoError(t, err)
		assert.NotNil(t, shutdown)
		// The global provider must be left untouched when tracing is disabled.
		assert.Same(t, previous, otel.GetTracerProvider())
		assert.NoError(t, shutdown(context.Background()))
	})

	t.Run("valid config file installs a tracer provider", func(t *testing.T) {
		previous := otel.GetTracerProvider()
		defer otel.SetTracerProvider(previous)

		configFile := filepath.Join(t.TempDir(), "tracing.yaml")
		content := "endpoint: localhost:4317\nsamplingRatePerMillion: 1000\n"
		assert.NoError(t, os.WriteFile(configFile, []byte(content), 0644))

		shutdown, err := setupTracing(context.Background(), configFile)
		assert.NoError(t, err)
		assert.NotNil(t, shutdown)
		assert.NotSame(t, previous, otel.GetTracerProvider())
		assert.NoError(t, shutdown(context.Background()))
	})

	t.Run("missing config file returns an error", func(t *testing.T) {
		shutdown, err := setupTracing(context.Background(), filepath.Join(t.TempDir(), "does-not-exist.yaml"))
		assert.Error(t, err)
		assert.Nil(t, shutdown)
	})

	t.Run("invalid sampling rate returns an error", func(t *testing.T) {
		configFile := filepath.Join(t.TempDir(), "tracing.yaml")
		// samplingRatePerMillion above 1000000 is rejected by validation.
		content := "samplingRatePerMillion: 2000000\n"
		assert.NoError(t, os.WriteFile(configFile, []byte(content), 0644))

		shutdown, err := setupTracing(context.Background(), configFile)
		assert.Error(t, err)
		assert.Nil(t, shutdown)
	})
}
