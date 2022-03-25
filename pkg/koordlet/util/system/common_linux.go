//go:build linux
// +build linux

package system

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"k8s.io/klog/v2"
)

// CmdLine returns the command line args of a process.
func ProcCmdLine(procRoot string, pid int) ([]string, error) {
	data, err := ReadFileNoStat(path.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}

	if len(data) < 1 {
		return []string{}, nil
	}

	return strings.Split(string(bytes.TrimRight(data, string("\x00"))), string(byte(0))), nil
}

// PidOf finds process(es) with a specified name (regexp match)
// and return their pid(s).
var PidOf = pidOfFn

// From k8s.io/kubernetes/pkg/util/procfs/procfs_linux.go
// caller should specify proc root dir
func pidOfFn(procRoot string, name string) ([]int, error) {
	if len(name) == 0 {
		return []int{}, fmt.Errorf("name should not be empty")
	}
	re, err := regexp.Compile("(^|/)" + name + "$")
	if err != nil {
		return []int{}, err
	}
	return getPids(procRoot, re), nil
}

func getPids(procRoot string, re *regexp.Regexp) []int {
	pids := []int{}

	dirFD, err := os.Open(procRoot)
	if err != nil {
		return nil
	}
	defer dirFD.Close()

	for {
		// Read a small number at a time in case there are many entries, we don't want to
		// allocate a lot here.
		ls, err := dirFD.Readdir(10)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}

		for _, entry := range ls {
			if !entry.IsDir() {
				continue
			}

			// If the directory is not a number (i.e. not a PID), skip it
			pid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}

			cmdline, err := ioutil.ReadFile(filepath.Join(procRoot, entry.Name(), "cmdline"))
			if err != nil {
				klog.V(4).Infof("Error reading file %s: %+v", filepath.Join(procRoot, entry.Name(), "cmdline"), err)
				continue
			}

			// The bytes we read have '\0' as a separator for the command line
			parts := bytes.SplitN(cmdline, []byte{0}, 2)
			if len(parts) == 0 {
				continue
			}
			// Split the command line itself we are interested in just the first part
			exe := strings.FieldsFunc(string(parts[0]), func(c rune) bool {
				return unicode.IsSpace(c) || c == ':'
			})
			if len(exe) == 0 {
				continue
			}
			// Check if the name of the executable is what we are looking for
			if re.MatchString(exe[0]) {
				// Grab the PID from the directory path
				pids = append(pids, pid)
			}
		}
	}

	return pids
}

// If running in container, exec command by 'nsenter --mount=/proc/1/ns/mnt ${cmds}'.
// return stdout, exitcode, error
var ExecCmdOnHost = execCmdOnHostFn

func execCmdOnHostFn(cmds []string) ([]byte, int, error) {
	if len(cmds) == 0 {
		return nil, -1, fmt.Errorf("nil command")
	}
	cmdPrefix := []string{}
	if AgentMode == DS_MODE {
		cmdPrefix = append(cmdPrefix, "nsenter", fmt.Sprintf("--mount=%s", path.Join(Conf.ProcRootDir, "/1/ns/mnt")))
	}
	cmdPrefix = append(cmdPrefix, cmds...)

	var errB bytes.Buffer
	command := exec.Command(cmdPrefix[0], cmdPrefix[1:]...)
	command.Stderr = &errB
	if out, err := command.Output(); err != nil {
		return out, command.ProcessState.ExitCode(),
			fmt.Errorf("nsenter command('%s') failed: %v, stderr: %s", strings.Join(cmds, " "), err, errB.String())
	} else {
		return out, 0, nil
	}
}
