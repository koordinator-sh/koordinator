package basic

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/framework"
	"github.com/koordinator-sh/koordinator/test/perf/pkg/scenarios"
)

// RunIDLabel is the pod/node label key used to scope resources to a single benchmark run.
const RunIDLabel = "benchmark.koordinator.sh/run-id"

// BasicScenario is a plain pod burst with no Koordinator-specific plugin setup.
// Use this as the baseline every other scenario is compared against.
type BasicScenario struct{}

func init() {
	scenarios.Register(&BasicScenario{})
}

func (s *BasicScenario) Name() string {
	return "basic"
}

// Setup ensures the benchmark namespace exists. No other prerequisites are needed.
func (s *BasicScenario) Setup(
	ctx       context.Context,
	client    kubernetes.Interface,
	dynClient dynamic.Interface,
	cfg       framework.ScenarioConfig,
	runID     string,
) error {
	ns := cfg.Namespace
	if ns == "" {
		ns = "benchmark"
	}
	_, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		_, createErr := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		}, metav1.CreateOptions{})
		if createErr != nil {
			return fmt.Errorf("failed to create namespace %q: %w", ns, createErr)
		}
	}
	return nil
}

// Pods returns cfg.PodCount pod specs targeting kwok-simulated nodes.
func (s *BasicScenario) Pods(cfg framework.ScenarioConfig, runID string) []*corev1.Pod {
	ns := cfg.Namespace
	if ns == "" {
		ns = "benchmark"
	}
	schedulerName := cfg.SchedulerName
	if schedulerName == "" {
		schedulerName = "koord-scheduler"
	}

	resources := corev1.ResourceRequirements{}
	if len(cfg.ResourceRequests) > 0 {
		requests := corev1.ResourceList{}
		for k, v := range cfg.ResourceRequests {
			requests[corev1.ResourceName(k)] = resource.MustParse(v)
		}
		resources.Requests = requests
		resources.Limits = requests
	}

	podLabels := map[string]string{
		RunIDLabel: runID,
		"app":      "kwok-bench",
	}
	for k, v := range cfg.Labels {
		podLabels[k] = v
	}
	if cfg.QoSClass != "" {
		podLabels["koordinator.sh/qosClass"] = cfg.QoSClass
	}

	pods := make([]*corev1.Pod, 0, cfg.PodCount)
	for i := 0; i < cfg.PodCount; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        fmt.Sprintf("bench-pod-%s-%d", runID[:8], i),
				Namespace:   ns,
				Labels:      podLabels,
				Annotations: cfg.Annotations,
			},
			Spec: corev1.PodSpec{
				SchedulerName: schedulerName,
				Containers: []corev1.Container{
					{
						Name:      "pause",
						Image:     "registry.k8s.io/pause:3.9",
						Resources: resources,
					},
				},
				// NodeSelector and toleration restrict pods to kwok-simulated nodes.
				NodeSelector: map[string]string{
					"type": "kwok",
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "kwok.x-k8s.io/node",
						Operator: corev1.TolerationOpExists,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
			},
		}
		pods = append(pods, pod)
	}
	return pods
}

// Teardown deletes all pods created during this run.
func (s *BasicScenario) Teardown(
	ctx       context.Context,
	client    kubernetes.Interface,
	dynClient dynamic.Interface,
	runID     string,
) error {
	ns := "benchmark"
	labelSel := fmt.Sprintf("%s=%s", RunIDLabel, runID)

	deletePolicy := metav1.DeletePropagationBackground
	err := client.CoreV1().Pods(ns).DeleteCollection(
		ctx,
		metav1.DeleteOptions{PropagationPolicy: &deletePolicy},
		metav1.ListOptions{LabelSelector: labelSel},
	)
	if err != nil {
		return fmt.Errorf("teardown: failed to delete pods: %w", err)
	}
	return nil
}
