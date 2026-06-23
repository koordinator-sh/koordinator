package framework

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Scenario is the contract every benchmark workload must satisfy.
// The engine calls: Setup → Pods → (fires burst) → Teardown.
type Scenario interface {
	Name() string

	Setup(
		ctx       context.Context,
		client    kubernetes.Interface,
		dynClient dynamic.Interface,
		cfg       ScenarioConfig,
		runID     string,
	) error

	Pods(cfg ScenarioConfig, runID string) []*corev1.Pod

	Teardown(
		ctx       context.Context,
		client    kubernetes.Interface,
		dynClient dynamic.Interface,
		runID     string,
	) error
}
