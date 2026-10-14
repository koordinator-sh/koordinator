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
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"

	"github.com/stretchr/testify/assert"

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
		statesInformer.EXPECT().HasSynced().Return(true).AnyTimes()

		mockMetricCache := mock_metriccache.NewMockMetricCache(ctrl)
		mockMetricCache.EXPECT().Get(metriccache.NodeLocalStorageInfoKey).Return(localStorageInfo, true).AnyTimes()

		opt := &framework.Options{
			MetricCache:    mockMetricCache,
			StatesInformer: statesInformer,
			Config:         framework.NewDefaultConfig(),
		}

		helper := system.NewFileTestUtil(t)
		helper.SetAnolisOSResourcesSupported(true)
		system.Conf.CgroupRootDir = filepath.Join(helper.TempDir, sysFSRootDirName)
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
						Enable: ptr.To[bool](true),
						BlkIOQOS: slov1alpha1.BlkIOQOS{
							Blocks: []*slov1alpha1.BlockCfg{
								{
									Name:      "/dev/vdc",
									BlockType: slov1alpha1.BlockTypeDevice,
									IOCfg: slov1alpha1.IOCfg{
										IOWeightPercent: ptr.To[int64](100),
									},
								},
							},
						},
					},
				},
				// Log will prompt that ls is not supported
				LSClass: &slov1alpha1.ResourceQOS{
					BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
						Enable: ptr.To[bool](true),
						BlkIOQOS: slov1alpha1.BlkIOQOS{
							Blocks: []*slov1alpha1.BlockCfg{
								{
									Name:      "/dev/vdd",
									BlockType: slov1alpha1.BlockTypeDevice,
									IOCfg: slov1alpha1.IOCfg{
										IOWeightPercent: ptr.To[int64](100),
									},
								},
							},
						},
					},
				},
				BEClass: &slov1alpha1.ResourceQOS{
					BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
						Enable: ptr.To[bool](true),
						BlkIOQOS: slov1alpha1.BlkIOQOS{
							Blocks: []*slov1alpha1.BlockCfg{
								{
									Name:      "yoda-pool0",
									BlockType: slov1alpha1.BlockTypeVolumeGroup,
									IOCfg: slov1alpha1.IOCfg{
										IOWeightPercent: ptr.To[int64](40),
										ReadIOPS:        ptr.To[int64](1024),
										WriteIOPS:       ptr.To[int64](1024),
										ReadBPS:         ptr.To[int64](1048576),
										WriteBPS:        ptr.To[int64](1048576),
									},
								},
							},
						},
					},
				},
				CgroupRoot: &slov1alpha1.ResourceQOS{
					BlkIOQOS: &slov1alpha1.BlkIOQOSCfg{
						Enable: ptr.To[bool](true),
						BlkIOQOS: slov1alpha1.BlkIOQOS{
							Blocks: []*slov1alpha1.BlockCfg{
								{
									Name:      "/dev/vdb",
									BlockType: slov1alpha1.BlockTypeDevice,
									IOCfg: slov1alpha1.IOCfg{
										ReadLatency:         ptr.To[int64](1000),
										WriteLatency:        ptr.To[int64](1000),
										ReadLatencyPercent:  ptr.To[int64](90),
										WriteLatencyPercent: ptr.To[int64](90),
										EnableUserModel:     ptr.To[bool](true),
										ModelReadBPS:        ptr.To[int64](3324911720),
										ModelWriteBPS:       ptr.To[int64](2765819289),
										ModelReadSeqIOPS:    ptr.To[int64](168274),
										ModelWriteSeqIOPS:   ptr.To[int64](367565),
										ModelReadRandIOPS:   ptr.To[int64](352545),
										ModelWriteRandIOPS:  ptr.To[int64](339390),
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

func assertBlkIOUpdater(t *testing.T, updater resourceexecutor.ResourceUpdater, resourceType system.ResourceType, value string) {
	t.Helper()
	assert.Equal(t, resourceType, updater.ResourceType())
	assert.Equal(t, value, updater.Value())
}

func newLocalStorageInfo() *metriccache.NodeLocalStorageInfo {
	return &metriccache.NodeLocalStorageInfo{
		DiskNumberMap: map[string]string{
			"/dev/vda": "253:0",
			"/dev/vdb": "253:16",
		},
		NumberDiskMap: map[string]string{
			"253:0":  "/dev/vda",
			"253:16": "/dev/vdb",
		},
		PartitionDiskMap: map[string]string{
			"/dev/vda1": "/dev/vda",
			"/dev/vdb1": "/dev/vdb",
		},
		VGDiskMap: map[string]string{
			"yoda-pool0": "/dev/vdb",
		},
		LVMapperVGMap: map[string]string{
			"/dev/mapper/yoda--pool0-yoda--test1": "yoda-pool0",
		},
		MPDiskMap: map[string]string{
			"/var/lib/kubelet/mount-disk":      "/dev/vda",
			"/var/lib/kubelet/mount-partition": "/dev/vda1",
			"/var/lib/kubelet/mount-lv":        "/dev/mapper/yoda--pool0-yoda--test1",
		},
	}
}

func TestGetBlkIOUpdaterFromBlockCfg(t *testing.T) {
	diskNumber := "253:16"
	dynamicPath := "kubepods.slice/kubepods-burstable.slice"

	t.Run("default values", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{
			Name:      "/dev/vdb",
			BlockType: slov1alpha1.BlockTypeDevice,
		}
		got := getBlkIOUpdaterFromBlockCfg(block, diskNumber, dynamicPath)
		assert.Len(t, got, 5)
		assertBlkIOUpdater(t, got[0], system.BlkioTRIopsName, "253:16 0")
		assertBlkIOUpdater(t, got[1], system.BlkioTRBpsName, "253:16 0")
		assertBlkIOUpdater(t, got[2], system.BlkioTWIopsName, "253:16 0")
		assertBlkIOUpdater(t, got[3], system.BlkioTWBpsName, "253:16 0")
		assertBlkIOUpdater(t, got[4], system.BlkioIOWeightName, "253:16 100")
	})

	t.Run("custom values", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{
			Name:      "/dev/vdb",
			BlockType: slov1alpha1.BlockTypeDevice,
			IOCfg: slov1alpha1.IOCfg{
				ReadIOPS:        ptr.To[int64](1024),
				WriteIOPS:       ptr.To[int64](512),
				ReadBPS:         ptr.To[int64](1048576),
				WriteBPS:        ptr.To[int64](524288),
				IOWeightPercent: ptr.To[int64](40),
			},
		}
		got := getBlkIOUpdaterFromBlockCfg(block, diskNumber, dynamicPath)
		assert.Len(t, got, 5)
		assertBlkIOUpdater(t, got[0], system.BlkioTRIopsName, "253:16 1024")
		assertBlkIOUpdater(t, got[1], system.BlkioTRBpsName, "253:16 1048576")
		assertBlkIOUpdater(t, got[2], system.BlkioTWIopsName, "253:16 512")
		assertBlkIOUpdater(t, got[3], system.BlkioTWBpsName, "253:16 524288")
		assertBlkIOUpdater(t, got[4], system.BlkioIOWeightName, "253:16 40")
	})
}

func TestGetBlkIORemoverFromDiskNumber(t *testing.T) {
	diskNumber := "253:16"
	dynamicPath := "kubepods.slice/kubepods-burstable.slice"

	got := getBlkIORemoverFromDiskNumber(diskNumber, dynamicPath)
	assert.Len(t, got, 5)
	assertBlkIOUpdater(t, got[0], system.BlkioTRIopsName, "253:16 0")
	assertBlkIOUpdater(t, got[1], system.BlkioTRBpsName, "253:16 0")
	assertBlkIOUpdater(t, got[2], system.BlkioTWIopsName, "253:16 0")
	assertBlkIOUpdater(t, got[3], system.BlkioTWBpsName, "253:16 0")
	assertBlkIOUpdater(t, got[4], system.BlkioIOWeightName, "253:16 100")
}

func TestGetDiskConfigUpdaterFromBlockCfg(t *testing.T) {
	diskNumber := "253:16"

	t.Run("default values", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{
			Name:      "/dev/vdb",
			BlockType: slov1alpha1.BlockTypeDevice,
		}
		got := getDiskConfigUpdaterFromBlockCfg(block, diskNumber, "")
		assert.Len(t, got, 2)
		assertBlkIOUpdater(t, got[0], system.BlkioIOQoSName, "253:16 enable=1 ctrl=user rpct=95 rlat=3000 wpct=95 wlat=3000")
		assertBlkIOUpdater(t, got[1], system.BlkioIOModelName, "253:16 ctrl=auto")
	})

	t.Run("custom latency and percent", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{
			Name:      "/dev/vdb",
			BlockType: slov1alpha1.BlockTypeDevice,
			IOCfg: slov1alpha1.IOCfg{
				ReadLatency:         ptr.To[int64](1000),
				WriteLatency:        ptr.To[int64](2000),
				ReadLatencyPercent:  ptr.To[int64](90),
				WriteLatencyPercent: ptr.To[int64](80),
			},
		}
		got := getDiskConfigUpdaterFromBlockCfg(block, diskNumber, "")
		assert.Len(t, got, 2)
		assertBlkIOUpdater(t, got[0], system.BlkioIOQoSName, "253:16 enable=1 ctrl=user rpct=90 rlat=1000 wpct=80 wlat=2000")
		assertBlkIOUpdater(t, got[1], system.BlkioIOModelName, "253:16 ctrl=auto")
	})

	t.Run("enable user model", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{
			Name:      "/dev/vdb",
			BlockType: slov1alpha1.BlockTypeDevice,
			IOCfg: slov1alpha1.IOCfg{
				EnableUserModel:    ptr.To[bool](true),
				ModelReadBPS:       ptr.To[int64](1000),
				ModelWriteBPS:      ptr.To[int64](2000),
				ModelReadSeqIOPS:   ptr.To[int64](3000),
				ModelWriteSeqIOPS:  ptr.To[int64](4000),
				ModelReadRandIOPS:  ptr.To[int64](5000),
				ModelWriteRandIOPS: ptr.To[int64](6000),
			},
		}
		got := getDiskConfigUpdaterFromBlockCfg(block, diskNumber, "")
		assert.Len(t, got, 2)
		assertBlkIOUpdater(t, got[0], system.BlkioIOQoSName, "253:16 enable=1 ctrl=user rpct=95 rlat=3000 wpct=95 wlat=3000")
		assertBlkIOUpdater(t, got[1], system.BlkioIOModelName, "253:16 ctrl=user rbps=1000 rseqiops=3000 rrandiops=5000 wbps=2000 wseqiops=4000 wrandiops=6000")
	})
}

func TestGetDiskConfigRemoverFromDiskNumber(t *testing.T) {
	diskNumber := "253:16"

	got := getDiskConfigRemoverFromDiskNumber(diskNumber, "")
	assert.Len(t, got, 1)
	assertBlkIOUpdater(t, got[0], system.BlkioIOQoSName, "253:16 enable=0")
}

func TestParseBlkIOResult(t *testing.T) {
	t.Run("valid json", func(t *testing.T) {
		blockIOQos, err := parseBlkIOResult(`{"blocks":[{"name":"html","type":"podvolume","ioCfg":{"readIOPS":1024,"writeIOPS":512}}]}`)
		assert.NoError(t, err)
		assert.NotNil(t, blockIOQos)
		assert.Len(t, blockIOQos.Blocks, 1)
		assert.Equal(t, "html", blockIOQos.Blocks[0].Name)
		assert.Equal(t, slov1alpha1.BlockTypePodVolume, blockIOQos.Blocks[0].BlockType)
		if assert.NotNil(t, blockIOQos.Blocks[0].IOCfg.ReadIOPS) {
			assert.Equal(t, int64(1024), *blockIOQos.Blocks[0].IOCfg.ReadIOPS)
		}
		if assert.NotNil(t, blockIOQos.Blocks[0].IOCfg.WriteIOPS) {
			assert.Equal(t, int64(512), *blockIOQos.Blocks[0].IOCfg.WriteIOPS)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		blockIOQos, err := parseBlkIOResult(`{"blocks":`)
		assert.Error(t, err)
		assert.Nil(t, blockIOQos)
	})
}

func TestGetDiskNumbersFromCgroupFile(t *testing.T) {
	t.Run("parse valid file", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "blkio.file")
		assert.NoError(t, os.WriteFile(filePath, []byte("253:0 100\n253:16 2048\ntotal 0\n"), 0600))
		diskNumbers, err := getDiskNumbersFromCgroupFile(filePath)
		assert.NoError(t, err)
		assert.Equal(t, []string{"253:0", "253:16"}, diskNumbers)
	})

	t.Run("invalid line content", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "blkio.file")
		assert.NoError(t, os.WriteFile(filePath, []byte("253:16\n"), 0600))
		_, err := getDiskNumbersFromCgroupFile(filePath)
		assert.Error(t, err)
	})

	t.Run("file not exist", func(t *testing.T) {
		_, err := getDiskNumbersFromCgroupFile(filepath.Join(t.TempDir(), "not-exist"))
		assert.Error(t, err)
	})
}

func TestGetBlkIORecorder(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, system.BlkioTRIopsName), []byte("253:0 100\n253:16 2048\n"), 0600))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, system.BlkioTRBpsName), []byte("253:0 1048576\n"), 0600))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, system.BlkioTWIopsName), []byte("253:16 512\n"), 0600))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, system.BlkioTWBpsName), []byte("253:16 512\n"), 0600))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, system.BlkioIOWeightName), []byte("253:16 100\n"), 0600))

	got, err := getBlkIORecorder(dir)
	assert.NoError(t, err)
	assert.Equal(t, map[string]bool{"253:0": true, "253:16": true}, got)
}

func TestGetDiskConfigRecorder(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, system.BlkioIOQoSName), []byte("253:16 enable=1 ctrl=user rpct=95 rlat=3000 wpct=95 wlat=3000\n"), 0600))

	got, err := getDiskConfigRecorder(dir)
	assert.NoError(t, err)
	assert.Equal(t, map[string]bool{"253:16": true}, got)
}

func TestGetDiskRecorderWithError(t *testing.T) {
	_, err := getDiskRecorder(t.TempDir(), []string{system.BlkioTRIopsName})
	assert.Error(t, err)
}

func TestGetDiskNumber(t *testing.T) {
	storageInfo := newLocalStorageInfo()

	assert.Equal(t, "253:0", getDiskNumber(storageInfo, "/dev/vda"))
	assert.Equal(t, "", getDiskNumber(storageInfo, "/dev/unknown"))
	assert.Equal(t, "", getDiskNumber(nil, "/dev/vda"))
}

func TestGetDiskByDevice(t *testing.T) {
	storageInfo := newLocalStorageInfo()

	assert.Equal(t, "/dev/vda", getDiskByDevice(storageInfo, "/dev/vda"))
	assert.Equal(t, "/dev/vda", getDiskByDevice(storageInfo, "/dev/vda1"))
	assert.Equal(t, "", getDiskByDevice(storageInfo, "/dev/unknown"))
	assert.Equal(t, "", getDiskByDevice(nil, "/dev/vda"))
}

func TestGetDiskByVG(t *testing.T) {
	storageInfo := newLocalStorageInfo()

	assert.Equal(t, "/dev/vdb", getDiskByVG(storageInfo, "yoda-pool0"))
	assert.Equal(t, "", getDiskByVG(storageInfo, "unknown-pool"))
	assert.Equal(t, "", getDiskByVG(nil, "yoda-pool0"))
}

func TestGetDiskByMountPoint(t *testing.T) {
	storageInfo := newLocalStorageInfo()

	t.Run("mount point of a disk", func(t *testing.T) {
		assert.Equal(t, "/dev/vda", getDiskByMountPoint(storageInfo, "/var/lib/kubelet/mount-disk"))
	})
	t.Run("mount point of a partition", func(t *testing.T) {
		assert.Equal(t, "/dev/vda", getDiskByMountPoint(storageInfo, "/var/lib/kubelet/mount-partition"))
	})
	t.Run("mount point of a logical volume", func(t *testing.T) {
		assert.Equal(t, "/dev/vdb", getDiskByMountPoint(storageInfo, "/var/lib/kubelet/mount-lv"))
	})
	t.Run("mount point not found", func(t *testing.T) {
		assert.Equal(t, "", getDiskByMountPoint(storageInfo, "/var/lib/kubelet/unknown"))
		assert.Equal(t, "", getDiskByMountPoint(nil, "/var/lib/kubelet/mount-disk"))
	})
}

func TestIsDeviceDisk(t *testing.T) {
	storageInfo := newLocalStorageInfo()

	assert.True(t, isDeviceDisk(storageInfo, "/dev/vda"))
	assert.False(t, isDeviceDisk(storageInfo, "/dev/vda1"))
	assert.False(t, isDeviceDisk(storageInfo, "/dev/unknown"))
	assert.False(t, isDeviceDisk(nil, "/dev/vda"))
}

func TestBlkIOReconcile_GetDiskNumberFromDevice(t *testing.T) {
	b := &blkIOReconcile{storageInfo: newLocalStorageInfo()}

	t.Run("device disk", func(t *testing.T) {
		got, err := b.getDiskNumberFromDevice("/dev/vdb")
		assert.NoError(t, err)
		assert.Equal(t, "253:16", got)
	})
	t.Run("partition of a disk", func(t *testing.T) {
		got, err := b.getDiskNumberFromDevice("/dev/vdb1")
		assert.NoError(t, err)
		assert.Equal(t, "253:16", got)
	})
	t.Run("unknown device", func(t *testing.T) {
		_, err := b.getDiskNumberFromDevice("/dev/unknown")
		assert.Error(t, err)
	})
}

func TestBlkIOReconcile_GetDiskNumberFromVolumeGroup(t *testing.T) {
	b := &blkIOReconcile{storageInfo: newLocalStorageInfo()}

	t.Run("volume group found", func(t *testing.T) {
		got, err := b.getDiskNumberFromVolumeGroup("yoda-pool0")
		assert.NoError(t, err)
		assert.Equal(t, "253:16", got)
	})
	t.Run("volume group not found", func(t *testing.T) {
		_, err := b.getDiskNumberFromVolumeGroup("unknown-pool")
		assert.Error(t, err)
	})
}

func TestBlkIOReconcile_GetDiskNumberFromPodVolume(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	oldVarLibKubeletRoot := system.Conf.VarLibKubeletRootDir
	helper.SetConf(func(conf *system.Config) {
		conf.VarLibKubeletRootDir = KubePath
	}, func(conf *system.Config) {
		conf.VarLibKubeletRootDir = oldVarLibKubeletRoot
	})

	podUUID := uuid.NewUUID()
	podMeta := &statesinformer.PodMeta{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{UID: podUUID},
		},
	}
	storageInfo := newLocalStorageInfo()
	storageInfo.MPDiskMap[fmt.Sprintf("%s/pods/%s/volumes/kubernetes.io~csi/pv-test/mount", KubePath, podUUID)] = "/dev/mapper/yoda--pool0-yoda--test1"
	b := &blkIOReconcile{storageInfo: storageInfo}

	t.Run("volume mounted", func(t *testing.T) {
		got, err := b.getDiskNumberFromPodVolume(podMeta, "pv-test")
		assert.NoError(t, err)
		assert.Equal(t, "253:16", got)
	})
	t.Run("volume not mounted", func(t *testing.T) {
		_, err := b.getDiskNumberFromPodVolume(podMeta, "unknown-volume")
		assert.Error(t, err)
	})
}

func TestBlkIOReconcile_GetDiskNumberFromBlockCfg(t *testing.T) {
	helper := system.NewFileTestUtil(t)
	defer helper.Cleanup()
	oldVarLibKubeletRoot := system.Conf.VarLibKubeletRootDir
	helper.SetConf(func(conf *system.Config) {
		conf.VarLibKubeletRootDir = KubePath
	}, func(conf *system.Config) {
		conf.VarLibKubeletRootDir = oldVarLibKubeletRoot
	})

	b := &blkIOReconcile{storageInfo: newLocalStorageInfo()}

	t.Run("device", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{Name: "/dev/vdb", BlockType: slov1alpha1.BlockTypeDevice}
		got, err := b.getDiskNumberFromBlockCfg(block, nil)
		assert.NoError(t, err)
		assert.Equal(t, "253:16", got)
	})
	t.Run("unknown device", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{Name: "/dev/unknown", BlockType: slov1alpha1.BlockTypeDevice}
		_, err := b.getDiskNumberFromBlockCfg(block, nil)
		assert.Error(t, err)
	})
	t.Run("volume group", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{Name: "yoda-pool0", BlockType: slov1alpha1.BlockTypeVolumeGroup}
		got, err := b.getDiskNumberFromBlockCfg(block, nil)
		assert.NoError(t, err)
		assert.Equal(t, "253:16", got)
	})
	t.Run("unknown volume group", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{Name: "unknown-pool", BlockType: slov1alpha1.BlockTypeVolumeGroup}
		_, err := b.getDiskNumberFromBlockCfg(block, nil)
		assert.Error(t, err)
	})
	t.Run("pod volume with nil pod meta", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{Name: "html", BlockType: slov1alpha1.BlockTypePodVolume}
		_, err := b.getDiskNumberFromBlockCfg(block, nil)
		assert.Error(t, err)
	})
	t.Run("pod volume with pvc", func(t *testing.T) {
		podUUID := uuid.NewUUID()
		storageInfo := newLocalStorageInfo()
		storageInfo.MPDiskMap[fmt.Sprintf("%s/pods/%s/volumes/kubernetes.io~csi/pv-test/mount", KubePath, podUUID)] = "/dev/mapper/yoda--pool0-yoda--test1"

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		statesInformer := mock_statesinformer.NewMockStatesInformer(ctrl)
		statesInformer.EXPECT().GetVolumeName("default", "test-pvc").Return("pv-test").AnyTimes()
		b := &blkIOReconcile{
			statesInformer: statesInformer,
			storageInfo:    storageInfo,
		}
		podMeta := &statesinformer.PodMeta{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					UID:       podUUID,
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "html",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "test-pvc",
								},
							},
						},
					},
				},
			},
		}

		got, err := b.getDiskNumberFromBlockCfg(&slov1alpha1.BlockCfg{Name: "html", BlockType: slov1alpha1.BlockTypePodVolume}, podMeta)
		assert.NoError(t, err)
		assert.Equal(t, "253:16", got)
	})
	t.Run("pod volume with csi", func(t *testing.T) {
		podUUID := uuid.NewUUID()
		storageInfo := newLocalStorageInfo()
		storageInfo.MPDiskMap[fmt.Sprintf("%s/pods/%s/volumes/kubernetes.io~csi/html/mount", KubePath, podUUID)] = "/dev/mapper/yoda--pool0-yoda--test1"
		b := &blkIOReconcile{storageInfo: storageInfo}
		podMeta := &statesinformer.PodMeta{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{UID: podUUID},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "html",
							VolumeSource: corev1.VolumeSource{
								CSI: &corev1.CSIVolumeSource{},
							},
						},
					},
				},
			},
		}

		got, err := b.getDiskNumberFromBlockCfg(&slov1alpha1.BlockCfg{Name: "html", BlockType: slov1alpha1.BlockTypePodVolume}, podMeta)
		assert.NoError(t, err)
		assert.Equal(t, "253:16", got)
	})
	t.Run("pod volume without matching volume", func(t *testing.T) {
		podMeta := &statesinformer.PodMeta{
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", UID: uuid.NewUUID()},
			},
		}
		_, err := b.getDiskNumberFromBlockCfg(&slov1alpha1.BlockCfg{Name: "not-exist", BlockType: slov1alpha1.BlockTypePodVolume}, podMeta)
		assert.Error(t, err)
	})
	t.Run("unsupported block type", func(t *testing.T) {
		block := &slov1alpha1.BlockCfg{Name: "html", BlockType: "unknown-type"}
		_, err := b.getDiskNumberFromBlockCfg(block, nil)
		assert.Error(t, err)
	})
}
