//go:build linux
// +build linux

package system

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_GuessCgroupDriverFromCgroupName(t *testing.T) {
	tests := []struct {
		name     string
		envSetup func(cgroupRoot string)
		want     CgroupDriverType
	}{
		{
			name: "'kubepods' and 'kubepods.slice' both exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods"), 0755)
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods.slice"), 0755)
			},
			want: "",
		},
		{
			name:     "neither 'kubepods' nor 'kubepods.slice' exists",
			envSetup: func(cgroupRoot string) {},
			want:     "",
		},
		{
			name: "'kubepods.slice' exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods.slice"), 0755)
			},
			want: Systemd,
		},
		{
			name: "'kubepods' exist",
			envSetup: func(cgroupRoot string) {
				os.MkdirAll(filepath.Join(cgroupRoot, "cpu", "kubepods"), 0755)
			},
			want: Cgroupfs,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpCgroupRoot, _ := ioutil.TempDir("", "cgroup")
			os.MkdirAll(tmpCgroupRoot, 0555)
			defer os.RemoveAll(tmpCgroupRoot)

			Conf = &Config{
				CgroupRootDir: tmpCgroupRoot,
			}

			tt.envSetup(tmpCgroupRoot)
			got := GuessCgroupDriverFromCgroupName()
			assert.Equal(t, tt.want, got)
		})
	}
}
