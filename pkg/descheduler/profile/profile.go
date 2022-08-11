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

package profile

import (
	"fmt"

	"k8s.io/client-go/tools/events"

	deschedulerconfig "github.com/koordinator-sh/koordinator/pkg/descheduler/apis/config"
	"github.com/koordinator-sh/koordinator/pkg/descheduler/framework"
	frameworkruntime "github.com/koordinator-sh/koordinator/pkg/descheduler/framework/runtime"
)

// RecorderFactory builds an EventRecorder for a given scheduler name.
type RecorderFactory func(string) events.EventRecorder

// newProfile builds a DeschedulerProfile for the given configuration.
func newProfile(profile deschedulerconfig.DeschedulerProfile, r frameworkruntime.Registry, recorderFactory RecorderFactory, opts ...frameworkruntime.Option) (framework.Handle, error) {
	eventRecorder := recorderFactory(profile.Name)
	opts = append(opts, frameworkruntime.WithEventRecorder(eventRecorder))
	return frameworkruntime.NewFramework(r, &profile, opts...)
}

// Map holds frameworks indexed by scheduler name.
type Map map[string]framework.Handle

// NewMap builds the frameworks given by the configuration, indexed by name.
func NewMap(profiles []deschedulerconfig.DeschedulerProfile, r frameworkruntime.Registry, recorderFactory RecorderFactory, opts ...frameworkruntime.Option) (Map, error) {
	m := make(Map)
	for _, profileCfg := range profiles {
		p, err := newProfile(profileCfg, r, recorderFactory, opts...)
		if err != nil {
			return nil, fmt.Errorf("creating profile for descheduler name %s: %v", profileCfg.Name, err)
		}
		m[profileCfg.Name] = p
	}
	return m, nil
}
