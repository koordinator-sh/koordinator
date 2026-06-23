package kwok

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/framework"
)

// Provider implements framework.NodeProvider using kwok simulated nodes.
type Provider struct {
	client kubernetes.Interface
}

// New creates a new kwok Provider.
func New(client kubernetes.Interface) *Provider {
	return &Provider{client: client}
}

// CreateNodes provisions count kwok nodes labelled with runID.
func (p *Provider) CreateNodes(
	ctx   context.Context,
	runID string,
	spec  framework.NodeSpec,
	count int,
) error {
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("kwok-bench-node-%s-%d", runID[:8], i)
		node := buildKwokNode(name, runID, spec)
		_, err := p.client.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create node %q: %w", name, err)
		}
	}
	return nil
}

// DeleteNodes removes all nodes labelled with runID.
func (p *Provider) DeleteNodes(ctx context.Context, runID string) error {
	labelSel := fmt.Sprintf("%s=%s", RunIDLabel, runID)
	deletePolicy := metav1.DeletePropagationBackground
	return p.client.CoreV1().Nodes().DeleteCollection(
		ctx,
		metav1.DeleteOptions{PropagationPolicy: &deletePolicy},
		metav1.ListOptions{LabelSelector: labelSel},
	)
}

// WaitReady blocks until all nodes labelled with runID are Ready, or until timeout fires.
func (p *Provider) WaitReady(ctx context.Context, runID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	labelSel := fmt.Sprintf("%s=%s", RunIDLabel, runID)

	for time.Now().Before(deadline) {
		nodes, err := p.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{
			LabelSelector: labelSel,
		})
		if err != nil {
			return fmt.Errorf("failed to list nodes: %w", err)
		}

		allReady := true
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

		if allReady && len(nodes.Items) > 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for nodes to become ready (runID=%s)", runID)
}
