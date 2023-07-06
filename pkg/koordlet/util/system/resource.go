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
	"strings"

	"k8s.io/klog/v2"
)

const ErrResourceUnsupportedPrefix = "resource is unsupported"

type ResourceType string

type Resource interface {
	// ResourceType is the type of system resource. e.g. "cpu.cfs_quota_us", "cpu.cfs_period_us", "schemata"
	ResourceType() ResourceType
	// Path is the generated system file path according to the given parent directory.
	// e.g. "/host-cgroup/kubepods/kubepods-podxxx/cpu.shares"
	Path(dynamicPath string) string
	// IsSupported checks whether the system resource is supported in current platform
	IsSupported(dynamicPath string) (bool, string)
	// IsValid checks whether the given value is valid for the system resource's content
	IsValid(v string) (bool, string)
	// WithValidator sets the ResourceValidator for the resource
	WithValidator(validator ResourceValidator) Resource
	// WithSupported sets the Supported status of the resource when it is initialized.
	WithSupported(supported bool, msg string) Resource
	// WithCheckSupported sets the check function for the Supported status of given resource and parent directory.
	WithCheckSupported(checkSupportedFn func(r Resource, dynamicPath string) (isSupported bool, msg string)) Resource
	// WithCheckOnce sets the check function only checking once and then use the result as the Supported status.
	WithCheckOnce(isCheckOnce bool) Resource
}

func GetDefaultResourceType(subfs string, filename string) ResourceType {
	return ResourceType(filepath.Join(subfs, filename))
}

func ValidateResourceValue(value *int64, dynamicPath string, r Resource) bool {
	if value == nil {
		klog.V(5).Infof("failed to validate value, path:%s, value is nil", r.Path(dynamicPath))
		return false
	}
	if valid, msg := r.IsValid(strconv.FormatInt(*value, 10)); !valid {
		klog.V(4).Infof("failed to validate value, path:%s, msg:%s", r.Path(dynamicPath), msg)
		return false
	}
	return true
}

func ResourceUnsupportedErr(msg string) error {
	return fmt.Errorf("%s, reason: %s", ErrResourceUnsupportedPrefix, msg)
}

func IsResourceUnsupportedErr(err error) bool {
	return strings.HasPrefix(err.Error(), ErrResourceUnsupportedPrefix)
}

func SupportedIfFileExistsInKubepods(r Resource, _ string) (bool, string) {
	p := r.Path(CgroupPathFormatter.ParentDir)
	exists, err := PathExists(p)
	if err != nil {
		return false, fmt.Sprintf("cannot check if %s exists in kubepods cgroup, err: %v", r.ResourceType(), err)
	}
	if !exists {
		return false, "file not exist in kubepods cgroup"
	}
	return true, ""
}

func SupportedIfFileExists(r Resource, dynamicPath string) (bool, string) {
	exists, err := PathExists(r.Path(dynamicPath))
	if err != nil {
		return false, fmt.Sprintf("cannot check if %s exists, err: %v", r.ResourceType(), err)
	}
	if !exists {
		return false, "file not exist"
	}
	return true, ""
}

func SupportedIfFileExistsInRootCgroup(filename string, subfs string) (bool, string) {
	exists, err := PathExists(filepath.Join(Conf.CgroupRootDir, subfs, filename))
	if err != nil {
		return false, fmt.Sprintf("cannot check if %s exists in root cgroup, err: %v", filename, err)
	}
	if !exists {
		return false, "file not exist in root cgroup"
	}
	return true, ""
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
