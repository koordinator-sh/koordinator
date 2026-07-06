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

package kwok

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"golang.org/x/sync/errgroup"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// defaultNodeCreationWorkers is used when types.NodeSpec.NodeCreationWorkers is unset.
const defaultNodeCreationWorkers = 20

// Provider implements nodeprovider.NodeProvider using kwok simulated nodes.
type Provider struct {
	client kubernetes.Interface
}

// New creates a new kwok Provider. Pass nil for Week 1 stub mode.
func New(client kubernetes.Interface) *Provider {
	return &Provider{client: client}
}

// CreateNodes provisions count kwok nodes labelled with runID.
func (p *Provider) CreateNodes(ctx context.Context, runID string, spec types.NodeSpec, count int) error {
	if p.client == nil {
		fmt.Printf("[kwok stub] Would create %d nodes for runID=%s\n", count, runID)
		return nil
	}
	runIDPrefix := runID
	if len(runIDPrefix) > 8 {
		runIDPrefix = runIDPrefix[:8]
	}

	workers := spec.NodeCreationWorkers
	if workers <= 0 {
		workers = defaultNodeCreationWorkers
	}

	g, gctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, workers)

	for i := 0; i < count; i++ {
		name := fmt.Sprintf("kwok-bench-node-%s-%04d", runIDPrefix, i)
		g.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()
			node, err := buildKwokNode(name, runID, spec)
			if err != nil {
				return err
			}
			if _, err := p.client.CoreV1().Nodes().Create(gctx, node, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("failed to create node %q: %w", name, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// DeleteNodes removes all nodes labelled with runID.
func (p *Provider) DeleteNodes(ctx context.Context, runID string) error {
	if p.client == nil {
		fmt.Printf("[kwok stub] Would delete nodes for runID=%s\n", runID)
		return nil
	}
	policy := metav1.DeletePropagationBackground
	return p.client.CoreV1().Nodes().DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &policy},
		metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", RunIDLabel, runID)},
	)
}

// WaitReady blocks until all nodes labelled with runID are Ready, or until timeout fires.
func (p *Provider) WaitReady(ctx context.Context, runID string, timeout time.Duration) error {
	if p.client == nil {
		fmt.Printf("[kwok stub] Would wait for nodes runID=%s\n", runID)
		return nil
	}
	deadline := time.Now().Add(timeout)
	labelSel := fmt.Sprintf("%s=%s", RunIDLabel, runID)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		nodes, err := p.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: labelSel})
		if err != nil {
			return fmt.Errorf("failed to list nodes: %w", err)
		}

		allReady := len(nodes.Items) > 0
		for _, node := range nodes.Items {
			ready := false
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					ready = true
					break
				}
			}
			if !ready {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for nodes to be ready (runID=%s)", runID)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
