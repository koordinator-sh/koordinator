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

package mamanger

import (
	"fmt"

	resourceconifg "github.com/koordinator-sh/koordinator/cmd/koord-device-daemon/config/v1"
	"github.com/koordinator-sh/koordinator/pkg/device-daemon/resource"
)

// Printer defines an interface for generating device printers
type Printer interface {
	Prints() (DevicePrinters, error)
}

// NewPrinter constructs the required printer from the specified config
func NewPrinter(manager map[string]resource.Manager, config *resourceconifg.Config) (Printer, error) {
	devicePrinter, err := NewDevicePrinter(manager, config)
	if err != nil {
		return nil, fmt.Errorf("error creating devicePrinter: %v", err)
	}

	return devicePrinter, nil
}
