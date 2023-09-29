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
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"
	"sigs.k8s.io/scheduler-plugins/pkg/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	koordutil "github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	SyncHandlerCycle           = 1 * time.Second
	ElasticQuotaControllerName = "QuotaCRDController"
)

var resourceDecorators = []func(quota *v1alpha1.ElasticQuota, resource v1.ResourceList){}

func decorateResource(quota *v1alpha1.ElasticQuota, resource v1.ResourceList) {
	for _, fn := range resourceDecorators {
		fn(quota, resource)
	}
}

// Controller is a controller that update elastic quota crd
type Controller struct {
	plugin *Plugin
}

// NewElasticQuotaController returns a new *Controller
func NewElasticQuotaController(
	plugin *Plugin,
	newOpt ...func(ctrl *Controller),
) *Controller {
	// set up elastic quota ctrl
	ctrl := &Controller{
		plugin: plugin,
	}
	for _, f := range newOpt {
		f(ctrl)
	}

	return ctrl
}

func (ctrl *Controller) Name() string {
	return ElasticQuotaControllerName
}

func (ctrl *Controller) Start() {
	go wait.Until(ctrl.Run, SyncHandlerCycle, context.TODO().Done())
	klog.Infof("start elasticQuota controller syncHandler")
}

func (ctrl *Controller) Run() {
	if errs := ctrl.syncHandler(); len(errs) != 0 {
		for _, err := range errs {
			utilruntime.HandleError(err)
		}
	}
}

// syncHandler syncs elastic quotas with local and convert status.used/request/runtime
func (ctrl *Controller) syncHandler() []error {
	eqList, err := ctrl.plugin.quotaLister.List(labels.Everything())
	if err != nil {
		klog.V(3).ErrorS(err, "Unable to list elastic quota from store", "elasticQuota")
		return []error{err}
	}
	errors := make([]error, 0)

	for _, eq := range eqList {
		func() {
			mgr := ctrl.plugin.GetGroupQuotaManagerForQuota(eq.Name)
			if mgr == nil {
				errors = append(errors, fmt.Errorf("failed get quota manager for %v", eq.Name))
				return
			}
			used, request, childRequest, runtime, guaranteed, allocated, err := mgr.GetQuotaInformationForSyncHandler(eq.Name)
			if err != nil {
				errors = append(errors, err)
				return
			}

			// decorate resources.
			// It will convert unit or remove some resources which we don't want to expose.
			decorateResource(eq, used)
			decorateResource(eq, request)
			decorateResource(eq, childRequest)
			decorateResource(eq, runtime)
			decorateResource(eq, guaranteed)
			decorateResource(eq, allocated)

			var oriRuntime, oriRequest, oriChildRequest, oriGuaranteed, oriAllocated v1.ResourceList
			if eq.Annotations[extension.AnnotationRequest] != "" {
				if err := json.Unmarshal([]byte(eq.Annotations[extension.AnnotationRequest]), &oriRequest); err != nil {
					errors = append(errors, err)
					return
				}
			}
			if eq.Annotations[extension.AnnotationChildRequest] != "" {
				if err := json.Unmarshal([]byte(eq.Annotations[extension.AnnotationChildRequest]), &oriChildRequest); err != nil {
					errors = append(errors, err)
					return
				}
			}
			if eq.Annotations[extension.AnnotationRuntime] != "" {
				if err := json.Unmarshal([]byte(eq.Annotations[extension.AnnotationRuntime]), &oriRuntime); err != nil {
					errors = append(errors, err)
					return
				}
			}

			oriGuaranteed, err = extension.GetGuaranteed(eq)
			if err != nil {
				errors = append(errors, err)
				return
			}

			oriAllocated, err = extension.GetAllocated(eq)
			if err != nil {
				errors = append(errors, err)
				return
			}

			// Ignore this loop if the runtime/request/used doesn't change
			if quotav1.Equals(quotav1.RemoveZeros(eq.Status.Used), quotav1.RemoveZeros(used)) &&
				quotav1.Equals(quotav1.RemoveZeros(oriRuntime), quotav1.RemoveZeros(runtime)) &&
				quotav1.Equals(quotav1.RemoveZeros(oriRequest), quotav1.RemoveZeros(request)) &&
				quotav1.Equals(quotav1.RemoveZeros(oriChildRequest), quotav1.RemoveZeros(childRequest)) &&
				quotav1.Equals(quotav1.RemoveZeros(oriGuaranteed), quotav1.RemoveZeros(guaranteed)) &&
				quotav1.Equals(quotav1.RemoveZeros(oriAllocated), quotav1.RemoveZeros(allocated)) {
				return
			}
			newEQ := eq.DeepCopy()
			if newEQ.Annotations == nil {
				newEQ.Annotations = make(map[string]string)
			}
			runtimeBytes, err := json.Marshal(runtime)
			if err != nil {
				errors = append(errors, err)
				return
			}
			requestBytes, err := json.Marshal(request)
			if err != nil {
				errors = append(errors, err)
				return
			}
			childRequestBytes, err := json.Marshal(childRequest)
			if err != nil {
				errors = append(errors, err)
				return
			}
			guaranteedBytes, err := json.Marshal(guaranteed)
			if err != nil {
				errors = append(errors, err)
				return
			}
			allocatedBytes, err := json.Marshal(allocated)
			if err != nil {
				errors = append(errors, err)
				return
			}
			newEQ.Annotations[extension.AnnotationRuntime] = string(runtimeBytes)
			newEQ.Annotations[extension.AnnotationRequest] = string(requestBytes)
			newEQ.Annotations[extension.AnnotationChildRequest] = string(childRequestBytes)
			newEQ.Annotations[extension.AnnotationGuaranteed] = string(guaranteedBytes)
			newEQ.Annotations[extension.AnnotationAllocated] = string(allocatedBytes)
			newEQ.Status.Used = used

			klog.V(5).Infof("quota: %v, oldUsed: %v, newUsed: %v, oldRuntime: %v, newRuntime: %v, oldRequest: %v, newRequest: %v, oldChildRequest: %v, newChildRequest: %v, oldGuarantee: %v, newGuarantee: %v, oldAllocated: %v, newAllocated: %v",
				eq.Name, eq.Status.Used, used, eq.Annotations[extension.AnnotationRuntime], string(runtimeBytes),
				eq.Annotations[extension.AnnotationRequest], string(requestBytes),
				eq.Annotations[extension.AnnotationChildRequest], string(childRequestBytes),
				eq.Annotations[extension.AnnotationGuaranteed], string(guaranteedBytes),
				eq.Annotations[extension.AnnotationAllocated], string(allocatedBytes))

			patch, err := util.CreateMergePatch(eq, newEQ)
			if err != nil {
				errors = append(errors, err)
				return
			}
			err = koordutil.RetryOnConflictOrTooManyRequests(func() error {
				_, patchErr := ctrl.plugin.client.SchedulingV1alpha1().ElasticQuotas(eq.Namespace).
					Patch(context.TODO(), eq.Name, types.MergePatchType,
						patch, metav1.PatchOptions{})
				return patchErr
			})
			if err != nil {
				errors = append(errors, err)
				return
			}
		}()
	}
	return errors
}
