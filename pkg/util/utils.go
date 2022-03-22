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
