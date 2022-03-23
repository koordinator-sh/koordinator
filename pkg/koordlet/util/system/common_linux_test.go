//go:build linux
// +build linux

package system

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ProcCmdLine(t *testing.T) {
	t.Run("testing process cmdline args should match", func(t *testing.T) {
		cmdline, err := ProcCmdLine("/proc", os.Getpid())
		assert.Empty(t, err)
		assert.ElementsMatch(t, cmdline, os.Args)
	})
	t.Run("fake process should fail", func(t *testing.T) {
		procRoot, _ := ioutil.TempDir("", "proc")
		fakePid := 42
		fakeProcDir := filepath.Join(procRoot, strconv.Itoa((fakePid)))
		os.MkdirAll(fakeProcDir, 0555)
		defer os.RemoveAll(procRoot)

		_, err := ProcCmdLine(procRoot, fakePid)
		assert.NotEmpty(t, err)
	})
}

func Test_PidOf(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skipf("not supported on GOOS=%s", runtime.GOOS)
	}
	t.Run("testing process pid should match", func(t *testing.T) {
		pids, err := PidOf("/proc", filepath.Base(os.Args[0]))
		assert.Empty(t, err)
		assert.NotZero(t, pids)
		assert.Contains(t, pids, os.Getpid())
	})
	t.Run("empty process name should failed", func(t *testing.T) {
		_, err := PidOf("/proc", "")
		assert.Error(t, err)
	})
}

func Test_ExecCmdOnHost(t *testing.T) {
	type args struct {
		agentMode   string
		procDir     string
		fileContent string
	}
	tests := []struct {
		name    string
		args    args
		wantOut string
		wantErr bool
	}{
		{
			name: "agent in DS_MODE",
			args: args{
				agentMode:   DS_MODE,
				procDir:     "/proc",
				fileContent: "bar",
			},
			wantOut: "bar",
			wantErr: false,
		},
		{
			name: "agent in HOST_MODE",
			args: args{
				agentMode:   HOST_MODE,
				procDir:     "/proc",
				fileContent: "bar",
			},
			wantOut: "bar",
			wantErr: false,
		},
		{
			name: "wrong proc root dir",
			args: args{
				agentMode:   DS_MODE,
				procDir:     "/fake-proc",
				fileContent: "bar",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := NewFileTestUtil(t)
			AgentMode = tt.args.agentMode
			Conf.ProcRootDir = tt.args.procDir
			defer helper.Cleanup()

			helper.WriteFileContents("foo", tt.args.fileContent)
			output, _, err := ExecCmdOnHost([]string{"cat", path.Join(helper.TempDir, "foo")})
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.wantOut, string(output))
		})
	}
}
