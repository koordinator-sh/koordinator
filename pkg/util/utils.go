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

package util

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// MergeCfg returns a merged interface. Value in new will
// override old's when both fields exist.
// It will throw an error if:
//   1. either of the inputs was nil;
//   2. inputs were not a pointer of the same json struct.
func MergeCfg(old, new interface{}) (interface{}, error) {
	if old == nil || new == nil {
		return nil, fmt.Errorf("invalid input, should not be empty")
	}

	if reflect.TypeOf(old) != reflect.TypeOf(new) || reflect.TypeOf(old).Kind() != reflect.Ptr {
		return nil, fmt.Errorf("invalud input, should be the same type")
	}

	if data, err := json.Marshal(new); err != nil {
		return nil, err
	} else if err := json.Unmarshal(data, &old); err != nil {
		return nil, err
	}

	return old, nil
}
