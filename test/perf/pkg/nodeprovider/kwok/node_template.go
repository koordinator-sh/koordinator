package kwok

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/framework"
)

const RunIDLabel = "benchmark.koordinator.sh/run-id"

// buildKwokNode constructs a fake Node object that the kwok controller will simulate as Ready.
func buildKwokNode(name, runID string, spec framework.NodeSpec) *corev1.Node {
	cpu := spec.CPU
	if cpu == "" {
		cpu = "32"
	}
	memory := spec.Memory
	if memory == "" {
		memory = "256Gi"
	}
	maxPods := spec.MaxPods
	if maxPods == 0 {
		maxPods = 110
	}

	labels := map[string]string{
		"type":                    "kwok",
		"kubernetes.io/hostname":  name,
		"beta.kubernetes.io/os":   "linux",
		"beta.kubernetes.io/arch": "amd64",
		RunIDLabel:                runID,
	}
	for k, v := range spec.Labels {
		labels[k] = v
	}

	resourceList := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
		corev1.ResourcePods:   *resource.NewQuantity(int64(maxPods), resource.DecimalSI),
	}

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				"node.alpha.kubernetes.io/ttl": "0",
				"kwok.x-k8s.io/node":          "fake",
			},
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			// kwok requires this taint; pods must carry the matching toleration.
			Taints: []corev1.Taint{
				{
					Key:    "kwok.x-k8s.io/node",
					Value:  "fake",
					Effect: corev1.TaintEffectNoSchedule,
				},
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: resourceList,
			Capacity:    resourceList,
			Phase:       corev1.NodeRunning,
			Conditions: []corev1.NodeCondition{
				{
					Type:               corev1.NodeReady,
					Status:             corev1.ConditionTrue,
					LastHeartbeatTime:  metav1.Now(),
					LastTransitionTime: metav1.Now(),
					Reason:             "KwokReady",
					Message:            "kwok node is ready",
				},
			},
		},
	}
}
