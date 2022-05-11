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

package util

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func Test_readTotalCPUStat(t *testing.T) {
	tempDir := t.TempDir()
	tempInvalidStatPath := filepath.Join(tempDir, "no_stat")
	tempStatPath := filepath.Join(tempDir, "stat")
	statContentStr := "cpu  514003 37519 593580 1706155242 5134 45033 38832 0 0 0\n" +
		"cpu0 9755 845 15540 26635869 3021 2312 9724 0 0 0\n" +
		"cpu1 10075 664 10790 26653871 214 973 1163 0 0 0\n" +
		"intr 574218032 193 0 0 0 4209 0 0 225 131056 131080 130910 130673 130935 130681 130682 130949 131048\n" +
		"ctxt 701110258\n" +
		"btime 1620641098\n" +
		"processes 4488302\n" +
		"procs_running 53\n" +
		"procs_blocked 0\n" +
		"softirq 134422017 2 39835165 107003 28614585 2166152 0 2398085 30750729 0 30550296\n"
	err := ioutil.WriteFile(tempStatPath, []byte(statContentStr), 0666)
	if err != nil {
		t.Error(err)
	}
	type args struct {
		statPath string
	}
	tests := []struct {
		name    string
		args    args
		want    uint64
		wantErr bool
	}{
		{
			name:    "read illegal stat",
			args:    args{statPath: tempInvalidStatPath},
			want:    0,
			wantErr: true,
		},
		{
			name:    "read test cpu stat path",
			args:    args{statPath: tempStatPath},
			want:    1228967,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readTotalCPUStat(tt.args.statPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("readTotalCPUStat wantErr %v but got err %s", tt.wantErr, err)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("readTotalCPUStat want %v but got %v", tt.want, got)
			}
		})
	}
}

func Test_GetCPUStatUsageTicks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Log("Ignore non-Linux environment")
		return
	}
	system.SetConf(*system.NewHostModeConfig())
	cpuStatUsage, err := GetCPUStatUsageTicks()
	if err != nil {
		t.Error("failed to get CPU stat usage: ", err)
	}
	t.Log("get cpu stat usage ticks ", cpuStatUsage)
}

func Test_readPodCPUStatUsageTicks(t *testing.T) {
	tempDir := t.TempDir()
	tempInvalidPodCgroupDir := filepath.Join(tempDir, "no_cgroup")
	tempPodStatPath := filepath.Join(tempDir, system.CpuacctStatFileName)
	err := ioutil.WriteFile(tempPodStatPath, []byte(getStatContents()), 0666)
	assert.NoError(t, err)
	tempInvalidPodCgroupDir1 := filepath.Join(tempDir, "no_cgroup_1")
	err = os.Mkdir(tempInvalidPodCgroupDir1, 0755)
	assert.NoError(t, err)
	tempPodInvalidStatPath := filepath.Join(tempInvalidPodCgroupDir1, system.CpuacctStatFileName)
	err = ioutil.WriteFile(tempPodInvalidStatPath, []byte(getInvalidStatContents()), 0666)
	assert.NoError(t, err)
	type args struct {
		podCgroupDir string
	}
	tests := []struct {
		name    string
		args    args
		want    uint64
		wantErr bool
	}{
		{
			name:    "read illegal cpu stat path",
			args:    args{podCgroupDir: tempInvalidPodCgroupDir},
			want:    0,
			wantErr: true,
		},
		{
			name:    "read test cpu stat path",
			args:    args{podCgroupDir: tempPodStatPath},
			want:    1356232,
			wantErr: false,
		},
		{
			name:    "read invalid cpu stat content",
			args:    args{podCgroupDir: tempPodInvalidStatPath},
			want:    0,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readCPUAcctStatUsageTicks(tt.args.podCgroupDir)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_GetPodCPUStatUsageTicks(t *testing.T) {
	tempDir := t.TempDir()
	system.Conf = system.NewDsModeConfig()
	_, err := GetPodCPUStatUsageTicks(tempDir)
	assert.NotNil(t, err)
}

func getStatContents() string {
	return "user 407232\nnice 60223\nsystem 888777\nidle 3710549851\niowait 7638\nirq 0\nsoftirq 0\n" +
		"steal 1115801\nguest 0\nload average(1min) 5\nload average(5min) 1\nload average(15min) 0\n" +
		"nr_running 0\nnr_uninterrupible 0\nnr_switches 234453363\n"
}

func getInvalidStatContents() string {
	return "user 407232\nnice a\nsystem a\nidle a\niowait a\nirq 0\nsoftirq 0\n" +
		"steal 1115801\nguest 0\nload average(1min) 5\nload average(5min) 1\nload average(15min) 0\n" +
		"nr_running 0\nnr_uninterrupible 0\nnr_switches 234453363\n"
}

func TestGetContainerCPUStatUsageTicks(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	podCgroupDir := "pod-cgroup-dir"
	container := &corev1.ContainerStatus{
		Name:        "test-container",
		ContainerID: "docker://test-container-id",
	}
	containerDir, _ := GetContainerCgroupPathWithKube(podCgroupDir, container)
	helper.WriteCgroupFileContents(containerDir, system.CpuacctStat, getStatContents())
	got, err := GetContainerCPUStatUsageTicks(podCgroupDir, container)
	assert.NoError(t, err)
	assert.Equal(t, uint64(1356232), got)
}
