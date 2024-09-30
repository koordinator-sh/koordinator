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

package blkio

import (
	"fmt"
	"path"
	"path/filepath"
	"testing"

	"github.com/golang/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/pointer"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	mock_metriccache "github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache/mockmetriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	mock_statesinformer "github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer/mockstatesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	"github.com/koordinator-sh/koordinator/pkg/util/cache"
)

const (
	PodName0 = "pod0"
	PodName1 = "pod1"
	PodName2 = "pod2"
	PVCName  = "test-pvc"
	PVName   = "yoda-87d8625a-dcc9-47bf-a14a-994cf2971193"
	KubePath = "/var/lib/kubelet"
)

func TestBlkIOReconcile_reconcile(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	sysFSRootDirName := BlkIOReconcileName

	testingNodeSLO := newNodeSLO()
	pod0 := newPodWithPVC(PodName0, PVCName)
	pod1 := newPodWithEphemeralVolume(PodName1)
	pod2 := newPodWithEphemeralVolume(PodName2)
	pod0.Status.Phase = corev1.PodRunning
	pod2.Status.Phase = corev1.PodRunning
	pod2.Status.Phase = corev1.PodPending
	testingPodMeta0 := &statesinformer.PodMeta{
		Pod:       pod0,
		CgroupDir: filepath.Join(system.CgroupPathFormatter.QOSDirFn(corev1.PodQOSBestEffort), PodName0),
	}
	testingPodMeta1 := &statesinformer.PodMeta{
		Pod:       pod1,
		CgroupDir: filepath.Join(system.CgroupPathFormatter.QOSDirFn(corev1.PodQOSBestEffort), PodName1),
	}
	testingPodMeta2 := &statesinformer.PodMeta{
		Pod:       pod2,
		CgroupDir: filepath.Join(system.CgroupPathFormatter.QOSDirFn(corev1.PodQOSBestEffort), PodName2),
	}

	diskNumberMap := map[string]string{
		"/dev/vda": "253:0",
		"/dev/vdb": "253:16",
	}
	numberDiskMap := map[string]string{
		"253:0":  "/dev/vda",
		"253:16": "/dev/vdb",
	}
	partitionDiskMap := map[string]string{
		"/dev/vda1": "/dev/vda",
		"/dev/vdb1": "/dev/vdb",
	}
	vgDiskMap := map[string]string{
		"yoda-pool0": "/dev/vdb",
	}
	lvMapperVGMap := map[string]string{
		"/dev/mapper/yoda--pool0-yoda--87d8625a--dcc9--47bf--a14a--994cf2971193": "yoda-pool0",
		"/dev/mapper/yoda--pool0-yoda--test1":                                    "yoda-pool0",
		"/dev/mapper/yoda--pool0-yoda--test2":                                    "yoda-pool0",
	}

	var oldVarLibKubeletRoot string
	helper.SetConf(func(conf *system.Config) {
		oldVarLibKubeletRoot = conf.VarLibKubeletRootDir
		conf.VarLibKubeletRootDir = KubePath
	}, func(conf *system.Config) {
		conf.VarLibKubeletRootDir = oldVarLibKubeletRoot
	})
	mpDiskMap := map[string]string{
		fmt.Sprintf("%s/pods/%s/volumes/kubernetes.io~csi/%s/mount", KubePath, pod0.UID, "yoda-87d8625a-dcc9-47bf-a14a-994cf2971193"): "/dev/mapper/yoda--pool0-yoda--87d8625a--dcc9--47bf--a14a--994cf2971193",
		fmt.Sprintf("%s/pods/%s/volumes/kubernetes.io~csi/html/mount", KubePath, pod1.UID):                                            "/dev/mapper/yoda--pool0-yoda--test1",
		fmt.Sprintf("%s/pods/%s/volumes/kubernetes.io~csi/html/mount", KubePath, pod2.UID):                                            "/dev/mapper/yoda--pool0-yoda--test2",
	}

	localStorageInfo := &metriccache.NodeLocalStorageInfo{}
	localStorageInfo.DiskNumberMap = diskNumberMap
	localStorageInfo.NumberDiskMap = numberDiskMap
	localStorageInfo.PartitionDiskMap = partitionDiskMap
	localStorageInfo.VGDiskMap = vgDiskMap
	localStorageInfo.LVMapperVGMap = lvMapperVGMap
	localStorageInfo.MPDiskMap = mpDiskMap

	t.Run("test not panic", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
		statesInformer.EXPECT().GetAllPods().Return([]*statesinformer.PodMeta{
			testingPodMeta0,
			testingPodMeta1,
			testingPodMeta2}).AnyTimes()
		statesInformer.EXPECT().GetNodeSLO().Return(testingNodeSLO).AnyTimes()
		statesInformer.EXPECT().GetVolumeName("default", PVCName).Return(PVName).AnyTimes()

		mockMetricCache := mock_metriccache.NewMockMetricCache(ctrl)
		mockMetricCache.EXPECT().Get(metriccache.NodeLocalStorageInfoKey).Return(localStorageInfo, true).AnyTimes()

		opt := &framework.Options{
			MetricCache:    mockMetricCache,
			StatesInformer: statesInformer,
			Config:         framework.NewDefaultConfig(),
		}

		helper := system.NewFileTestUtil(t)
		helper.SetAnolisOSResourcesSupported(true)
		system.Conf.CgroupRootDir = path.Join(helper.TempDir, sysFSRootDirName)
		// root class
		rootClassDir := ""
		helper.WriteCgroupFileContents(rootClassDir, system.BlkioIOQoS, "253:16 enable=1 ctrl=user rlat=2000 wlat=2000")
		// be class
		beClassDir := filepath.Join(system.CgroupPathFormatter.ParentDir, system.CgroupPathFormatter.QOSDirFn(corev1.PodQOSBestEffort))
		helper.WriteCgroupFileContents(beClassDir, system.BlkioIOWeight, "253:16 100")
		helper.WriteCgroupFileContents(beClassDir, system.BlkioReadIops, "253:16 2048")
		helper.WriteCgroupFileContents(beClassDir, system.BlkioWriteIops, "253:16 2048")
		helper.WriteCgroupFileContents(beClassDir, system.BlkioReadBps, "253:0 2048")
		helper.WriteCgroupFileContents(beClassDir, system.BlkioWriteBps, "253:0 2048")
		// pod
		pod0Dir := filepath.Join(system.CgroupPathFormatter.ParentDir, testingPodMeta0.CgroupDir)
		pod1Dir := filepath.Join(system.CgroupPathFormatter.ParentDir, testingPodMeta1.CgroupDir)
		pod2Dir := filepath.Join(system.CgroupPathFormatter.ParentDir, testingPodMeta1.CgroupDir)
		helper.WriteCgroupFileContents(pod0Dir, system.BlkioIOWeight, "253:16 100")
		helper.WriteCgroupFileContents(pod0Dir, system.BlkioReadIops, "253:16 2048")
		helper.WriteCgroupFileContents(pod0Dir, system.BlkioWriteIops, "253:16 2048")
		helper.WriteCgroupFileContents(pod0Dir, system.BlkioReadBps, "253:16 10485760")
		helper.WriteCgroupFileContents(pod0Dir, system.BlkioWriteBps, "253:16 10485760")
		helper.WriteCgroupFileContents(pod1Dir, system.BlkioIOWeight, "253:16 100")
		helper.WriteCgroupFileContents(pod1Dir, system.BlkioReadIops, "253:16 2048")
		helper.WriteCgroupFileContents(pod1Dir, system.BlkioWriteIops, "253:16 2048")
		helper.WriteCgroupFileContents(pod1Dir, system.BlkioReadBps, "253:16 10485760")
		helper.WriteCgroupFileContents(pod1Dir, system.BlkioWriteBps, "253:16 10485760")
		helper.WriteCgroupFileContents(pod2Dir, system.BlkioIOWeight, "253:16 100")
		helper.WriteCgroupFileContents(pod2Dir, system.BlkioReadIops, "253:16 2048")
		helper.WriteCgroupFileContents(pod2Dir, system.BlkioWriteIops, "253:16 2048")
		helper.WriteCgroupFileContents(pod2Dir, system.BlkioReadBps, "253:16 10485760")
		helper.WriteCgroupFileContents(pod2Dir, system.BlkioWriteBps, "253:16 10485760")
		defer helper.Cleanup()

		bi := New(opt)
		b := bi.(*blkIOReconcile)
		stop := make(chan struct{})
		defer func() { stop <- struct{}{} }()

		b.executor = &resourceexecutor.ResourceUpdateExecutorImpl{
			Config:        resourceexecutor.NewDefaultConfig(),
			ResourceCache: cache.NewCacheDefault(),
		}

		if err := b.init(stop); err != nil {
			b.executor.Run(stop)
		}
		b.reconcile()
	})
}

func newNodeSLO() *slov1alpha1.NodeSLO {
	return &slov1alpha1.NodeSLO{
		Spec: slov1alpha1.NodeSLOSpec{
			ResourceQOSStrategy: &slov1alpha1.ResourceQOSStrategy{
				// Log will prompt that lsr is not supported
				LSRClass: &slov1alpha1.ResourceQOS{
					BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
						Enable: pointer.Bool(true),
						BlkIOQOS: slov1alpha1.BlkIOQOS{
							Blocks: []*slov1alpha1.BlockCfg{
								{
									Name:      "/dev/vdc",
									BlockType: slov1alpha1.BlockTypeDevice,
									IOCfg: slov1alpha1.IOCfg{
										IOWeightPercent: pointer.Int64(100),
									},
								},
							},
						},
					},
				},
				// Log will prompt that ls is not supported
				LSClass: &slov1alpha1.ResourceQOS{
					BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
						Enable: pointer.Bool(true),
						BlkIOQOS: slov1alpha1.BlkIOQOS{
							Blocks: []*slov1alpha1.BlockCfg{
								{
									Name:      "/dev/vdd",
									BlockType: slov1alpha1.BlockTypeDevice,
									IOCfg: slov1alpha1.IOCfg{
										IOWeightPercent: pointer.Int64(100),
									},
								},
							},
						},
					},
				},
				BEClass: &slov1alpha1.ResourceQOS{
					BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
						Enable: pointer.Bool(true),
						BlkIOQOS: slov1alpha1.BlkIOQOS{
							Blocks: []*slov1alpha1.BlockCfg{
								{
									Name:      "yoda-pool0",
									BlockType: slov1alpha1.BlockTypeVolumeGroup,
									IOCfg: slov1alpha1.IOCfg{
										IOWeightPercent: pointer.Int64(40),
										ReadIOPS:        pointer.Int64(1024),
										WriteIOPS:       pointer.Int64(1024),
										ReadBPS:         pointer.Int64(1048576),
										WriteBPS:        pointer.Int64(1048576),
									},
								},
							},
						},
					},
				},
				CgroupRoot: &slov1alpha1.ResourceQOS{
					BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
						Enable: pointer.Bool(true),
						BlkIOQOS: slov1alpha1.BlkIOQOS{
							Blocks: []*slov1alpha1.BlockCfg{
								{
									Name:      "/dev/vdb",
									BlockType: slov1alpha1.BlockTypeDevice,
									IOCfg: slov1alpha1.IOCfg{
										ReadLatency:         pointer.Int64(1000),
										WriteLatency:        pointer.Int64(1000),
										ReadLatencyPercent:  pointer.Int64(90),
										WriteLatencyPercent: pointer.Int64(90),
										EnableUserModel:     pointer.Bool(true),
										ModelReadBPS:        pointer.Int64(3324911720),
										ModelWriteBPS:       pointer.Int64(2765819289),
										ModelReadSeqIOPS:    pointer.Int64(168274),
										ModelWriteSeqIOPS:   pointer.Int64(367565),
										ModelReadRandIOPS:   pointer.Int64(352545),
										ModelWriteRandIOPS:  pointer.Int64(339390),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func newPodWithPVC(podName string, pvcName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: "default",
			UID:       uuid.NewUUID(),
			Annotations: map[string]string{
				slov1alpha1.AnnotationPodBlkioQoS: `{"blocks":[{"name":"html","type":"podvolume","iocfg":{"readIOPS":1024,"writeIOPS":512}}]}`,
			},
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
			Volumes: []corev1.Volume{
				{
					Name: "html",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
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
	}
}

func newPodWithEphemeralVolume(podName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: "default",
			UID:       uuid.NewUUID(),
			Annotations: map[string]string{
				slov1alpha1.AnnotationPodBlkioQoS: `{"blocks":[{"name":"html","type":"podvolume","iocfg":{"readIOPS":1024,"writeIOPS":512}}]}`,
			},
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
			Volumes: []corev1.Volume{
				{
					Name: "html",
					VolumeSource: corev1.VolumeSource{
						CSI: &corev1.CSIVolumeSource{},
					},
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
	}
}
