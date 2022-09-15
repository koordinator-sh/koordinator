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

package main

import (
	"math/rand"
	"os"
	"time"

	"k8s.io/component-base/logs"

	"github.com/koordinator-sh/koordinator/cmd/koord-scheduler/app"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/frameworkext"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/batchresource"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/compatibledefaultpreemption"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/coscheduling"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/deviceshare"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/elasticquota"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/loadaware"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/nodenumaresource"
	"github.com/koordinator-sh/koordinator/pkg/scheduler/plugins/reservation"

	// Ensure scheme package is initialized.
	_ "github.com/koordinator-sh/koordinator/apis/scheduling/config/scheme"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	// Register custom scheduling hooks for pre-process scheduling context before call plugins.
	// e.g. change the nodeInfo and make a copy before calling filter plugins
	schedulingHooks := []frameworkext.SchedulingPhaseHook{
		reservation.NewHook(),
	}

	// Register custom plugins to the scheduler framework.
	// Later they can consist of scheduler profile(s) and hence
	// used by various kinds of workloads.
	command := app.NewSchedulerCommand(
		schedulingHooks,
		app.WithPlugin(loadaware.Name, loadaware.New),
		app.WithPlugin(nodenumaresource.Name, nodenumaresource.New),
		app.WithPlugin(compatibledefaultpreemption.Name, compatibledefaultpreemption.New),
		app.WithPlugin(reservation.Name, reservation.New),
		app.WithPlugin(batchresource.Name, batchresource.New),
		app.WithPlugin(coscheduling.Name, coscheduling.New),
		app.WithPlugin(deviceshare.Name, deviceshare.New),
		app.WithPlugin(elasticquota.Name, elasticquota.New),
	)

	logs.InitLogs()
	defer logs.FlushLogs()

	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
