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

// Get returns the scenario registered under name, or false if not found.
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
