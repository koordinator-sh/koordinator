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

// Package nodeprovider defines the NodeProvider interface for simulating
// Kubernetes nodes. Importing only pkg/types keeps this package free of
// import cycles with pkg/framework.
package nodeprovider

import (
	"context"
	"time"

	"github.com/koordinator-sh/koordinator/test/perf/pkg/types"
)

// NodeProvider creates and destroys simulated nodes.
// kwok is the default implementation in pkg/nodeprovider/kwok/.
// A future kubemark implementation satisfies this same interface.
type NodeProvider interface {
	// CreateNodes provisions count nodes matching spec, all labelled with runID.
	CreateNodes(ctx context.Context, runID string, spec types.NodeSpec, count int) error
	// DeleteNodes removes all nodes created for runID.
	DeleteNodes(ctx context.Context, runID string) error
	// WaitReady blocks until all nodes for runID are Ready or timeout fires.
	WaitReady(ctx context.Context, runID string, timeout time.Duration) error
}
