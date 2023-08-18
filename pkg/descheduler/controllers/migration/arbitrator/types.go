/*
Copyright 2023 The Koordinator Authors.

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

package arbitrator

import (
	"github.com/koordinator-sh/koordinator/apis/scheduling/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

type Arbitrator interface {
	handler.EventHandler

	Arbitrate(stopCh <-chan struct{})

	AddJob(job *v1alpha1.PodMigrationJob)
}

// SortFn stably sorts PodMigrationJobs slice based on a certain strategy. Users
// can implement different SortFn according to their needs.
type SortFn func(jobs []*v1alpha1.PodMigrationJob) []*v1alpha1.PodMigrationJob

// GroupFilterFn group and filter jobs according to workload.
type GroupFilterFn func(jobs []*v1alpha1.PodMigrationJob) []*v1alpha1.PodMigrationJob
