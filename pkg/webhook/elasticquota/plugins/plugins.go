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

package plugins

import (
	"context"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/webhook/elasticquota"
)

var (
	DefaultEnabledPlugins sets.String
)

type NewPluginFunc func(d *admission.Decoder, client client.Client) elasticquota.PluginInterface

type Plugin struct {
	name           string
	validationFunc elasticquota.ValidationFunc
	mutationFunc   elasticquota.MutationFunc
}

func NewPlugin(name string, vf elasticquota.ValidationFunc, mf elasticquota.MutationFunc) elasticquota.PluginInterface {
	return &Plugin{
		name:           name,
		validationFunc: vf,
		mutationFunc:   mf,
	}
}

func (p Plugin) Name() string {
	return p.name
}

func (p Plugin) Validate(ctx context.Context, req admission.Request, obj runtime.Object) error {
	if p.validationFunc == nil {
		return fmt.Errorf("plugin %s does not implement Validation", p.name)
	}

	// Deep copy this object for each validating plugin, in case someone modifies it
	var newObj runtime.Object
	if obj != nil {
		newObj = obj.DeepCopyObject()
	}
	err := p.validationFunc(ctx, req, newObj)
	// validateTime.WithLabelValues(p.name).Observe(time.Since(startTime).Seconds())

	if err != nil {
		// validateErrors.WithLabelValues(p.name).Inc()
		err = fmt.Errorf("plugin %s validate for %s/%s failed: %v",
			p.name, req.AdmissionRequest.Namespace, req.AdmissionRequest.Name, err)
		klog.Warningf("%v, object: %v", err, util.DumpJSON(obj))
	}

	// if object changed during validating, return error
	if !reflect.DeepEqual(obj, newObj) {
		// validateErrors.WithLabelValues(p.name).Inc()
		err = fmt.Errorf("plugin %s validate for %s/%s failed: %v",
			p.name, req.AdmissionRequest.Namespace, req.AdmissionRequest.Name, err)
		klog.Warningf("%v, object: %v", err, util.DumpJSON(obj))
	}

	return err
}

func (p Plugin) Admit(ctx context.Context, req admission.Request, obj runtime.Object) error {
	if p.mutationFunc == nil {
		return fmt.Errorf("plugin %s does not implement Mutation", p.Name())
	}

	err := p.mutationFunc(ctx, req, obj)
	if err != nil {
		err = fmt.Errorf("plugin %s admit for %s/%s failed: %v",
			p.name, req.AdmissionRequest.Namespace, req.AdmissionRequest.Name, err)
		klog.Warningf("%v, object: %v", err, util.DumpJSON(obj))
	}
	return err
}
