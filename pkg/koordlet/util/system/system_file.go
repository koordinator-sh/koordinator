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
	"path"

	"k8s.io/klog/v2"
)

const (
	ProcSysVmRelativePath = "sys/vm/"

	MinFreeKbytesFileName        = "min_free_kbytes"
	WatermarkScaleFactorFileName = "watermark_scale_factor"
	ProcStatFileName             = "stat"
)

type SystemFile struct {
	File      string
	Validator Validate
}

var (
	MinFreeKbytesValidator        = &RangeValidator{name: MinFreeKbytesFileName, min: 10, max: 400}
	WatermarkScaleFactorValidator = &RangeValidator{name: WatermarkScaleFactorFileName, min: 10, max: 400}
)

var (
	MinFreeKbytesFile        SystemFile
	WatermarkScaleFactorFile SystemFile
	ProcStatFile             SystemFile
)

func init() {
	initFilePath()
}

func initFilePath() {
	MinFreeKbytesFile = SystemFile{File: path.Join(Conf.ProcRootDir, ProcSysVmRelativePath, MinFreeKbytesFileName), Validator: MinFreeKbytesValidator}
	WatermarkScaleFactorFile = SystemFile{File: path.Join(Conf.ProcRootDir, ProcSysVmRelativePath, WatermarkScaleFactorFileName), Validator: WatermarkScaleFactorValidator}
	ProcStatFile = SystemFile{File: path.Join(Conf.ProcRootDir, ProcStatFileName)}
}

func ValidateValue(value *int64, file SystemFile) bool {
	if value == nil {
		klog.V(5).Infof("validate fail,file:%s , value is nil!", file.File)
		return false
	}
	if file.Validator != nil {
		valid, msg := file.Validator.Validate(value)
		if !valid {
			klog.Warningf("validate fail!msg: %s", msg)
		}
		return valid
	}

	return true
}
