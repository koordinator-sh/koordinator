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

package metavalidate

import (
	"context"
	"fmt"

	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/scheduler-plugins/pkg/apis/scheduling/v1alpha1"

	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota/plugins"
)

const (
	PluginName            = "QuotaMetaChecker"
	ResourceElasticQuotas = "elasticQuotas"
)

type QuotaMetaChecker struct {
	client.Client
	*admission.Decoder
	quotaTopo *quotaTopology
}

var (
	quotaMetaCheck = &QuotaMetaChecker{
		quotaTopo: nil,
	}
)

func NewPlugin(decoder *admission.Decoder, client client.Client) elasticquota.PluginInterface {
	quotaMetaCheck.Client = client
	quotaMetaCheck.Decoder = decoder
	if quotaMetaCheck.quotaTopo == nil {
		quotaMetaCheck.quotaTopo = NewQuotaTopology()
	}
	return plugins.NewPlugin(PluginName, quotaMetaCheck.Validate, quotaMetaCheck.Admit)
}

func (c *QuotaMetaChecker) Admit(ctx context.Context, req admission.Request, obj runtime.Object) error {
	klog.V(5).Infof("start to admit quota: %+v", obj)

	quotaObj := obj.(*v1alpha1.ElasticQuota)
	op := req.AdmissionRequest.Operation
	switch op {
	case v1.Create:
		return c.quotaTopo.AddQuota(quotaObj)
	case v1.Update:
		oldQuota := &v1alpha1.ElasticQuota{}
		err := c.Decode(admission.Request{
			AdmissionRequest: v1.AdmissionRequest{
				Object: req.AdmissionRequest.OldObject,
			},
		}, oldQuota)
		if err != nil {
			return fmt.Errorf("failed to get quota from old object, err:%+v", err)
		}
		return c.quotaTopo.UpdateQuota(oldQuota, quotaObj)
	case v1.Delete:
		return c.quotaTopo.DeleteQuota(quotaObj.Name)
	}
	return nil
}

func (c *QuotaMetaChecker) Validate(ctx context.Context, req admission.Request, obj runtime.Object) error {
	quotaObj := obj.(*v1alpha1.ElasticQuota)

	klog.V(5).Infof("start to validate quota :%+v", quotaObj)

	switch req.AdmissionRequest.Operation {
	case v1.Delete:
		return c.quotaTopo.ValidateDeleteQuota(quotaObj)
	}
	return nil
}

func GetQuotaTopologyInfo() *QuotaTopologyForMarshal {
	if quotaMetaCheck.quotaTopo == nil {
		return nil
	}
	return quotaMetaCheck.quotaTopo.GetQuotaTopologyInfo()
}
