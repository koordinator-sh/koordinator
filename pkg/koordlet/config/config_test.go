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

package config

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfiguration_InitFlags(t *testing.T) {
	cmdArgs := []string{
		"",
		"--collect-res-used-interval=2s",
		"--reconcile-interval-seconds=5",
		"--cgroup-root-dir=/host-cgroup/",
		"--feature-gates=AllBeta=true,AllAlpha=false",
		"--enable-kernel-core-sched=false",
	}
	t.Run("ensure not panic", func(t *testing.T) {
		fs := flag.NewFlagSet(cmdArgs[0], flag.ExitOnError)
		cfg := NewConfiguration()
		cfg.InitFlags(fs)
		err := fs.Parse(cmdArgs[1:])
		assert.NoError(t, err)
	})
}
