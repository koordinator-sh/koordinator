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

package options

import (
	"flag"
	"fmt"
	"strings"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type Options struct {
	ControllerAddFuncs map[string]func(manager.Manager) error
	Controllers        []string
}

func NewOptions() *Options {
	return &Options{
		ControllerAddFuncs: controllerAddFuncs,
		Controllers:        sets.StringKeySet(controllerAddFuncs).List(),
	}
}

func (o *Options) InitFlags(fs *flag.FlagSet) {
	pflag.StringSliceVar(&o.Controllers, "controllers", o.Controllers, fmt.Sprintf("A list of controllers to enable. "+
		"'-controllers=*' enables all controllers. "+
		"'-controllers=noderesource' means only the 'noderesource' controller is enabled. "+
		"'-controllers=*,-noderesource' means all controllers except the 'noderesource' controller are enabled.\n"+
		"All controllers: %s", strings.Join(o.Controllers, ", ")))
}

func (o *Options) ApplyTo(m manager.Manager) error {
	for controllerName, addFn := range o.ControllerAddFuncs {
		if !isControllerEnabled(controllerName, o.Controllers) {
			klog.Warningf("controller %q is disabled", controllerName)
			continue
		}

		if err := addFn(m); err != nil {
			klog.Errorf("Unable to create controller %s, err: %v", controllerName, err)
			return err
		} else {
			klog.V(4).Infof("controller %q added", controllerName)
		}
	}

	return nil
}

func isControllerEnabled(controllerName string, controllers []string) bool {
	hasStar := false
	for _, c := range controllers {
		if c == controllerName {
			return true
		}
		if c == "-"+controllerName {
			return false
		}
		if c == "*" {
			hasStar = true
		}
	}
	return hasStar
}
