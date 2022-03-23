package system

import (
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"k8s.io/klog/v2"
)

var (
	CommonRootDir = "" // for uni-test
)

func CommonFileRead(file string) (string, error) {
	file = path.Join(CommonRootDir, file)
	klog.V(5).Infof("read %s", file)
	data, err := ioutil.ReadFile(file)
	return strings.Trim(string(data), "\n"), err
}

func CommonFileWriteIfDifferent(file string, value string) error {
	currentValue, err := CommonFileRead(file)
	if err != nil {
		return err
	}
	if value == string(currentValue) {
		klog.Infof("resource currentValue equal newValue, skip update resource! file:%s, value %s", file, value)
		return nil
	}
	return CommonFileWrite(file, value)
}

func CommonFileWrite(file string, data string) error {
	file = path.Join(CommonRootDir, file)
	klog.V(5).Infof("write %s [%s]", file, data)
	return ioutil.WriteFile(file, []byte(data), 0644)
}

// ReadFileNoStat uses ioutil.ReadAll to read contents of entire file.
// This is similar to ioutil.ReadFile but without the call to os.Stat, because
// many files in /proc and /sys report incorrect file sizes (either 0 or 4096).
// Reads a max file size of 512kB.  For files larger than this, a scanner
// should be used.
func ReadFileNoStat(filename string) ([]byte, error) {
	const maxBufferSize = 1024 * 512

	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := io.LimitReader(f, maxBufferSize)
	return ioutil.ReadAll(reader)
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

func PathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
