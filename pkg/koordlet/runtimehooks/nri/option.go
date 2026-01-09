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

package nri

import (
	"fmt"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	rmconfig "github.com/koordinator-sh/koordinator/pkg/runtimeproxy/config"
)

type Options struct {
	NriPluginName     string
	NriPluginIdx      string
	NriSocketPath     string
	NriConnectTimeout time.Duration
	// support stop running other hooks once someone failed
	PluginFailurePolicy rmconfig.FailurePolicyType
	// todo: add support for disable stages
	DisableStages map[string]struct{}
	Executor      resourceexecutor.ResourceUpdateExecutor
	BackOff       wait.Backoff
	EventRecorder record.EventRecorder
}

func (o Options) Validate() error {
	// a fast check for the NRI support status
	completeNriSocketPath := o.GetNRISocketPath()
	if !system.FileExists(completeNriSocketPath) {
		return fmt.Errorf("nri socket path %q does not exist", completeNriSocketPath)
	}
	return nil
}

func (o Options) GetNRISocketPath() string {
	return filepath.Join(system.Conf.VarRunRootDir, o.NriSocketPath)
}
