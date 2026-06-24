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

import "fmt"

var registry = map[string]Scenario{}

// Register adds a scenario to the registry.
// Call from each scenario package's init() to enable automatic discovery.
func Register(s Scenario) {
	if _, exists := registry[s.Name()]; exists {
		panic(fmt.Sprintf("scenario %q already registered", s.Name()))
	}
	registry[s.Name()] = s
}

// Get returns the scenario registered under name.
func Get(name string) (Scenario, bool) {
	s, ok := registry[name]
	return s, ok
}

// List returns all registered scenario names.
func List() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
