//go:build !linux
// +build !linux

package system

func GuessCgroupDriverFromCgroupName() CgroupDriverType {
	return ""
}

func GuessCgroupDriverFromKubelet() (CgroupDriverType, error) {
	return kubeletDefaultCgroupDriver, nil
}
