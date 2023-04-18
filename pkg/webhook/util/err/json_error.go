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

package err

import (
	"encoding/json"
	"fmt"
)

type Reason string

var _ error = &JsonFormatError{}

var contentStart = "___"
var contentEnd = "___"

type JsonFormatError struct {
	Reason  Reason      `json:"reason,omitempty"`
	Message interface{} `json:"message,omitempty"`
}

func (e *JsonFormatError) Error() string {
	if e == nil {
		return ""
	}
	errorBytes, err := json.Marshal(e)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%s%s%s", contentStart, string(errorBytes), contentEnd)
}
