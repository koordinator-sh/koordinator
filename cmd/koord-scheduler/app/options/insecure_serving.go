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
	"fmt"
	"net"

	"github.com/spf13/pflag"
	apiserveroptions "k8s.io/apiserver/pkg/server/options"

	schedulerappconfig "github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app/config"
)

// CombinedInsecureServingOptions sets up to two insecure listeners for healthz and metrics. The flags
// override the ComponentConfig and DeprecatedInsecureServingOptions values for both.
type CombinedInsecureServingOptions struct {
	Healthz *apiserveroptions.DeprecatedInsecureServingOptions

	BindPort    int
	BindAddress string
}

// AddFlags adds flags for the insecure serving options.
func (o *CombinedInsecureServingOptions) AddFlags(fs *pflag.FlagSet) {
	if o == nil {
		return
	}

	fs.StringVar(&o.BindAddress, "address", "0.0.0.0", "DEPRECATED: the IP address on which to listen for the --port port (set to 0.0.0.0 or :: for listening in all interfaces and IP families). See --bind-address instead. This parameter is ignored if a config file is specified in --config.")
	fs.IntVar(&o.BindPort, "port", 10251, "DEPRECATED: the port on which to serve HTTP insecurely without authentication and authorization. If 0, don't serve plain HTTP at all. See --secure-port instead. This parameter is ignored if a config file is specified in --config.")
}

// ApplyTo applies the insecure serving options to the given scheduler app configuration, and updates the componentConfig.
func (o *CombinedInsecureServingOptions) ApplyTo(c *schedulerappconfig.Config) error {
	if o == nil {
		return nil
	}

	if o.Healthz != nil {
		o.Healthz.BindPort = o.BindPort
		o.Healthz.BindAddress = net.ParseIP(o.BindAddress)
	}

	if err := o.Healthz.ApplyTo(&c.InsecureServing); err != nil {
		return err
	}
	return nil
}

// Validate validates the insecure serving options.
func (o *CombinedInsecureServingOptions) Validate() []error {
	if o == nil {
		return nil
	}

	var errors []error
	if o.BindPort < 0 || o.BindPort > 65535 {
		errors = append(errors, fmt.Errorf("--port %v must be between 0 and 65535, inclusive. 0 for turning off insecure (HTTP) port", o.BindPort))
	}

	if len(o.BindAddress) > 0 && net.ParseIP(o.BindAddress) == nil {
		errors = append(errors, fmt.Errorf("--address %v is an invalid IP address", o.BindAddress))
	}

	return errors
}
