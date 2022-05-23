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

package resmanager

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

func testingPrepareResctrlL3CatPath(t *testing.T, cbmStr, rootSchemataStr string) {
	resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)
	l3CatDir := filepath.Join(resctrlDir, system.RdtInfoDir, system.L3CatDir)
	err := os.MkdirAll(l3CatDir, 0700)
	assert.NoError(t, err)

	cbmPath := filepath.Join(l3CatDir, system.CbmMaskFileName)
	err = ioutil.WriteFile(cbmPath, []byte(cbmStr), 0666)
	assert.NoError(t, err)

	schemataPath := filepath.Join(resctrlDir, system.SchemataFileName)
	err = ioutil.WriteFile(schemataPath, []byte(rootSchemataStr), 0666)
	assert.NoError(t, err)
}

func testingPrepareResctrlL3CatGroups(t *testing.T, cbmStr, rootSchemataStr string) {
	testingPrepareResctrlL3CatPath(t, cbmStr, rootSchemataStr)
	resctrlDir := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)

	beSchemataData := []byte("    L3:0=f;1=f\n    MB:0=100;1=100")
	beSchemataDir := filepath.Join(resctrlDir, BEResctrlGroup)
	err := os.MkdirAll(beSchemataDir, 0700)
	assert.NoError(t, err)
	beSchemataPath := filepath.Join(beSchemataDir, system.SchemataFileName)
	err = ioutil.WriteFile(beSchemataPath, beSchemataData, 0666)
	assert.NoError(t, err)
	beTasksPath := filepath.Join(beSchemataDir, system.ResctrlTaskFileName)
	err = ioutil.WriteFile(beTasksPath, []byte{}, 0666)
	assert.NoError(t, err)

	lsSchemataData := []byte("    L3:0=ff;1=ff\n    MB:0=100;1=100")
	lsSchemataDir := filepath.Join(resctrlDir, LSResctrlGroup)
	err = os.MkdirAll(lsSchemataDir, 0700)
	assert.NoError(t, err)
	lsSchemataPath := filepath.Join(lsSchemataDir, system.SchemataFileName)
	err = ioutil.WriteFile(lsSchemataPath, lsSchemataData, 0666)
	assert.NoError(t, err)
	lsTasksPath := filepath.Join(lsSchemataDir, system.ResctrlTaskFileName)
	err = ioutil.WriteFile(lsTasksPath, []byte{}, 0666)
	assert.NoError(t, err)

	lsrSchemataData := []byte("    L3:0=ff;1=ff\n    MB:0=100;1=100")
	lsrSchemataDir := filepath.Join(resctrlDir, LSRResctrlGroup)
	err = os.MkdirAll(lsrSchemataDir, 0700)
	assert.NoError(t, err)
	lsrSchemataPath := filepath.Join(lsrSchemataDir, system.SchemataFileName)
	err = ioutil.WriteFile(lsrSchemataPath, lsrSchemataData, 0666)
	assert.NoError(t, err)
	lsrTasksPath := filepath.Join(lsrSchemataDir, system.ResctrlTaskFileName)
	err = ioutil.WriteFile(lsrTasksPath, []byte{}, 0666)
	assert.NoError(t, err)
}

func testingPrepareContainerCgroupCPUTasks(t *testing.T, containerParentPath, tasksStr string) {
	containerCgroupDir := filepath.Join(system.Conf.CgroupRootDir, system.CgroupCPUDir, containerParentPath)
	err := os.MkdirAll(containerCgroupDir, 0700)
	assert.NoError(t, err)

	containerTasksPath := filepath.Join(containerCgroupDir, system.CPUTaskFileName)
	err = ioutil.WriteFile(containerTasksPath, []byte(tasksStr), 0666)
	assert.NoError(t, err)
}

func Test_calculateCatL3Schemata(t *testing.T) {
	type args struct {
		cbm          uint
		startPercent int64
		endPercent   int64
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name:    "do not panic but throw an error for empty input",
			want:    "",
			wantErr: true,
		},
		{
			name: "cbm value is invalid",
			args: args{
				cbm:          0x101,
				startPercent: 0,
				endPercent:   100,
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "cbm value is invalid 1",
			args: args{
				cbm:          4,
				startPercent: 0,
				endPercent:   100,
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "percent value is invalid",
			args: args{
				cbm:          0xff,
				startPercent: -10,
				endPercent:   100,
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "percent value is invalid 1",
			args: args{
				cbm:          0xff,
				startPercent: 30,
				endPercent:   30,
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "calculate l3 schemata correctly",
			args: args{
				cbm:          0xff,
				startPercent: 0,
				endPercent:   100,
			},
			want:    "ff",
			wantErr: false,
		},
		{
			name: "calculate l3 schemata correctly 1",
			args: args{
				cbm:          0x3ff,
				startPercent: 10,
				endPercent:   80,
			},
			want:    "fe",
			wantErr: false,
		},
		{
			name: "calculate l3 schemata correctly 2",
			args: args{
				cbm:          0x7ff,
				startPercent: 10,
				endPercent:   50,
			},
			want:    "3c",
			wantErr: false,
		},
		{
			name: "calculate l3 schemata correctly 3",
			args: args{
				cbm:          0x3ff,
				startPercent: 0,
				endPercent:   30,
			},
			want:    "7",
			wantErr: false,
		},
		{
			name: "calculate l3 schemata correctly 4",
			args: args{
				cbm:          0x3ff,
				startPercent: 10,
				endPercent:   85,
			},
			want:    "1fe",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculateCatL3MaskValue(tt.args.cbm, tt.args.startPercent, tt.args.endPercent)
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_initCatResctrl(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)

		sysFSRootDirName := "initCatResctrl"
		helper.MkDirAll(sysFSRootDirName)

		system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)

		testingPrepareResctrlL3CatGroups(t, "ff", "L3:0=ff")

		resctrlDirPath := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)
		_, err := os.Stat(resctrlDirPath)
		assert.NoError(t, err)

		err = initCatResctrl()
		// skip init if resctrl group path exists
		assert.NoError(t, err)

		err = os.RemoveAll(system.Conf.SysFSRootDir)
		assert.NoError(t, err)
		helper.MkDirAll(sysFSRootDirName)

		testingPrepareResctrlL3CatPath(t, "ff", "L3:0=ff")

		// do not panic but create resctrl group if the path does not exist
		err = initCatResctrl()
		assert.NoError(t, err)

		resctrlDirPath = filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir)
		beResctrlGroupPath := filepath.Join(resctrlDirPath, BEResctrlGroup)
		lsResctrlGroupPath := filepath.Join(resctrlDirPath, LSResctrlGroup)
		_, err = os.Stat(beResctrlGroupPath)
		assert.NoError(t, err)
		_, err = os.Stat(lsResctrlGroupPath)
		assert.NoError(t, err)

		// path is invalid, do not panic but log the error
		system.Conf.SysFSRootDir = "invalidPath"
		err = initCatResctrl()
		assert.Error(t, err)
	})
}

func Test_getPodCgroupNewTaskIds(t *testing.T) {
	type args struct {
		podMeta  *statesinformer.PodMeta
		tasksMap map[int]struct{}
	}
	type fields struct {
		containerParentDir string
		containerTasksStr  string
		invalidPath        bool
	}
	tests := []struct {
		name   string
		args   args
		fields fields
		want   []int
	}{
		{
			name: "do nothing for empty pod",
			args: args{
				podMeta: &statesinformer.PodMeta{Pod: &corev1.Pod{}},
			},
			want: nil,
		},
		{
			name: "successfully get task ids for the pod",
			fields: fields{
				containerParentDir: "kubepods.slice/p0/cri-containerd-c0.scope",
				containerTasksStr:  "122450\n122454\n123111\n128912",
			},
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod0",
							UID:  "p0",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "container0",
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "container0",
									ContainerID: "containerd://c0",
								},
							},
						},
					},
					CgroupDir: "p0",
				},
				tasksMap: map[int]struct{}{
					122450: {},
				},
			},
			want: []int{122454, 123111, 128912},
		},
		{
			name: "return empty for invalid path",
			fields: fields{
				containerParentDir: "p0/cri-containerd-c0.scope",
				containerTasksStr:  "122454\n123111\n128912",
				invalidPath:        true,
			},
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod0",
							UID:  "p0",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "container0",
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "container0",
									ContainerID: "containerd://c0",
								},
							},
						},
					},
					CgroupDir: "p0",
				},
				tasksMap: map[int]struct{}{
					122450: {},
				},
			},
			want: nil,
		},
		{
			name: "missing container's status",
			fields: fields{
				containerParentDir: "p0/cri-containerd-c0.scope",
				containerTasksStr:  "122454\n123111\n128912",
				invalidPath:        true,
			},
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name: "pod0",
							UID:  "p0",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "container0",
								},
							},
						},
						Status: corev1.PodStatus{
							Phase:             corev1.PodRunning,
							ContainerStatuses: []corev1.ContainerStatus{},
						},
					},
					CgroupDir: "p0",
				},
				tasksMap: map[int]struct{}{
					122450: {},
				},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system.NewFileTestUtil(t)

			testingPrepareContainerCgroupCPUTasks(t,
				tt.fields.containerParentDir, tt.fields.containerTasksStr)

			system.CommonRootDir = ""
			if tt.fields.invalidPath {
				system.Conf.CgroupRootDir = "invalidPath"
			}

			got := getPodCgroupNewTaskIds(tt.args.podMeta, tt.args.tasksMap)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResctrlReconcile_calculateAndApplyCatL3PolicyForGroup(t *testing.T) {
	type args struct {
		group       string
		cbm         uint
		l3Num       int
		qosStrategy *slov1alpha1.ResourceQoSStrategy
	}
	type field struct {
		invalidPath bool
		noUpdate    bool
	}
	tests := []struct {
		name    string
		field   field
		args    args
		want    string
		wantErr bool
	}{
		{
			name:    "warning for empty input",
			want:    "",
			wantErr: false,
		},
		{
			name: "throw an error for write on invalid path",
			args: args{
				group: LSResctrlGroup,
				cbm:   0xf,
				l3Num: 2,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
							},
						},
					},
				},
			},
			field:   field{invalidPath: true},
			want:    "    L3:0=ff;1=ff\n    MB:0=100;1=100",
			wantErr: true,
		},
		{
			name: "warning to empty policy",
			args: args{
				group: LSResctrlGroup,
				cbm:   0xf,
				l3Num: 2,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					BE: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
							},
						},
					},
				},
			},
			want:    "    L3:0=ff;1=ff\n    MB:0=100;1=100",
			wantErr: false,
		},
		{
			name: "throw an error for calculating schemata failed",
			args: args{
				group: LSResctrlGroup,
				cbm:   0x4,
				l3Num: 2,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
							},
						},
					},
				},
			},
			want:    "    L3:0=ff;1=ff\n    MB:0=100;1=100",
			wantErr: true,
		},
		{
			name: "apply policy correctly",
			args: args{
				group: LSResctrlGroup,
				cbm:   0xf,
				l3Num: 2,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
							},
						},
					},
				},
			},
			want:    "L3:0=f;1=f;\n",
			wantErr: false,
		},
		{
			name: "apply policy correctly 1",
			args: args{
				group: LSResctrlGroup,
				cbm:   0x7ff,
				l3Num: 1,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(10),
								CATRangeEndPercent:   pointer.Int64Ptr(50),
							},
						},
					},
					BE: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
							},
						},
					},
				},
			},
			want:    "L3:0=3c;\n",
			wantErr: false,
		},
		{
			name: "apply policy correctly 2",
			args: args{
				group: LSRResctrlGroup,
				cbm:   0x7ff,
				l3Num: 1,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LSR: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(10),
								CATRangeEndPercent:   pointer.Int64Ptr(50),
							},
						},
					},
					BE: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
							},
						},
					},
				},
			},
			want:    "L3:0=3c;\n",
			wantErr: false,
		},
		{
			name: "calculate the policy but no need to update",
			args: args{
				group: BEResctrlGroup,
				cbm:   0x7ff,
				l3Num: 1,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
							},
						},
					},
					BE: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(10),
								CATRangeEndPercent:   pointer.Int64Ptr(50),
							},
						},
					},
				},
			},
			field:   field{noUpdate: true},
			want:    "L3:0=3c;\n",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helper := system.NewFileTestUtil(t)

			sysFSRootDirName := "calculateAndApplyCatL3PolicyForGroup"
			helper.MkDirAll(sysFSRootDirName)

			system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)
			validSysFSRootDir := system.Conf.SysFSRootDir
			system.CommonRootDir = ""

			testingPrepareResctrlL3CatGroups(t, "ff", "")

			r := ResctrlReconcile{
				executor: NewResourceUpdateExecutor("ResctrlExecutor", 60),
			}
			stop := make(chan struct{})
			r.RunInit(stop)
			defer func() { stop <- struct{}{} }()

			if tt.field.invalidPath {
				system.Conf.SysFSRootDir = "invalidPath"
			}
			if tt.field.noUpdate {
				// prepare fake record
				schemataFilePath := system.GetResctrlSchemataFilePath(tt.args.group)
				updaterKey := schemataFilePath + ":" + L3SchemataPrefix
				fakeResource := NewDetailCommonResourceUpdater(updaterKey, schemataFilePath,
					tt.want, GroupOwnerRef(tt.args.group), updateResctrlSchemataFunc)
				isUpdate := r.executor.UpdateBatchByCache(fakeResource)
				assert.True(t, isUpdate)
			}

			// execute function
			err := r.calculateAndApplyCatL3PolicyForGroup(tt.args.group, tt.args.cbm, tt.args.l3Num,
				getResourceQoSForResctrlGroup(tt.args.qosStrategy, tt.args.group))
			assert.Equal(t, tt.wantErr, err != nil)

			schemataPath := filepath.Join(validSysFSRootDir, system.ResctrlDir, tt.args.group, system.SchemataFileName)
			got, _ := ioutil.ReadFile(schemataPath)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestResctrlReconcile_calculateAndApplyCatMbPolicyForGroup(t *testing.T) {
	type args struct {
		group       string
		l3Num       int
		qosStrategy *slov1alpha1.ResourceQoSStrategy
	}
	type field struct {
		invalidPath bool
		noUpdate    bool
	}
	tests := []struct {
		name    string
		field   field
		args    args
		want    string
		wantErr bool
	}{
		{
			name:    "warning for empty input",
			want:    "",
			wantErr: false,
		},
		{
			name: "throw an error for write on invalid path",
			args: args{
				group: LSResctrlGroup,
				l3Num: 2,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								MBAPercent: pointer.Int64Ptr(90),
							},
						},
					},
				},
			},
			field:   field{invalidPath: true},
			want:    "    L3:0=ff;1=ff\n    MB:0=100;1=100",
			wantErr: true,
		},
		{
			name: "warning to empty policy",
			args: args{
				group: LSResctrlGroup,
				l3Num: 2,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					BE: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								MBAPercent: pointer.Int64Ptr(90),
							},
						},
					},
				},
			},
			want:    "    L3:0=ff;1=ff\n    MB:0=100;1=100",
			wantErr: false,
		},
		{
			name: "apply policy correctly",
			args: args{
				group: LSResctrlGroup,
				l3Num: 2,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								MBAPercent: pointer.Int64Ptr(90),
							},
						},
					},
				},
			},
			want:    "MB:0=90;1=90;\n",
			wantErr: false,
		},
		{
			name: "calculate the policy but no need to update",
			args: args{
				group: BEResctrlGroup,
				l3Num: 2,
				qosStrategy: &slov1alpha1.ResourceQoSStrategy{
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								MBAPercent: pointer.Int64Ptr(100),
							},
						},
					},
					BE: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								MBAPercent: pointer.Int64Ptr(90),
							},
						},
					},
				},
			},
			field:   field{noUpdate: true},
			want:    "MB:0=90;1=90;\n",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			helper := system.NewFileTestUtil(t)

			sysFSRootDirName := "calculateAndApplyCatMbPolicyForGroup"
			helper.MkDirAll(sysFSRootDirName)

			system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)
			validSysFSRootDir := system.Conf.SysFSRootDir
			system.CommonRootDir = ""

			testingPrepareResctrlL3CatGroups(t, "ff", "")
			r := ResctrlReconcile{
				executor: NewResourceUpdateExecutor("ResctrlExecutor", 60),
			}
			stop := make(chan struct{})
			r.RunInit(stop)
			defer func() { stop <- struct{}{} }()

			if tt.field.invalidPath {
				system.Conf.SysFSRootDir = "invalidPath"
			}
			if tt.field.noUpdate {
				// prepare fake record
				schemataFilePath := system.GetResctrlSchemataFilePath(tt.args.group)
				updaterKey := schemataFilePath + ":" + MbSchemataPrefix
				fakeResource := NewDetailCommonResourceUpdater(updaterKey, schemataFilePath,
					tt.want, GroupOwnerRef(tt.args.group), updateResctrlSchemataFunc)
				isUpdate := r.executor.UpdateBatchByCache(fakeResource)
				assert.True(t, isUpdate)
			}

			// execute function
			err := r.calculateAndApplyCatMbPolicyForGroup(tt.args.group, tt.args.l3Num,
				getResourceQoSForResctrlGroup(tt.args.qosStrategy, tt.args.group))
			assert.Equal(t, tt.wantErr, err != nil)

			schemataPath := filepath.Join(validSysFSRootDir, system.ResctrlDir, tt.args.group, system.SchemataFileName)
			got, _ := ioutil.ReadFile(schemataPath)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestResctrlReconcile_calculateAndApplyCatL3GroupTasks(t *testing.T) {
	type args struct {
		group   string
		taskIds []int
	}
	type fields struct {
		invalidPath bool
	}
	tests := []struct {
		name    string
		args    args
		fields  fields
		want    string
		wantErr bool
	}{
		{
			name:    "write nothing",
			args:    args{group: LSResctrlGroup},
			want:    "",
			wantErr: false,
		},
		{
			name: "abort writing for invalid path",
			args: args{group: LSResctrlGroup},
			fields: fields{
				invalidPath: true,
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "write successfully",
			args: args{
				group:   BEResctrlGroup,
				taskIds: []int{0, 1, 2, 5, 7, 9},
			},
			want:    "012579", // the real content of resctrl tasks file would be "0\n\1\n2\n..."
			wantErr: false,
		},
		{
			name: "write successfully 1",
			args: args{
				group:   BEResctrlGroup,
				taskIds: []int{0, 1, 2, 4, 5, 6},
			},
			want:    "012456", // the real content of resctrl tasks file would be "0\n\1\n2\n..."
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			helper := system.NewFileTestUtil(t)

			sysFSRootDirName := "writeCatL3GroupTasks"
			helper.MkDirAll(sysFSRootDirName)

			system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)
			validSysFSRootDir := system.Conf.SysFSRootDir

			testingPrepareResctrlL3CatGroups(t, "", "")

			if tt.fields.invalidPath {
				system.Conf.SysFSRootDir = "invalidPath"
			}
			r := ResctrlReconcile{
				executor: NewResourceUpdateExecutor("ResctrlExecutor", 60),
			}
			stop := make(chan struct{})
			r.RunInit(stop)
			defer func() { stop <- struct{}{} }()

			err := r.calculateAndApplyCatL3GroupTasks(tt.args.group, tt.args.taskIds)
			assert.Equal(t, tt.wantErr, err != nil)

			out, err := ioutil.ReadFile(filepath.Join(validSysFSRootDir, system.ResctrlDir, tt.args.group,
				system.CPUTaskFileName))
			assert.NoError(t, err)
			assert.Equal(t, tt.want, string(out))
		})
	}
}

func TestResctrlReconcile_reconcileCatResctrlPolicy(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)

		sysFSRootDirName := "reconcileCatResctrlPolicy"
		helper.MkDirAll(sysFSRootDirName)

		system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)
		validSysFSRootDir := system.Conf.SysFSRootDir
		system.CommonRootDir = ""

		testingPrepareResctrlL3CatGroups(t, "7ff", "L3:0=7ff;1=7ff\n")

		resctrlDirPath := filepath.Join(validSysFSRootDir, system.ResctrlDir)
		_, err := os.Stat(resctrlDirPath)
		assert.NoError(t, err)

		nodeSLO := &slov1alpha1.NodeSLO{
			Spec: slov1alpha1.NodeSLOSpec{
				ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
					LSR: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
							},
						},
					},
					LS: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(100),
								MBAPercent:           pointer.Int64Ptr(90),
							},
						},
					},
					BE: &slov1alpha1.ResourceQoS{
						ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
							ResctrlQoS: slov1alpha1.ResctrlQoS{
								CATRangeStartPercent: pointer.Int64Ptr(0),
								CATRangeEndPercent:   pointer.Int64Ptr(30),
							},
						},
					},
				},
			},
		}

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		metricCache := mock_metriccache.NewMockMetricCache(ctrl)
		metricCache.EXPECT().GetNodeCPUInfo(&metriccache.QueryParam{}).Return(&metriccache.NodeCPUInfo{
			BasicInfo: util.CPUBasicInfo{CatL3CbmMask: "7ff"},
			TotalInfo: util.CPUTotalInfo{NumberL3s: 2},
		}, nil).Times(3)
		rm := &resmanager{metricCache: metricCache}
		r := ResctrlReconcile{
			resManager: rm,
			executor:   NewResourceUpdateExecutor("ResctrlReconcile", 60),
		}
		stop := make(chan struct{})
		r.RunInit(stop)
		defer func() { stop <- struct{}{} }()

		// reconcile and check if the result is correct
		r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)

		beSchemataPath := filepath.Join(resctrlDirPath, BEResctrlGroup, system.SchemataFileName)
		expectBESchemataStr := "L3:0=f;1=f;\n"
		got, _ := ioutil.ReadFile(beSchemataPath)
		assert.Equal(t, expectBESchemataStr, string(got))

		lsSchemataPath := filepath.Join(resctrlDirPath, LSResctrlGroup, system.SchemataFileName)
		expectLSSchemataStr := "MB:0=90;1=90;\n"
		got, _ = ioutil.ReadFile(lsSchemataPath)
		assert.Equal(t, expectLSSchemataStr, string(got))

		// log error for invalid be resctrl path
		err = os.RemoveAll(filepath.Join(resctrlDirPath, BEResctrlGroup))
		assert.NoError(t, err)
		r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)

		// log error for invalid root resctrl path
		system.Conf.SysFSRootDir = "invalidPath"
		r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)
		system.Conf.SysFSRootDir = validSysFSRootDir

		// log error for invalid l3 number
		metricCache.EXPECT().GetNodeCPUInfo(&metriccache.QueryParam{}).Return(&metriccache.NodeCPUInfo{
			BasicInfo: util.CPUBasicInfo{CatL3CbmMask: "7ff"},
			TotalInfo: util.CPUTotalInfo{NumberL3s: -1},
		}, nil).Times(1)
		r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)

		// log error for invalid l3 cbm
		metricCache.EXPECT().GetNodeCPUInfo(&metriccache.QueryParam{}).Return(&metriccache.NodeCPUInfo{
			BasicInfo: util.CPUBasicInfo{CatL3CbmMask: "invalid"},
			TotalInfo: util.CPUTotalInfo{NumberL3s: 2},
		}, nil).Times(1)
		r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)
		metricCache.EXPECT().GetNodeCPUInfo(&metriccache.QueryParam{}).Return(&metriccache.NodeCPUInfo{
			BasicInfo: util.CPUBasicInfo{CatL3CbmMask: ""},
			TotalInfo: util.CPUTotalInfo{NumberL3s: 2},
		}, nil).Times(1)
		r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)

		// log error for invalid nodeCPUInfo
		metricCache.EXPECT().GetNodeCPUInfo(&metriccache.QueryParam{}).Return(nil, nil)
		r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)

		// log error for get nodeCPUInfo failed
		metricCache.EXPECT().GetNodeCPUInfo(&metriccache.QueryParam{}).Return(nil, fmt.Errorf("error"))
		r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)
	})
}

func TestResctrlReconcile_reconcileResctrlGroups(t *testing.T) {
	// preparing
	wantResctrlTaskStr := "122450122454123111128912"
	testingContainerParentDir := "kubepods.slice/p0/cri-containerd-c0.scope"
	testingContainerTasksStr := "122450\n122454\n123111\n128912"
	testingBEResctrlTasksStr := "122450"
	testingPodMeta := &statesinformer.PodMeta{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod0",
				UID:  "p0",
				Labels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSBE),
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "container0",
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:        "container0",
						ContainerID: "containerd://c0",
					},
				},
			},
		},
		CgroupDir: "p0",
	}
	testQOSStrategy := util.DefaultResourceQoSStrategy()
	testQOSStrategy.BE.ResctrlQoS.Enable = pointer.BoolPtr(true)

	t.Run("test", func(t *testing.T) {
		// initialization
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
		rm := &resmanager{statesInformer: statesInformer}
		r := ResctrlReconcile{
			resManager: rm,
			executor:   NewResourceUpdateExecutor("ResctrlReconcile", 30),
		}
		stop := make(chan struct{})
		r.RunInit(stop)
		defer func() { stop <- struct{}{} }()

		statesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{testingPodMeta}).MaxTimes(2)

		helper := system.NewFileTestUtil(t)

		sysFSRootDirName := "reconcileResctrlGroups"
		helper.MkDirAll(sysFSRootDirName)

		system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)

		testingPrepareResctrlL3CatGroups(t, "", "")
		testingPrepareContainerCgroupCPUTasks(t, testingContainerParentDir, testingContainerTasksStr)

		// run reconcileResctrlGroups for BE tasks not exist
		r.reconcileResctrlGroups(testQOSStrategy)

		// check if the reconciliation is a success
		out, err := ioutil.ReadFile(filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir, BEResctrlGroup,
			system.CPUTaskFileName))
		assert.NoError(t, err)
		assert.Equal(t, wantResctrlTaskStr, string(out))

		beTasksPath := filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir, BEResctrlGroup, system.ResctrlTaskFileName)
		err = ioutil.WriteFile(beTasksPath, []byte(testingBEResctrlTasksStr), 0666)
		assert.NoError(t, err)

		// run reconcileResctrlGroups
		r.reconcileResctrlGroups(testQOSStrategy)

		// check if the reconciliation is a success
		out, err = ioutil.ReadFile(filepath.Join(system.Conf.SysFSRootDir, system.ResctrlDir, BEResctrlGroup,
			system.CPUTaskFileName))
		assert.NoError(t, err)
		assert.Equal(t, wantResctrlTaskStr, string(out))
	})
}

func TestResctrlReconcile_reconcile(t *testing.T) {
	// preparing
	testingContainerParentDir := "kubepods.slice/p0/cri-containerd-c0.scope"
	testingContainerTasksStr := "122450\n122454\n123111\n128912"

	testingNodeSLO := &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceQoSStrategy: &slov1alpha1.ResourceQoSStrategy{
				LSR: &slov1alpha1.ResourceQoS{
					ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
						ResctrlQoS: slov1alpha1.ResctrlQoS{
							CATRangeStartPercent: pointer.Int64Ptr(0),
							CATRangeEndPercent:   pointer.Int64Ptr(100),
						},
					},
				},
				LS: &slov1alpha1.ResourceQoS{
					ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
						ResctrlQoS: slov1alpha1.ResctrlQoS{
							CATRangeStartPercent: pointer.Int64Ptr(0),
							CATRangeEndPercent:   pointer.Int64Ptr(100),
						},
					},
				},
				BE: &slov1alpha1.ResourceQoS{
					ResctrlQoS: &slov1alpha1.ResctrlQoSCfg{
						ResctrlQoS: slov1alpha1.ResctrlQoS{
							CATRangeStartPercent: pointer.Int64Ptr(0),
							CATRangeEndPercent:   pointer.Int64Ptr(30),
						},
					},
				},
			},
		},
	}
	testingPodMeta := &statesinformer.PodMeta{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod0",
				UID:  "p0",
				Labels: map[string]string{
					extension.LabelPodQoS: string(extension.QoSBE),
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "container0",
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:        "container0",
						ContainerID: "containerd://c0",
					},
				},
			},
		},
		CgroupDir: "p0",
	}
	testingNodeCPUInfo := &metriccache.NodeCPUInfo{
		BasicInfo: util.CPUBasicInfo{CatL3CbmMask: "7ff"},
		TotalInfo: util.CPUTotalInfo{NumberL3s: 2},
	}

	t.Run("test not panic", func(t *testing.T) {

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
		metricCache := mock_metriccache.NewMockMetricCache(ctrl)
		statesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{testingPodMeta}).AnyTimes()
		statesInformer.EXPECT().GetNodeSLO().Return(testingNodeSLO).AnyTimes()
		metricCache.EXPECT().GetNodeCPUInfo(&metriccache.QueryParam{}).Return(testingNodeCPUInfo, nil).AnyTimes()
		rm := &resmanager{
			statesInformer: statesInformer,
			metricCache:    metricCache,
			config:         NewDefaultConfig(),
		}

		helper := system.NewFileTestUtil(t)

		sysFSRootDirName := "ResctrlReconcile"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)
		validSysFSRootDir := system.Conf.SysFSRootDir
		system.CommonRootDir = ""

		testingPrepareContainerCgroupCPUTasks(t, testingContainerParentDir, testingContainerTasksStr)
		testingPrepareResctrlL3CatGroups(t, "", "")

		r := NewResctrlReconcile(rm)
		stop := make(chan struct{})
		r.RunInit(stop)
		defer func() { stop <- struct{}{} }()

		cpuInfoContents := "flags		: fpu vme de pse cat_l3 mba"
		helper.WriteProcSubFileContents("cpuinfo", cpuInfoContents)

		r.reconcile()

		// test nil resmgr
		r.resManager = nil
		r.reconcile()
		r.resManager = rm

		// test init cat resctrl failed
		system.Conf.SysFSRootDir = "invalidPath"
		r.reconcile()
		system.Conf.SysFSRootDir = validSysFSRootDir

		r.reconcile()

		// test strategy parse error
		testingNodeSLO.Spec.ResourceQoSStrategy = nil
		statesInformer.EXPECT().GetNodeSLO().Return(testingNodeSLO).AnyTimes()
		r.reconcile()

	})
}

func Test_calculateMbaPercentForGroup(t *testing.T) {

	type args struct {
		group     string
		mbPercent *int64
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "mbPercent not config",
			args: args{
				group: "BE",
			},
			want: "",
		},
		{
			name: "mbPercent value is invalid,not between (0,100]",
			args: args{
				group:     "BE",
				mbPercent: pointer.Int64Ptr(0),
			},
			want: "",
		},
		{
			name: "mbPercent value is invalid,not between (0,100]",
			args: args{
				group:     "BE",
				mbPercent: pointer.Int64Ptr(101),
			},
			want: "",
		},
		{
			name: "mbPercent value is invalid, not multiple of 10",
			args: args{
				group:     "BE",
				mbPercent: pointer.Int64Ptr(85),
			},
			want: "90",
		},
		{
			name: "mbPercent value is valid",
			args: args{
				group:     "BE",
				mbPercent: pointer.Int64Ptr(80),
			},
			want: "80",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateMbaPercentForGroup(tt.args.group, tt.args.mbPercent)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_calculateL3SchemataResource(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)

		sysFSRootDirName := "reconcileCatResctrlPolicy"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)

		testingPrepareResctrlL3CatGroups(t, "7ff", "    L3:0=ff;1=ff\n    MB:0=100;1=100")
		updater := calculateL3SchemataResource(BEResctrlGroup, "3c", 2)
		assert.Equal(t, updater.Value(), "L3:0=3c;1=3c;\n")

	})
}

func Test_calculateMbSchemataResource(t *testing.T) {
	t.Run("test", func(t *testing.T) {
		helper := system.NewFileTestUtil(t)

		sysFSRootDirName := "reconcileCatResctrlPolicy"
		helper.MkDirAll(sysFSRootDirName)
		system.Conf.SysFSRootDir = path.Join(helper.TempDir, sysFSRootDirName)

		testingPrepareResctrlL3CatGroups(t, "7ff", "    L3:0=ff;1=ff\n    MB:0=100;1=100")
		updater := calculateMbSchemataResource(BEResctrlGroup, "90", 2)
		assert.Equal(t, updater.Value(), "MB:0=90;1=90;\n")

	})
}
