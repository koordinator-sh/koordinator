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
	controllerruntime "sigs.k8s.io/controller-runtime"

	"github.com/koordinator-sh/koordinator/cmd/koord-runtime-proxy/app"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	logs.InitLogs()
	defer logs.FlushLogs()

	ctx := controllerruntime.SetupSignalHandler()
	command := app.NewRuntimeProxyCommand(ctx)
	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
