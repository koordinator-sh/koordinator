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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	quotav1 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/klog/v2"
	schedclientset "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"
	schedlister "sigs.k8s.io/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"
	"sigs.k8s.io/scheduler-plugins/pkg/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
	koordutil "github.com/koordinator-sh/koordinator/pkg/util"
)

const (
	SyncHandlerCycle = 1 * time.Second
)

// Controller is a controller that update elastic quota crd
type Controller struct {
	// schedClient is a clientSet for SchedulingV1alpha1 API group
	schedClient       schedclientset.Interface
	eqLister          schedlister.ElasticQuotaLister
	groupQuotaManager *core.GroupQuotaManager
}

// NewElasticQuotaController returns a new *Controller
func NewElasticQuotaController(
	client schedclientset.Interface,
	eqLister schedlister.ElasticQuotaLister,
	groupQuotaManager *core.GroupQuotaManager,
	newOpt ...func(ctrl *Controller),
) *Controller {
	// set up elastic quota ctrl
	ctrl := &Controller{
		schedClient:       client,
		eqLister:          eqLister,
		groupQuotaManager: groupQuotaManager,
	}
	for _, f := range newOpt {
		f(ctrl)
	}

	return ctrl
}

func (ctrl *Controller) Start() {
	go wait.Until(ctrl.Run, SyncHandlerCycle, context.TODO().Done())
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
	eqList, err := ctrl.eqLister.List(labels.Everything())
	if err != nil {
		klog.V(3).ErrorS(err, "Unable to list elastic quota from store", "elasticQuota")
		return []error{err}
	}
	errors := make([]error, 0)

	for _, eq := range eqList {
		func() {
			ctrl.groupQuotaManager.RLock()
			defer ctrl.groupQuotaManager.RUnLock()

			klog.V(5).InfoS("Try to process elastic quota", "elasticQuota", eq.Name)

			quotaInfo := ctrl.groupQuotaManager.GetQuotaInfoByNameNoLock(eq.Name)
			if quotaInfo == nil {
				errors = append(errors, fmt.Errorf("qroupQuotaManager has not have this quota:%v", eq.Name))
				return
			}
			used := quotaInfo.GetUsed()
			runtime, _ := json.Marshal(ctrl.groupQuotaManager.RefreshRuntimeNoLock(eq.Name))
			request, _ := json.Marshal(quotaInfo.GetRequest())

			// Ignore this loop if the runtime/request/used doesn't change
			if quotav1.Equals(eq.Status.Used, used) &&
				eq.Annotations[extension.AnnotationRuntime] == string(runtime) &&
				eq.Annotations[extension.AnnotationRequest] == string(request) {
				return
			}
			klog.V(5).Infof("quota:%v, oldUsed:%v, newUsed:%v, oldRuntime:%v, newRuntime:%v, oldRequest:%v, newRequest:%v",
				eq.Name, eq.Status.Used, used, eq.Annotations[extension.AnnotationRuntime], string(runtime),
				eq.Annotations[extension.AnnotationRequest], string(request))
			newEQ := eq.DeepCopy()
			if newEQ.Annotations == nil {
				newEQ.Annotations = make(map[string]string)
			}
			newEQ.Annotations[extension.AnnotationRuntime] = string(runtime)
			newEQ.Annotations[extension.AnnotationRequest] = string(request)
			newEQ.Status.Used = used

			patch, err := util.CreateMergePatch(eq, newEQ)
			if err != nil {
				errors = append(errors, err)
				return
			}
			err = koordutil.RetryOnConflictOrTooManyRequests(func() error {
				_, patchErr := ctrl.schedClient.SchedulingV1alpha1().ElasticQuotas(eq.Namespace).
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
