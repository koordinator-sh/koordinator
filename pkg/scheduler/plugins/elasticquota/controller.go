/*
Copyright 2020 The Kubernetes Authors.
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
	"reflect"

	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	schedclientset "sigs.k8s.io/scheduler-plugins/pkg/generated/clientset/versioned"
	schedlister "sigs.k8s.io/scheduler-plugins/pkg/generated/listers/scheduling/v1alpha1"
	"sigs.k8s.io/scheduler-plugins/pkg/util"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota/core"
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
	kubeClient kubernetes.Interface,
	eqLister schedlister.ElasticQuotaLister,
	schedClient schedclientset.Interface,
	groupQuotaManager *core.GroupQuotaManager,
	newOpt ...func(ctrl *Controller),
) *Controller {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{
		Interface: kubeClient.CoreV1().Events(v1.NamespaceAll),
	})
	//set up elastic quota ctrl
	ctrl := &Controller{
		schedClient:       schedClient,
		eqLister:          eqLister,
		groupQuotaManager: groupQuotaManager,
	}
	for _, f := range newOpt {
		f(ctrl)
	}
	return ctrl
}

func (ctrl *Controller) Run() {
	if err := ctrl.syncHandler(); err != nil {
		utilruntime.HandleError(err)
	}
}

// syncHandler syncs elastic quotas with local and convert status.used/request/runtime
func (ctrl *Controller) syncHandler() error {
	eqList, err := ctrl.eqLister.List(labels.Everything())
	if err != nil {
		klog.V(3).ErrorS(err, "Unable to list elastic quota from store", "elasticQuota")
		return err
	}

	for _, eq := range eqList {
		klog.V(5).InfoS("Try to process elastic quota", "elasticQuota", eq.Name)

		quotaInfo := ctrl.groupQuotaManager.GetQuotaInfoByName(eq.Name)
		newEQ := eq.DeepCopy()
		newEQ.Status.Used = quotaInfo.GetUsed()

		if newEQ.Annotations == nil {
			newEQ.Annotations = make(map[string]string)
		}
		runtime, _ := json.MarshalIndent(quotaInfo.GetRuntime(), "", "")
		request, _ := json.MarshalIndent(quotaInfo.GetRequest(), "", "")
		newEQ.Annotations[extension.AnnotationRuntime] = string(runtime)
		newEQ.Annotations[extension.AnnotationRequest] = string(request)

		// Ignore this loop if the usage value has not changed and the annotation doesn't change
		if apiequality.Semantic.DeepEqual(newEQ.Status, eq.Status) && reflect.DeepEqual(newEQ.Annotations, eq.Annotations) {
			return nil
		}
		patch, err := util.CreateMergePatch(eq, newEQ)
		if err != nil {
			return err
		}
		if _, err = ctrl.schedClient.SchedulingV1alpha1().ElasticQuotas(eq.Namespace).
			Patch(context.TODO(), eq.Name, types.MergePatchType,
				patch, metav1.PatchOptions{}); err != nil {
			return err
		}
	}

	return nil
}
