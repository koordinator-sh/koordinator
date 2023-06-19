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

package prediction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/util/histogram"
)

const (
	TmpFileSuffix = ".tmp"
)

// ModelCheckpoint represents a checkpoint for a model.
type ModelCheckpoint struct {
	UID         UIDType
	CPU         *histogram.HistogramCheckpoint
	Memory      *histogram.HistogramCheckpoint
	LastUpdated metav1.Time

	Error error `json:"-,omitempty"`
}

// Checkpointer is an interface for saving and restoring model checkpoints.
type Checkpointer interface {
	Save(checkpoint ModelCheckpoint) error
	Remove(UID UIDType) error
	Restore() ([]*ModelCheckpoint, error)
}

// NewFileCheckpointer creates a new file-based checkpointer with the specified directory.
func NewFileCheckpointer(path string) *fileCheckpointer {
	return &fileCheckpointer{path: path}
}

// fileCheckpointer is an implementation of the Checkpointer interface using files.
type fileCheckpointer struct {
	path string // The directory path where checkpoints are stored.
}

// Save saves the given model as a checkpoint with the specified UID.
func (f *fileCheckpointer) Save(checkpoint ModelCheckpoint) error {
	filename := filepath.Join(f.path, string(checkpoint.UID))
	tmpFilename := filename + TmpFileSuffix
	file, err := os.Create(tmpFilename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(checkpoint); err != nil {
		return err
	}

	return os.Rename(tmpFilename, filename)
}

// Remove removes the given model the specified UID.
func (f *fileCheckpointer) Remove(UID UIDType) error {
	filename := filepath.Join(f.path, string(UID))
	return os.Remove(filename)
}

// Restore returns a slice of ModelCheckpoint instances by scanning and decoding checkpoint files from the specified path.
func (f *fileCheckpointer) Restore() ([]*ModelCheckpoint, error) {
	models := make([]*ModelCheckpoint, 0, 32)
	err := filepath.Walk(f.path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, TmpFileSuffix) {
			err := os.Remove(path)
			klog.InfoS("remove tmp file", path, err)
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			klog.InfoS("open file failed, skip it", path)
			return nil
		}
		defer file.Close()

		checkpoint := &ModelCheckpoint{}
		decoder := json.NewDecoder(file)
		if err := decoder.Decode(checkpoint); err != nil {
			checkpoint.Error = err
		}
		// reset UID to the file name
		checkpoint.UID = UIDType(filepath.Base(path))
		models = append(models, checkpoint)
		return nil
	})
	return models, err
}
