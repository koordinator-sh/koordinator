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

// Int64Ptr returns a int64 pointer for given value
func Int64Ptr(v int64) *int64 {
	return &v
}

// Float64Ptr returns a float64 pointer for given value
func Float64Ptr(v float64) *float64 {
	return &v
}

// BoolPtr returns a boolean pointer for given value
func BoolPtr(v bool) *bool {
	return &v
}

// StringPtr returns a string pointer for given value
func StringPtr(v string) *string {
	return &v
}

// Merge returns a merged interface. Value in new will
// override old's when both fields exist.
// It will throw an error if:
//   1. either of the inputs was nil;
//   2. inputs were not a pointer of the same json struct.
func Merge(old, new interface{}) (interface{}, error) {
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
