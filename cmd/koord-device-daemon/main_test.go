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
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	resourceconifg "github.com/koordinator-sh/koordinator/cmd/koord-device-daemon/config/v1"
	"github.com/koordinator-sh/koordinator/pkg/device-daemon/resource"
)

// fakeFailingManager is a resource.Manager whose GetDevices call always
// fails, simulating a vendor tool (e.g. nvidia-smi, xpu-smi) erroring out
// during device discovery.
type fakeFailingManager struct {
	vendor string
	err    error
}

func (f *fakeFailingManager) GetDeviceVendor() string {
	return f.vendor
}

func (f *fakeFailingManager) GetDevices() ([]resource.Device, error) {
	return nil, f.err
}

func (f *fakeFailingManager) GetDriverVersion() (string, error) {
	return "", nil
}

// TestRun_PropagatesPrinterInitError guards against a regression of the bug
// where (*resourceFeatureDiscovery).run silently swallowed errors returned
// by printmanager.NewPrinters and returned (false, nil), making a failed
// discovery run look like a successful one.
func TestRun_PropagatesPrinterInitError(t *testing.T) {
	underlying := errors.New("nvidia-smi: device query failed")

	oneshot := true
	rfd := &resourceFeatureDiscovery{
		manager: map[string]resource.Manager{
			"nvidia": &fakeFailingManager{vendor: "nvidia", err: underlying},
		},
		config: &resourceconifg.Config{
			Flags: resourceconifg.Flags{
				CommandLineFlags: resourceconifg.CommandLineFlags{
					KDD: &resourceconifg.KDDCommandLineFlags{
						Oneshot:       &oneshot,
						SleepInterval: &metav1.Duration{},
					},
				},
			},
		},
	}

	restart, err := rfd.run(make(chan os.Signal, 1))

	require.Error(t, err, "run() must not swallow the printer initialization error")
	assert.False(t, restart)
	// printmanager.NewPrinters wraps the underlying error with fmt.Errorf's
	// %v verb rather than %w, so the original error's message text is
	// preserved but its identity is not chainable via errors.Is/errors.As.
	assert.Contains(t, err.Error(), underlying.Error(), "the original error message must be preserved")
	assert.Contains(t, err.Error(), "nvidia", "the error should retain vendor context")
}
