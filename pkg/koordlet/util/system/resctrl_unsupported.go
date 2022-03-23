//go:build !linux
// +build !linux

package system

import "fmt"

// MountResctrlSubsystem is not supported for non-linux os
func MountResctrlSubsystem() (bool, error) {
	return false, fmt.Errorf("only support linux")
}
