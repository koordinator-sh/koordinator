//go:build !linux
// +build !linux

package system

import (
	"fmt"
)

func ProcCmdLine(procRoot string, pid int) ([]string, error) {
	return []string{}, fmt.Errorf("only support linux")
}

var PidOf = pidOfFn

func pidOfFn(procRoot string, name string) ([]int, error) {
	return []int{}, fmt.Errorf("only support linux")
}

var ExecCmdOnHost = execCmdOnHostFn

func execCmdOnHostFn(cmds []string) ([]byte, int, error) {
	return nil, -1, fmt.Errorf("only support linux")
}
