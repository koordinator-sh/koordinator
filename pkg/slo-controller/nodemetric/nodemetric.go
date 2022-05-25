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

package nodemetric

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

func (r *NodeMetricReconciler) initNodeMetric(node *corev1.Node, nodeMetric *slov1alpha1.NodeMetric) error {
	if node == nil || nodeMetric == nil {
		return fmt.Errorf("both Node and NodeMetric should not be empty")
	}

	nodeMetricSpec, err := r.getNodeMetricSpec(node, nil)
	if err != nil {
		return err
	}

	nodeMetricStatus := &slov1alpha1.NodeMetricStatus{
		UpdateTime: &metav1.Time{Time: time.Now()},
		NodeMetric: &slov1alpha1.NodeMetricInfo{},
		PodsMetric: make([]*slov1alpha1.PodMetricInfo, 0),
	}

	nodeMetric.Spec = *nodeMetricSpec
	nodeMetric.Status = *nodeMetricStatus
	nodeMetric.SetName(node.GetName())
	nodeMetric.SetNamespace(node.GetNamespace())

	return nil
}
