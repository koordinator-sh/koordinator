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
	"fmt"

	"github.com/mohae/deepcopy"
)

var (
	defaultColocationStrategyExtender = ColocationStrategyExtender{}
)

// +k8s:deepcopy-gen=true
type ColocationStrategyExtender struct {
	Extensions ExtraFields `json:"extensions,omitempty"`
}

// +k8s:deepcopy-gen=false
type ExtraFields map[string]interface{}

func (in *ExtraFields) DeepCopyInto(out *ExtraFields) {
	if in == nil {
		return
	} else {
		outIf := deepcopy.Copy(*in)
		*out = outIf.(ExtraFields)
	}
}

func (in *ExtraFields) DeepCopy() *ExtraFields {
	if in == nil {
		return nil
	}
	out := new(ExtraFields)
	in.DeepCopyInto(out)
	return out
}

func RegisterDefaultColocationExtension(key string, extension interface{}) error {
	if defaultColocationStrategyExtender.Extensions == nil {
		defaultColocationStrategyExtender.Extensions = map[string]interface{}{}
	}
	if _, exist := defaultColocationStrategyExtender.Extensions[key]; exist {
		return fmt.Errorf("extension %v of defaultColocationStrategyExtender already exist", key)
	}
	defaultColocationStrategyExtender.Extensions[key] = extension
	return nil
}

func UnregisterDefaultColocationExtension(key string) {
	delete(defaultColocationStrategyExtender.Extensions, key)
	if len(defaultColocationStrategyExtender.Extensions) == 0 {
		defaultColocationStrategyExtender.Extensions = nil
	}
}

// solving internal ut conflicts
func clearDefaultColocationExtension() {
	defaultColocationStrategyExtender.Extensions = nil
}
