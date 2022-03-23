package system

import (
	"k8s.io/klog/v2"
	"path"
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
