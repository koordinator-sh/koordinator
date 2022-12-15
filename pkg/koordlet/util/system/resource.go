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

import (
	"fmt"
	"path/filepath"
	"strconv"

	"k8s.io/klog/v2"
)

type ResourceType string

type Resource interface {
	// ResourceType is the type of system resource. e.g. "cpu.cfs_quota_us", "cpu.cfs_period_us", "schemata"
	ResourceType() ResourceType
	// Path is the generated system file path according to the given parent directory.
	// e.g. "/host-cgroup/kubepods/kubepods-podxxx/cpu.shares"
	Path(parentDir string) string
	// IsSupported checks whether the system resource is supported in current platform
	IsSupported(parentDir string) (bool, string)
	// IsValid checks whether the given value is valid for the system resource's content
	IsValid(v string) (bool, string)
	WithValidator(validator ResourceValidator) Resource
	WithCheckSupported(checkSupportedFn func(r Resource, parentDir string) (newSupported *bool, isSupported bool, msg string)) Resource
}

func GetDefaultResourceType(subfs string, filename string) ResourceType {
	return ResourceType(filepath.Join(subfs, filename))
}

func ValidateResourceValue(value *int64, parentDir string, r Resource) bool {
	if value == nil {
		klog.V(5).Infof("failed to validate cgroup value, path:%s, value is nil", r.Path(parentDir))
		return false
	}
	if valid, msg := r.IsValid(strconv.FormatInt(*value, 10)); !valid {
		klog.V(4).Infof("failed to validate cgroup value, path:%s, msg:%s", r.Path(parentDir), msg)
		return false
	}
	return true
}

func SupportedIfFileExists(r Resource, parentDir string) (*bool, bool, string) {
	exists, err := PathExists(r.Path(parentDir))
	if err != nil {
		return nil, false, fmt.Sprintf("cannot check if %s exists, err: %v", r.ResourceType(), err)
	}
	if !exists {
		return nil, false, "file not exist"
	}
	return nil, true, ""
}

func CheckIfAllSupported(checkSupportedFns ...func() (bool, string)) func() (bool, string) {
	return func() (bool, string) {
		for _, fn := range checkSupportedFns {
			supported, msg := fn()
			if !supported {
				return false, msg
			}
		}
		return true, ""
	}
}
