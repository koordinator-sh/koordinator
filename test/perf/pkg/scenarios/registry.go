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

package scenarios

import (
	"fmt"
	"sort"
)

var registry = map[string]func() Scenario{}

// Register adds a scenario factory to the registry.
// Each call to Get returns a fresh Scenario instance, preventing shared
// mutable state across runs. Call from each scenario package's init().
func Register(factory func() Scenario) {
	name := factory().Name()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("scenario %q already registered", name))
	}
	registry[name] = factory
}

// Get returns a new Scenario instance for name, or false if not registered.
func Get(name string) (Scenario, bool) {
	factory, ok := registry[name]
	if !ok {
		return nil, false
	}
	return factory(), true
}

// List returns registered scenario names in deterministic alphabetical order.
func List() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
