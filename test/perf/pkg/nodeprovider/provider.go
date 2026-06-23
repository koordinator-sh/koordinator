package nodeprovider

import "github.com/koordinator-sh/koordinator/test/perf/pkg/framework"

// NodeProvider is an alias for framework.NodeProvider.
// The interface definition lives in framework to avoid import cycles.
type NodeProvider = framework.NodeProvider
