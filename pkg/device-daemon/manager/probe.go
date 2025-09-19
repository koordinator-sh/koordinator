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

func NewDevicePrinter(manager map[string]resource.Manager, config *resourceconifg.Config) (Printer, error) {
	printers, err := getPrinters(manager)
	if err != nil {
		return nil, fmt.Errorf("failed to get printers: %v", err)
	}

	return printers, nil
}

func getPrinters(manager map[string]resource.Manager) (Printer, error) {
	var printers PrinterList
	for name, manager := range manager {
		if printerFunc, ok := PrinterMap[name]; ok {
			printer, err := printerFunc(manager)
			if err != nil {
				return nil, fmt.Errorf("error creating printer: %v", err)
			}
			printers = append(printers, printer)
		}
	}
	return printers, nil
}
