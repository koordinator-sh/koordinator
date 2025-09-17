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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/google/renameio"
	"k8s.io/klog/v2"

	resourceconifg "github.com/koordinator-sh/koordinator/cmd/koord-device-daemon/config/v1"
	koordletuti "github.com/koordinator-sh/koordinator/pkg/koordlet/util"
)

// Writer defines a mechanism to output labels and device infos.
type Writer interface {
	OutputPrints(koordletuti.XPUDevices) error
}

// toFile writes to the specified file.
type toFile string

func (path *toFile) OutputPrints(devices koordletuti.XPUDevices) error {
	klog.Infof("Writing device devices (JSON) to output file %v", *path)

	data, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling device devices to JSON: %v", err)
	}

	buffer := bytes.NewBuffer(data)
	if err := renameio.WriteFile(string(*path), buffer.Bytes(), 0644); err != nil {
		return fmt.Errorf("error atomically writing file '%s': %w", *path, err)
	}
	return nil
}

type toWriter struct {
	io.Writer
}

func (output *toWriter) OutputPrints(devices koordletuti.XPUDevices) error {
	return nil
}

func NewPrintsWriter(config *resourceconifg.Config) (Writer, error) {
	path := *config.Flags.KDD.PrintsOutputFile
	if path == "" {
		return &toWriter{os.Stdout}, nil
	}

	o := toFile(path)
	return &o, nil
}
