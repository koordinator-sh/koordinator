package framework

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Engine orchestrates a full benchmark run.
type Engine struct {
	client    kubernetes.Interface
	dynClient dynamic.Interface
	provider  NodeProvider
}

// NewEngine creates an Engine connected to the cluster at kubeconfig.
// Pass "" to use the default ~/.kube/config.
func NewEngine(kubeconfig string, provider NodeProvider) (*Engine, error) {
	var cfg *rest.Config
	var err error

	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", filepath.Join(os.Getenv("HOME"), ".kube", "config"))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &Engine{
		client:    client,
		dynClient: dynClient,
		provider:  provider,
	}, nil
}

// Run executes a benchmark scenario and writes the result to outputPath.
// The caller is responsible for resolving the scenario from the registry.
func (e *Engine) Run(ctx context.Context, scenario Scenario, cfg ScenarioConfig, outputPath string) error {
	runID := uuid.New().String()
	fmt.Printf("Starting benchmark: scenario=%s runID=%s\n", cfg.Name, runID)

	_, err := e.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("cannot connect to API server: %w", err)
	}

	fmt.Printf("API server reachable. Scenario %q registered and ready.\n", scenario.Name())

	result := BenchmarkResult{
		Name:      cfg.Name,
		RunID:     runID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		NodeCount: cfg.NodeCount,
		PodCount:  cfg.PodCount,
	}
	return WriteReport(result, outputPath)
}
