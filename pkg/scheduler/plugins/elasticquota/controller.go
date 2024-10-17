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

package elasticquota

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"github.com/koordinator-sh/koordinator/apis/thirdparty/scheduler-plugins/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
	koordutil "github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	ControllerName = "ElasticQuotaController"
)

// Controller is a controller that update elastic quota crd
type Controller struct {
	plugin *Plugin
}

func NewElasticQuotaController(plugin *Plugin) *Controller {
	ctrl := &Controller{
		plugin: plugin,
	}
	return ctrl
}

func (ctrl *Controller) Name() string {
	return ControllerName
}

func (ctrl *Controller) Start() {
	go wait.Until(ctrl.syncElasticQuotaStatusWorker, 1*time.Second, context.TODO().Done())
	go wait.Until(ctrl.syncElasticQuotaStatusMetricsWorker, 10*time.Second, context.TODO().Done())
}

func (ctrl *Controller) syncElasticQuotaStatusWorker() {
	elasticQuotas, err := ctrl.plugin.quotaLister.List(labels.Everything())
	if err != nil {
		klog.V(3).ErrorS(err, "Unable to list elastic quota in syncElasticQuotaStatusWorker")
		return
	}
	for _, eq := range elasticQuotas {
		ctrl.syncElasticQuotaStatus(eq)
	}
	return
}

func (ctrl *Controller) syncElasticQuotaStatus(eq *v1alpha1.ElasticQuota) {
	summary, _ := ctrl.plugin.GetQuotaSummary(eq.Name, false)
	if summary == nil {
		klog.Warningf("failed get quota summary for elasticQuota %v", eq.Name)
		return
	}

	newEQ, err := updateElasticQuotaStatusIfChanged(eq, summary, klog.V(5).Enabled())
	if err != nil {
		klog.ErrorS(err, "failed to updateElasticQuotaStatusIfChanged", "elasticQuota", eq.Name)
		return
	}
	if newEQ == nil {
		if klog.V(5).Enabled() {
			klog.InfoS("Skip updating elasticQuota because there are no changes", "elasticQuota", eq.Name)
		}
		return
	}

	if klog.V(5).Enabled() {
		klog.InfoS("Try updating elasticQuota since it has changed", "elasticQuota", eq.Name)
	}

	patch, err := util.CreateMergePatch(eq, newEQ)
	if err != nil {
		klog.ErrorS(err, "Failed to create mergePatch", "elasticQuota", eq.Name)
		return
	}

	start := time.Now()
	defer func() {
		UpdateElasticQuotaStatusLatency.Observe(time.Since(start).Seconds())
	}()

	err = koordutil.RetryOnConflictOrTooManyRequests(func() error {
		_, patchErr := ctrl.plugin.client.SchedulingV1alpha1().ElasticQuotas(eq.Namespace).
			Patch(context.TODO(), eq.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		return patchErr
	})
	if err != nil {
		klog.ErrorS(err, "Failed to patch elasticQuota", "elasticQuota", eq.Name)
	} else {
		if klog.V(5).Enabled() {
			klog.InfoS("Successfully patch elasticQuota", "elasticQuota", eq.Name)
		}
	}
}

var resourceDecorators []func(quota *v1alpha1.ElasticQuota, resource v1.ResourceList)

func decorateResource(quota *v1alpha1.ElasticQuota, resource v1.ResourceList) {
	for _, fn := range resourceDecorators {
		fn(quota, resource)
	}
}

type traceChange struct {
	key      string
	original v1.ResourceList
	current  v1.ResourceList
}

func updateElasticQuotaStatusIfChanged(eq *v1alpha1.ElasticQuota, summary *core.QuotaInfoSummary, logChanges bool) (*v1alpha1.ElasticQuota, error) {
	m := map[string]v1.ResourceList{
		extension.AnnotationRuntime:               summary.Runtime,
		extension.AnnotationRequest:               summary.Request,
		extension.AnnotationChildRequest:          summary.ChildRequest,
		extension.AnnotationGuaranteed:            summary.Guaranteed,
		extension.AnnotationAllocated:             summary.Allocated,
		extension.AnnotationNonPreemptibleRequest: summary.NonPreemptibleRequest,
		extension.AnnotationNonPreemptibleUsed:    summary.NonPreemptibleUsed,
	}
	var newElasticQuota *v1alpha1.ElasticQuota
	var changes []traceChange
	for k, v := range m {
		decorateResource(eq, v)

		diff, original, err := isElasticQuotaAnnotationDiff(eq, k, v)
		if err != nil {
			return nil, err
		}
		if !diff {
			continue
		}

		if logChanges {
			changes = append(changes, traceChange{key: k, original: original, current: v})
		}

		if newElasticQuota == nil {
			newElasticQuota = eq.DeepCopy()
			if newElasticQuota.Annotations == nil {
				newElasticQuota.Annotations = map[string]string{}
			}
		}
		if err := updateElasticQuotaAnnotation(newElasticQuota, k, v); err != nil {
			return nil, err
		}
	}

	decorateResource(eq, summary.Used)
	if !quotav1.Equals(quotav1.RemoveZeros(eq.Status.Used), quotav1.RemoveZeros(summary.Used)) {
		if logChanges {
			changes = append(changes, traceChange{key: "used", original: eq.Status.Used, current: summary.Used})
		}
		if newElasticQuota == nil {
			newElasticQuota = eq.DeepCopy()
			if newElasticQuota.Annotations == nil {
				newElasticQuota.Annotations = map[string]string{}
			}
		}
		newElasticQuota.Status.Used = summary.Used
	}

	if logChanges && len(changes) > 0 {
		sb := strings.Builder{}
		for i, v := range changes {
			if i > 0 {
				fmt.Fprintf(&sb, ", ")
			}
			fmt.Fprintf(&sb, "%s changed from %v to %v", v.key, printResourceList(v.original), printResourceList(v.current))
		}
		klog.InfoS("ElasticQuota changed", "elasticQuota", eq.Name, "changed", sb.String())
	}

	return newElasticQuota, nil
}

func isElasticQuotaAnnotationDiff(eq *v1alpha1.ElasticQuota, key string, resourceList v1.ResourceList) (bool, v1.ResourceList, error) {
	var originalResourceList v1.ResourceList
	if val := eq.Annotations[key]; val != "" {
		if err := json.Unmarshal([]byte(val), &originalResourceList); err != nil {
			return false, nil, err
		}
	}
	changed := !quotav1.Equals(quotav1.RemoveZeros(originalResourceList), quotav1.RemoveZeros(resourceList))
	return changed, originalResourceList, nil
}

func updateElasticQuotaAnnotation(eq *v1alpha1.ElasticQuota, key string, resourceList v1.ResourceList) error {
	data, err := json.Marshal(resourceList)
	if err != nil {
		return err
	}
	eq.Annotations[key] = string(data)
	return nil
}

func (ctrl *Controller) syncElasticQuotaStatusMetricsWorker() {
	elasticQuotas, err := ctrl.plugin.quotaLister.List(labels.Everything())
	if err != nil {
		klog.V(3).ErrorS(err, "Unable to list elastic quota from store", "elasticQuota")
		return
	}

	for _, eq := range elasticQuotas {
		summary, _ := ctrl.plugin.GetQuotaSummary(eq.Name, false)
		if summary == nil {
			continue
		}
		syncElasticQuotaMetrics(eq, summary)
	}
}

func syncElasticQuotaMetrics(eq *v1alpha1.ElasticQuota, summary *core.QuotaInfoSummary) {
	quotaLabels := map[string]string{
		"name":      summary.Name,
		"tree":      summary.Tree,
		"is_parent": strconv.FormatBool(summary.IsParent),
		"parent":    summary.ParentName,
	}

	m := map[string]v1.ResourceList{
		extension.AnnotationRuntime:               summary.Runtime,
		extension.AnnotationRequest:               summary.Request,
		extension.AnnotationChildRequest:          summary.ChildRequest,
		extension.AnnotationGuaranteed:            summary.Guaranteed,
		extension.AnnotationAllocated:             summary.Allocated,
		extension.AnnotationNonPreemptibleRequest: summary.NonPreemptibleRequest,
		extension.AnnotationNonPreemptibleUsed:    summary.NonPreemptibleUsed,
	}

	// record the unschedulable resource
	if extension.IsTreeRootQuota(eq) {
		unschedulable, err := extension.GetUnschedulableResource(eq)
		if err == nil {
			m[extension.AnnotationUnschedulableResource] = unschedulable
		}
	}

	for k, v := range m {
		decorateResource(eq, v)

		k = strings.TrimPrefix(k, extension.QuotaKoordinatorPrefix+"/")
		RecordElasticQuotaMetric(ElasticQuotaStatusMetric, v, k, quotaLabels)
	}
	decorateResource(eq, summary.Used)
	RecordElasticQuotaMetric(ElasticQuotaStatusMetric, summary.Used, "used", quotaLabels)

	decorateResource(eq, summary.Min)
	decorateResource(eq, summary.Max)
	decorateResource(eq, summary.SharedWeight)
	RecordElasticQuotaMetric(ElasticQuotaSpecMetric, summary.Min, "min", quotaLabels)
	RecordElasticQuotaMetric(ElasticQuotaSpecMetric, summary.Max, "max", quotaLabels)
	RecordElasticQuotaMetric(ElasticQuotaSpecMetric, summary.SharedWeight, "sharedWeight", quotaLabels)
}
