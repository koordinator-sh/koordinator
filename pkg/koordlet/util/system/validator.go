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

package system

import "fmt"

type Validate interface {
	Validate(value *int64) (isValid bool, msg string)
}

type RangeValidator struct {
	name string
	max  int64
	min  int64
}

func (r *RangeValidator) Validate(value *int64) (isValid bool, msg string) {
	isValid = false
	if value == nil {
		msg = fmt.Sprintf("Value is not valid!%s value is nil!", r.name)
		return
	}
	isValid = *value >= r.min && *value <= r.max
	if !isValid {
		msg = fmt.Sprintf("%s Value(%d) is not valid!min:%d,max:%d", r.name, *value, r.min, r.max)
	}
	return
}

// Int64Ptr returns a int64 pointer for given value
func Int64Ptr(v int64) *int64 {
	return &v
}
