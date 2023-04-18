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

package sloconfig

import (
	"fmt"

	"github.com/koordinator-sh/koordinator/pkg/webhook/util/err"
)

const (
	ReasonOverlap          = err.Reason("NodeStrategiesOverlap")
	ReasonExistNodeConfict = err.Reason("ExistNodeConfict")
	ReasonParseFail        = err.Reason("ParseConfigFail")
	ReasonParamInvalid     = err.Reason("ConfigParamInvalid")
)

type ExsitNodeConflictMessage struct {
	Config                 string   `json:"config,omitempty"`
	ConflictNodeStrategies []string `json:"strategies,omitempty"`
	ExampleNodes           []string `json:"exampleNodes,omitempty"`
}

func buildJsonError(Reason err.Reason, message interface{}) *err.JsonFormatError {
	if message == nil {
		return &err.JsonFormatError{Reason: Reason, Message: fmt.Errorf("UNKNOWN Error")}
	}
	if jsonErr, ok := message.(*err.JsonFormatError); ok {
		jsonErr.Reason = Reason
		return jsonErr
	}
	return &err.JsonFormatError{Reason: Reason, Message: message}
}

func buildParamInvalidError(message error) *err.JsonFormatError {
	if message == nil {
		message = fmt.Errorf("UNKNOWN Error")
	}
	if jsonErr, ok := message.(*err.JsonFormatError); ok {
		return jsonErr
	}
	return &err.JsonFormatError{Reason: ReasonParamInvalid, Message: message.Error()}
}
