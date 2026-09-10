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

	apiext "github.com/koordinator-sh/koordinator/apis/extension"
)

func TestConfiguration_InitFlags(t *testing.T) {
	cmdArgs := []string{
		"",
		"--collect-res-used-interval=2s",
		"--reconcile-interval-seconds=5",
		"--cgroup-root-dir=/host-cgroup/",
		"--feature-gates=AllBeta=true,AllAlpha=false",
	}
	t.Run("ensure not panic", func(t *testing.T) {
		fs := flag.NewFlagSet(cmdArgs[0], flag.ExitOnError)
		cfg := NewConfiguration()
		cfg.InitFlags(fs)
		err := fs.Parse(cmdArgs[1:])
		assert.NoError(t, err)
	})
}

func TestConfiguration_InitFlags_DefaultQoSClassForGuaranteedPods(t *testing.T) {
	original := apiext.QoSClassForGuaranteed
	defer func() {
		apiext.QoSClassForGuaranteed = original
	}()

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg := NewConfiguration()
	cfg.InitFlags(fs)

	err := fs.Parse([]string{"--default-qos-class-for-guaranteed-pods=LS"})
	assert.NoError(t, err)
	assert.Equal(t, apiext.QoSLS, cfg.DefaultQoSClassForGuaranteedPods)
	assert.Equal(t, apiext.QoSLS, apiext.QoSClassForGuaranteed)
}

func TestConfiguration_InitFlags_InvalidDefaultQoSClassForGuaranteedPods(t *testing.T) {
	original := apiext.QoSClassForGuaranteed
	defer func() {
		apiext.QoSClassForGuaranteed = original
	}()

	apiext.QoSClassForGuaranteed = apiext.QoSLSR
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg := NewConfiguration()
	cfg.InitFlags(fs)

	err := fs.Parse([]string{"--default-qos-class-for-guaranteed-pods=invalid"})
	assert.Error(t, err)
	assert.Equal(t, apiext.QoSLSR, cfg.DefaultQoSClassForGuaranteedPods)
	assert.Equal(t, apiext.QoSLSR, apiext.QoSClassForGuaranteed)
}
