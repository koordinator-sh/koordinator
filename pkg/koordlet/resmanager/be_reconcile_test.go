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
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/koordinator-sh/koordinator/apis/extension"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

const (
	defaultCFSPeriod  = 100000
	defaultMemUnlimit = 9223372036854771712
)

func writeCgroupForTest(file string, data string) error {
	if _, err := os.Create(file); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_RDWR|syscall.O_NONBLOCK, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_ = f.Sync()
	w := bufio.NewWriter(f)
	_, err = w.WriteString(data)
	_ = w.Flush()
	return err
}

func initTestPodCFS(podMeta *statesinformer.PodMeta, podCurCFS int64, containerCurCFS map[string]int64) error {
	podCFSQuotaPath := util.GetPodCgroupCFSQuotaPath(podMeta.CgroupDir)
	podCFSPeriodPath := util.GetPodCgroupCFSPeriodPath(podMeta.CgroupDir)
	podCFSDir := filepath.Dir(podCFSQuotaPath)
	if err := os.MkdirAll(podCFSDir, 0755); err != nil {
		return err
	}
	if err := writeCgroupForTest(podCFSPeriodPath, strconv.FormatInt(defaultCFSPeriod, 10)); err != nil {
		return err
	}
	if err := writeCgroupForTest(podCFSQuotaPath, strconv.FormatInt(podCurCFS, 10)); err != nil {
		return err
	}
	for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
		if containerCFS, ok := containerCurCFS[containerStat.Name]; ok {
			containerCFSQuotaPath, err := util.GetContainerCgroupCFSQuotaPath(podMeta.CgroupDir, &containerStat)
			if err != nil {
				return err
			}
			containerCFSPeriodPath, err := util.GetContainerCgroupCFSPeriodPath(podMeta.CgroupDir, &containerStat)
			if err != nil {
				return err
			}
			containerCFSDir := filepath.Dir(containerCFSQuotaPath)
			if err := os.MkdirAll(containerCFSDir, 0755); err != nil {
				return err
			}
			err = writeCgroupForTest(containerCFSPeriodPath, strconv.FormatInt(defaultCFSPeriod, 10))
			if err != nil {
				return err
			}
			err = writeCgroupForTest(containerCFSQuotaPath, strconv.FormatInt(containerCFS, 10))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func initTestPodCPUShare(podMeta *statesinformer.PodMeta, podCurCPUShare int64, containerCurCPUShare map[string]int64) error {
	podCPUSharePath := util.GetPodCgroupCPUSharePath(podMeta.CgroupDir)
	podCPUShareDir := filepath.Dir(podCPUSharePath)
	if err := os.MkdirAll(podCPUShareDir, 0755); err != nil {
		return err
	}
	if err := writeCgroupForTest(podCPUSharePath, strconv.FormatInt(podCurCPUShare, 10)); err != nil {
		return err
	}
	for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
		if containerCPUShare, ok := containerCurCPUShare[containerStat.Name]; ok {
			containerCPUSharePath, err := util.GetContainerCgroupCPUSharePath(podMeta.CgroupDir, &containerStat)
			if err != nil {
				return err
			}
			containerCPUShareDir := filepath.Dir(containerCPUSharePath)
			if err := os.MkdirAll(containerCPUShareDir, 0755); err != nil {
				return err
			}
			err = writeCgroupForTest(containerCPUSharePath, strconv.FormatInt(containerCPUShare, 10))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func initTestPodMemLimit(podMeta *statesinformer.PodMeta, podCurMemLimit int64, containerCurMemLimit map[string]int64) error {
	podMemLimitPath := util.GetPodCgroupMemLimitPath(podMeta.CgroupDir)
	podMemLimitDir := filepath.Dir(podMemLimitPath)
	if err := os.MkdirAll(podMemLimitDir, 0755); err != nil {
		return err
	}
	if err := writeCgroupForTest(podMemLimitPath, strconv.FormatInt(podCurMemLimit, 10)); err != nil {
		return err
	}
	for _, containerStat := range podMeta.Pod.Status.ContainerStatuses {
		if containerMemLimit, ok := containerCurMemLimit[containerStat.Name]; ok {
			containerMemLimitPath, err := util.GetContainerCgroupMemLimitPath(podMeta.CgroupDir, &containerStat)
			if err != nil {
				return err
			}
			containerMemLimitDir := filepath.Dir(containerMemLimitPath)
			if err := os.MkdirAll(containerMemLimitDir, 0755); err != nil {
				return err
			}
			err = writeCgroupForTest(containerMemLimitPath, strconv.FormatInt(containerMemLimit, 10))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func Test_reconcileBECPULimit(t *testing.T) {
	type args struct {
		podMeta               *statesinformer.PodMeta
		podCurCFS             int64
		containerCurCFS       map[string]int64
		wantPodCFSQuota       int64
		wantContainerCFSQuota map[string]int64
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "set-be-cpu-limit",
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "test-ns",
							Name:      "test-name",
							UID:       "test-pod-uid",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											extension.BatchCPU: *resource.NewQuantity(500, resource.DecimalSI),
										},
										Requests: corev1.ResourceList{
											extension.BatchCPU: *resource.NewQuantity(500, resource.DecimalSI),
										},
									},
								},
								{
									Name: "test-container-2",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											extension.BatchCPU: *resource.NewQuantity(1000, resource.DecimalSI),
										},
										Requests: corev1.ResourceList{
											extension.BatchCPU: *resource.NewQuantity(1000, resource.DecimalSI),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container-1",
									ContainerID: "docker://testcontainer1hashid",
								},
								{
									Name:        "test-container-2",
									ContainerID: "docker://testcontainer2hashid",
								},
							},
						},
					},
					CgroupDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podtest_pod_uid.slice",
				},
				podCurCFS: -1,
				containerCurCFS: map[string]int64{
					"test-container-1": -1,
					"test-container-2": -1,
				},
				wantPodCFSQuota: 150000,
				wantContainerCFSQuota: map[string]int64{
					"test-container-1": 50000,
					"test-container-2": 100000,
				},
			},
		},
	}
	for _, tt := range tests {
		system.Conf = system.NewDsModeConfig()
		system.Conf.CgroupRootDir = t.TempDir()
		err := initTestPodCFS(tt.args.podMeta, tt.args.podCurCFS, tt.args.containerCurCFS)
		if err != nil {
			t.Errorf("init cfs quota failed, error: %v", err)
		}
		t.Run(tt.name, func(t *testing.T) {
			reconcileBECPULimit(tt.args.podMeta)
			podCFSResult, err := util.GetPodCurCFSQuota(tt.args.podMeta.CgroupDir)
			if err != nil {
				t.Errorf("get pod cfs quota result failed, error %v", err)
			}
			if podCFSResult != tt.args.wantPodCFSQuota {
				t.Errorf("pod cfs quota result not equal, want %v, got %v", tt.args.wantPodCFSQuota, podCFSResult)
			}
			for _, containerStat := range tt.args.podMeta.Pod.Status.ContainerStatuses {
				containerCFSResult, err := util.GetContainerCurCFSQuota(tt.args.podMeta.CgroupDir, &containerStat)
				if err != nil {
					t.Errorf("get container %v cfs quota result failed, error %v", containerStat.Name, err)
				}
				wantContainerCFSquota, exist := tt.args.wantContainerCFSQuota[containerStat.Name]
				if !exist {
					t.Errorf("container %v want cfs quota not exist", containerStat.Name)
				}
				if containerCFSResult != wantContainerCFSquota {
					t.Errorf("container %v cfs quota result not equal, want %v, got %v",
						containerStat.Name, wantContainerCFSquota, containerCFSResult)
				}
			}
		})
	}
}

func Test_reconcileBECPUShare(t *testing.T) {
	type args struct {
		podMeta               *statesinformer.PodMeta
		podCurCPUShare        int64
		containerCurCPUShare  map[string]int64
		wantPodCPUShare       int64
		wantContainerCPUShare map[string]int64
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "set-cpu-share",
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "test-ns",
							Name:      "test-name",
							UID:       "test-pod-uid",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											extension.BatchCPU: *resource.NewQuantity(500, resource.DecimalSI),
										},
										Requests: corev1.ResourceList{
											extension.BatchCPU: *resource.NewQuantity(500, resource.DecimalSI),
										},
									},
								},
								{
									Name: "test-container-2",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											extension.BatchCPU: *resource.NewQuantity(1000, resource.DecimalSI),
										},
										Requests: corev1.ResourceList{
											extension.BatchCPU: *resource.NewQuantity(1000, resource.DecimalSI),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container-1",
									ContainerID: "docker://testcontainer1hashid",
								},
								{
									Name:        "test-container-2",
									ContainerID: "docker://testcontainer2hashid",
								},
							},
						},
					},
					CgroupDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podtest_pod_uid.slice",
				},
				podCurCPUShare: 2,
				containerCurCPUShare: map[string]int64{
					"test-container-1": 2,
					"test-container-2": 2,
				},
				wantPodCPUShare: 1536,
				wantContainerCPUShare: map[string]int64{
					"test-container-1": 512,
					"test-container-2": 1024,
				},
			},
		},
	}
	for _, tt := range tests {
		system.Conf = system.NewDsModeConfig()
		system.Conf.CgroupRootDir = t.TempDir()
		err := initTestPodCPUShare(tt.args.podMeta, tt.args.podCurCPUShare, tt.args.containerCurCPUShare)
		if err != nil {
			t.Errorf("init cpu share failed, error: %v", err)
		}
		t.Run(tt.name, func(t *testing.T) {
			reconcileBECPUShare(tt.args.podMeta)
			podCPUShareResult, err := util.GetPodCurCPUShare(tt.args.podMeta.CgroupDir)
			if err != nil {
				t.Errorf("get pod cpu share result failed, error %v", err)
			}
			if podCPUShareResult != tt.args.wantPodCPUShare {
				t.Errorf("pod cpu share result not equal, want %v, got %v", tt.args.wantPodCPUShare, podCPUShareResult)
			}
			for _, containerStat := range tt.args.podMeta.Pod.Status.ContainerStatuses {
				containerCPUShareResult, err := util.GetContainerCurCPUShare(tt.args.podMeta.CgroupDir, &containerStat)
				if err != nil {
					t.Errorf("get container %v cpu share result failed, error %v", containerStat.Name, err)
				}
				wantContainerCPUShare, exist := tt.args.wantContainerCPUShare[containerStat.Name]
				if !exist {
					t.Errorf("container %v want cpu share quota not exist", containerStat.Name)
				}
				if containerCPUShareResult != wantContainerCPUShare {
					t.Errorf("container %v cpu share result not equal, want %v, got %v",
						containerStat.Name, wantContainerCPUShare, containerCPUShareResult)
				}
			}
		})
	}
}

func Test_reconcileBEMemLimit(t *testing.T) {
	type args struct {
		podMeta               *statesinformer.PodMeta
		podCurMemLimit        int64
		containerCurMemLimit  map[string]int64
		wantPodMemLimit       int64
		wantContainerMemLimit map[string]int64
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "set-mem-limit",
			args: args{
				podMeta: &statesinformer.PodMeta{
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "test-ns",
							Name:      "test-name",
							UID:       "test-pod-uid",
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name: "test-container-1",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											extension.BatchMemory: *resource.NewQuantity(512, resource.BinarySI),
										},
										Requests: corev1.ResourceList{
											extension.BatchMemory: *resource.NewQuantity(512, resource.BinarySI),
										},
									},
								},
								{
									Name: "test-container-2",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											extension.BatchMemory: *resource.NewQuantity(1024, resource.BinarySI),
										},
										Requests: corev1.ResourceList{
											extension.BatchMemory: *resource.NewQuantity(1024, resource.BinarySI),
										},
									},
								},
							},
						},
						Status: corev1.PodStatus{
							ContainerStatuses: []corev1.ContainerStatus{
								{
									Name:        "test-container-1",
									ContainerID: "docker://testcontainer1hashid",
								},
								{
									Name:        "test-container-2",
									ContainerID: "docker://testcontainer2hashid",
								},
							},
						},
					},
					CgroupDir: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podtest_pod_uid.slice",
				},
				podCurMemLimit: defaultMemUnlimit,
				containerCurMemLimit: map[string]int64{
					"test-container-1": defaultMemUnlimit,
					"test-container-2": defaultMemUnlimit,
				},
				wantPodMemLimit: 1536,
				wantContainerMemLimit: map[string]int64{
					"test-container-1": 512,
					"test-container-2": 1024,
				},
			},
		},
	}
	for _, tt := range tests {
		system.Conf = system.NewDsModeConfig()
		system.Conf.CgroupRootDir = t.TempDir()
		err := initTestPodMemLimit(tt.args.podMeta, tt.args.podCurMemLimit, tt.args.containerCurMemLimit)
		if err != nil {
			t.Errorf("init cpu share failed, error: %v", err)
		}
		t.Run(tt.name, func(t *testing.T) {
			reconcileBEMemLimit(tt.args.podMeta)
			podMemLimitResult, err := util.GetPodCurMemLimitBytes(tt.args.podMeta.CgroupDir)
			if err != nil {
				t.Errorf("get pod mem limit result failed, error %v", err)
			}
			if podMemLimitResult != tt.args.wantPodMemLimit {
				t.Errorf("pod mem limit result not equal, want %v, got %v", tt.args.wantPodMemLimit, podMemLimitResult)
			}
			for _, containerStat := range tt.args.podMeta.Pod.Status.ContainerStatuses {
				containerMemLimitResult, err := util.GetContainerCurMemLimitBytes(tt.args.podMeta.CgroupDir, &containerStat)
				if err != nil {
					t.Errorf("get container %v mem limit result failed, error %v", containerStat.Name, err)
				}
				wantContainerMemLimit, exist := tt.args.wantContainerMemLimit[containerStat.Name]
				if !exist {
					t.Errorf("container %v want mem limit not exist", containerStat.Name)
				}
				if containerMemLimitResult != wantContainerMemLimit {
					t.Errorf("container %v mem limit result not equal, want %v, got %v",
						containerStat.Name, wantContainerMemLimit, containerMemLimitResult)
				}
			}
		})
	}
}
