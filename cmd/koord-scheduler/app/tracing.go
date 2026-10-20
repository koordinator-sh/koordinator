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
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	"k8s.io/component-base/tracing"
	tracingapi "k8s.io/component-base/tracing/api/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

// tracingServiceName is the OpenTelemetry service.name reported for koord-scheduler spans.
const tracingServiceName = "koord-scheduler"

// setupTracing configures the global OpenTelemetry tracer provider for koord-scheduler
// from the given tracing configuration file. When configFile is empty, tracing stays
// disabled and a no-op shutdown function is returned, so the default behavior is unchanged.
//
// The returned function should be called on shutdown to flush any buffered spans.
func setupTracing(ctx context.Context, configFile string) (func(context.Context) error, error) {
	noopShutdown := func(context.Context) error { return nil }
	if configFile == "" {
		return noopShutdown, nil
	}

	tracingConfig, err := loadTracingConfiguration(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load tracing configuration from %q: %w", configFile, err)
	}
	if errs := tracingapi.ValidateTracingConfiguration(tracingConfig, nil, nil); len(errs) > 0 {
		return nil, fmt.Errorf("invalid tracing configuration: %v", errs.ToAggregate())
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", tracingServiceName),
	}
	// Attach service.instance.id so operators can tell replicas/shards apart in the
	// tracing backend. Best-effort: skip it if the hostname cannot be determined.
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		attrs = append(attrs, attribute.String("service.instance.id", hostname))
	}
	resourceOpts := []resource.Option{
		resource.WithAttributes(attrs...),
	}
	tp, err := tracing.NewProvider(ctx, tracingConfig, nil, resourceOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create tracing provider: %w", err)
	}

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(tracing.Propagators())
	klog.InfoS("Initialized OpenTelemetry tracing for koord-scheduler", "configFile", configFile)
	return tp.Shutdown, nil
}

// loadTracingConfiguration reads and decodes an OpenTelemetry tracing configuration file.
func loadTracingConfiguration(configFile string) (*tracingapi.TracingConfiguration, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	config := &tracingapi.TracingConfiguration{}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to decode tracing configuration: %w", err)
	}
	return config, nil
}
