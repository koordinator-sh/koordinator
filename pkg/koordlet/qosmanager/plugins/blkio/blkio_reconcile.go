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
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/audit"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
)

const (
	BlkIOReconcileName = "BlkioReconcile"

	DefaultReadIOPS           = 0
	DefaultWriteIOPS          = 0
	DefaultReadBPS            = 0
	DefaultWriteBPS           = 0
	DefaultIOWeightPercentage = 100
	DefaultIOLatency          = 3000
	DefaultLatencyPercent     = 95
)

var _ framework.QOSStrategy = &blkIOReconcile{}

type blkIOReconcile struct {
	reconcileInterval time.Duration
	statesInformer    statesinformer.StatesInformer
	metricCache       metriccache.MetricCache
	executor          resourceexecutor.ResourceUpdateExecutor
	storageInfo       *metriccache.NodeLocalStorageInfo
}

func (b *blkIOReconcile) Enabled() bool {
	return features.DefaultKoordletFeatureGate.Enabled(features.BlkIOReconcile) && b.reconcileInterval > 0
}

func (b *blkIOReconcile) Setup(context *framework.Context) {
}

func (b *blkIOReconcile) Run(stopCh <-chan struct{}) {
	if err := b.init(stopCh); err != nil {
		klog.Fatal("blkIOReconcile init failed, error %v", err)
		return
	}
	go wait.Until(b.reconcile, b.reconcileInterval, stopCh)
}

type (
	GetUpdaterFunc      func(block *slov1alpha1.BlockCfg, diskNumber string, dynamicPath string) (resources []resourceexecutor.ResourceUpdater)
	GetRemoverFunc      func(diskNumber string, dynamicPath string) (resources []resourceexecutor.ResourceUpdater)
	GetDiskRecorderFunc func(absolutePath string) (map[string]bool, error)
)

func New(opt *framework.Options) framework.QOSStrategy {
	return &blkIOReconcile{
		reconcileInterval: time.Duration(opt.Config.ReconcileIntervalSeconds) * time.Second,
		statesInformer:    opt.StatesInformer,
		metricCache:       opt.MetricCache,
		executor:          resourceexecutor.NewResourceUpdateExecutor(),
	}
}

func (b *blkIOReconcile) init(stopCh <-chan struct{}) error {
	b.executor.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh) {
		return fmt.Errorf("%s: timed out waiting for pvc caches to sync", BlkIOReconcileName)
	}
	return nil
}

func (b *blkIOReconcile) reconcile() {
	klog.V(4).Infof("%s: start to reconcile", BlkIOReconcileName)
	// get node local storage info
	storageInfoRaw, exist := b.metricCache.Get(metriccache.NodeLocalStorageInfoKey)
	if !exist {
		klog.Errorf("%s: fail to get node local storage info not exist", BlkIOReconcileName)
		return
	}
	storageInfo, ok := storageInfoRaw.(*metriccache.NodeLocalStorageInfo)
	if !ok {
		klog.Fatalf("type error, expect %T， but got %T", metriccache.NodeLocalStorageInfo{}, storageInfoRaw)
	}
	b.storageInfo = storageInfo
	// get nodeslo
	nodeSLO := b.statesInformer.GetNodeSLO()
	if nodeSLO == nil || nodeSLO.Spec.ResourceQOSStrategy == nil {
		klog.Errorf("%s: nodeSLO or resourceQOSStrategy is nil, skip reconcile blkio!", BlkIOReconcileName)
		return
	}

	// update node blk qos by strategy defined in nodeslo
	strategy := nodeSLO.Spec.ResourceQOSStrategy
	// lsr
	if strategy.LSRClass != nil && strategy.LSRClass.BlkIOQOS != nil && *strategy.LSRClass.BlkIOQOS.Enable && len(strategy.LSRClass.BlkIOQOS.Blocks) != 0 {
		klog.Warningf("%s: configuring blkio of LSRClass is not supported!", BlkIOReconcileName)
	}
	// ls
	if strategy.LSClass != nil && strategy.LSClass.BlkIOQOS != nil && *strategy.LSClass.BlkIOQOS.Enable && len(strategy.LSClass.BlkIOQOS.Blocks) != 0 {
		klog.Warningf("%s: configuring blkio of LSClass is not supported!", BlkIOReconcileName)
	}
	// be
	if strategy.BEClass != nil && strategy.BEClass.BlkIOQOS != nil {
		klog.V(4).Infof("%s: start to reconcile be class blkio config", BlkIOReconcileName)
		blocks := []*slov1alpha1.BlockCfg{}
		if *strategy.BEClass.BlkIOQOS.Enable {
			blocks = strategy.BEClass.BlkIOQOS.Blocks
		}
		beClassRelativeDir := util.GetPodQoSRelativePath(corev1.PodQOSBestEffort)
		beClassPath := util.GetPodCgroupBlkIOAbsoluteDir(corev1.PodQOSBestEffort)
		err := b.updateBlkIOConfig(
			blocks,
			nil,
			blkioUpdater{
				absolutePath:    beClassPath,
				getDiskRecorder: getBlkIORecorder,
				dynamicPath:     beClassRelativeDir,
				getUpdaterFunc:  getBlkIOUpdaterFromBlockCfg,
				getRemoverFunc:  getBlkIORemoverFromDiskNumber,
			},
		)
		if err != nil {
			klog.Errorf("%s: fail to update be class blkio config: %s", BlkIOReconcileName, err.Error())
		} else {
			klog.V(4).Infof("%s: reconcile be class blkio config finished", BlkIOReconcileName)
		}
	}
	// root
	if strategy.CgroupRoot != nil && strategy.CgroupRoot.BlkIOQOS != nil {
		klog.V(4).Infof("%s: start to reconcile root class blkio config", BlkIOReconcileName)
		blocks := []*slov1alpha1.BlockCfg{}
		if *strategy.CgroupRoot.BlkIOQOS.Enable {
			blocks = strategy.CgroupRoot.BlkIOQOS.Blocks
		}
		rootClassRelativePath := ""
		rootClassPath := util.GetCgroupRootBlkIOAbsoluteDir()
		err := b.updateBlkIOConfig(
			blocks,
			nil,
			blkioUpdater{
				absolutePath:    rootClassPath,
				getDiskRecorder: getDiskConfigRecorder,
				dynamicPath:     rootClassRelativePath,
				getUpdaterFunc:  getDiskConfigUpdaterFromBlockCfg,
				getRemoverFunc:  getDiskConfigRemoverFromDiskNumber,
			},
		)
		if err != nil {
			klog.Errorf("%s: fail to update root class blkio config: %s", BlkIOReconcileName, err.Error())
		} else {
			klog.V(4).Infof("%s: reconcile root class blkio config finished", BlkIOReconcileName)
		}
	}

	// pods
	podsMeta := b.statesInformer.GetAllPods()
	for _, podMeta := range podsMeta {
		if extension.GetPodQoSClassRaw(podMeta.Pod) == extension.QoSNone {
			// ignore unknown qos pods
			continue
		}
		if podMeta.Pod.Status.Phase != corev1.PodRunning {
			klog.Warningf("%s: pod %s/%s is not in running status, ignoring...", BlkIOReconcileName, podMeta.Pod.Namespace, podMeta.Pod.Name)
			continue
		}
		var err error
		podBlkIOQoS := &slov1alpha1.BlkIOQOS{}
		if podVolumeResult, exist := podMeta.Pod.Annotations[slov1alpha1.AnnotationPodBlkioQoS]; exist {
			if podVolumeResult != "" {
				if podBlkIOQoS, err = parseBlkIOResult(podVolumeResult); err != nil {
					klog.Errorf("%s: unmarshal pod annotation %v failed, error %v", BlkIOReconcileName, slov1alpha1.AnnotationPodBlkioQoS, err)
					continue
				}
			}
		} else {
			continue
		}
		klog.V(4).Infof("%s: start to reconcile pod %s/%s blkio config", BlkIOReconcileName, podMeta.Pod.Namespace, podMeta.Pod.Name)
		err = b.updateBlkIOConfig(
			podBlkIOQoS.Blocks,
			podMeta,
			blkioUpdater{
				absolutePath:    util.GetPodCgroupBlkIOAbsolutePath(podMeta.CgroupDir),
				dynamicPath:     podMeta.CgroupDir,
				getDiskRecorder: getBlkIORecorder,
				getUpdaterFunc:  getBlkIOUpdaterFromBlockCfg,
				getRemoverFunc:  getBlkIORemoverFromDiskNumber,
			},
		)
		if err != nil {
			klog.Errorf("%s: fail to update pod %s/%s blkio config: %s", BlkIOReconcileName, podMeta.Pod.Namespace, podMeta.Pod.Name, err.Error())
		} else {
			klog.V(4).Infof("%s: reconcile pod %s/%s blkio config finished", BlkIOReconcileName, podMeta.Pod.Namespace, podMeta.Pod.Name)
		}
	}
}

type blkioUpdater struct {
	dynamicPath  string
	absolutePath string

	getDiskRecorder GetDiskRecorderFunc
	getUpdaterFunc  GetUpdaterFunc
	getRemoverFunc  GetRemoverFunc
}

// update blkio cgroup files
// podMeta == nil when BlockType is BlockTypeDevice or BlockTypeVolumeGroup
// podMeta != nil when BlockType is BlockTypePodVolume
func (b *blkIOReconcile) updateBlkIOConfig(blocks []*slov1alpha1.BlockCfg, podMeta *statesinformer.PodMeta, blkioUpdater blkioUpdater) error {
	if blkioUpdater.getDiskRecorder == nil {
		return fmt.Errorf("getDiskRecorder can not be nil")
	}
	if blkioUpdater.getUpdaterFunc == nil || blkioUpdater.getRemoverFunc == nil {
		return fmt.Errorf("getUpdaterFunc or getRemoverFunc can not be nil")
	}
	var resources []resourceexecutor.ResourceUpdater
	diskConfigRecorder, err := blkioUpdater.getDiskRecorder(blkioUpdater.absolutePath)
	if err != nil {
		return fmt.Errorf("fail to get disk config recorder: %s", err.Error())
	}
	for _, block := range blocks {
		diskNumber, err := b.getDiskNumberFromBlockCfg(block, podMeta)
		if err != nil {
			return fmt.Errorf("fail to get disk number from block %v: %s", block, err.Error())
		}
		diskConfigRecorder[diskNumber] = false
		resources = append(resources, blkioUpdater.getUpdaterFunc(block, diskNumber, blkioUpdater.dynamicPath)...)
	}
	for diskNumber, needRemove := range diskConfigRecorder {
		if needRemove {
			resources = append(resources, blkioUpdater.getRemoverFunc(diskNumber, blkioUpdater.dynamicPath)...)
		}
	}
	b.executor.UpdateBatch(true, resources...)
	return nil
}

// deviceName: /dev/sdb
// diskNumber: 253:16
func (b *blkIOReconcile) getDiskNumberFromDevice(deviceName string) (string, error) {
	disk := getDiskByDevice(b.storageInfo, deviceName)
	number := getDiskNumber(b.storageInfo, disk)
	if number == "" {
		return "", fmt.Errorf("%s: fail to get device number of device %s", BlkIOReconcileName, deviceName)
	}
	return number, nil
}

// vgName: yoda-pool
// diskNumber: 253:16
func (b *blkIOReconcile) getDiskNumberFromVolumeGroup(vgName string) (string, error) {
	disk := getDiskByVG(b.storageInfo, vgName)
	number := getDiskNumber(b.storageInfo, disk)
	if number == "" {
		return "", fmt.Errorf("%s: fail to get device number of vg %s", BlkIOReconcileName, vgName)
	}
	return number, nil
}

// volumeName is volume name of pod
// diskNumber: 253:16
func (b *blkIOReconcile) getDiskNumberFromPodVolume(podMeta *statesinformer.PodMeta, volumeName string) (string, error) {
	podUUID := podMeta.Pod.UID
	mountpoint := filepath.Join(system.Conf.VarLibKubeletRootDir, "pods", string(podUUID), "volumes/kubernetes.io~csi", volumeName, "mount")
	disk := getDiskByMountPoint(b.storageInfo, mountpoint)
	diskNumber := getDiskNumber(b.storageInfo, disk)
	if diskNumber == "" {
		return "", fmt.Errorf("can not get diskNumber by mountpoint %s", mountpoint)
	}

	return diskNumber, nil
}

// dynamicPath for be: kubepods.slice/kubepods-burstable.slice/
// dynamicPath for pod: kubepods.slice/kubepods-burstable.slice/kubepods-pod7712555c_ce62_454a_9e18_9ff0217b8941.slice/
func getBlkIOUpdaterFromBlockCfg(block *slov1alpha1.BlockCfg, diskNumber string, dynamicPath string) (resources []resourceexecutor.ResourceUpdater) {
	var readIOPS, writeIOPS, readBPS, writeBPS, ioweight int64 = DefaultReadIOPS, DefaultWriteIOPS, DefaultReadBPS, DefaultWriteBPS, DefaultIOWeightPercentage
	// iops
	if value := block.IOCfg.ReadIOPS; value != nil {
		readIOPS = *value
	}
	if value := block.IOCfg.WriteIOPS; value != nil {
		writeIOPS = *value
	}
	// bps
	if value := block.IOCfg.ReadBPS; value != nil {
		readBPS = *value
	}
	if value := block.IOCfg.WriteBPS; value != nil {
		writeBPS = *value
	}
	// io weight
	if weight := block.IOCfg.IOWeightPercent; weight != nil {
		ioweight = *weight

	}

	readIOPSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioTRIopsName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, readIOPS),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioTRIopsName, fmt.Sprintf("%s %d", diskNumber, readIOPS)),
	)
	readBPSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioTRBpsName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, readBPS),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioTRBpsName, fmt.Sprintf("%s %d", diskNumber, readBPS)),
	)
	writeIOPSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioTWIopsName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, writeIOPS),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioTWIopsName, fmt.Sprintf("%s %d", diskNumber, writeIOPS)),
	)
	writeBPSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioTWBpsName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, writeBPS),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioTWBpsName, fmt.Sprintf("%s %d", diskNumber, writeBPS)),
	)
	ioWeightUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioIOWeightName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, ioweight),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioIOWeightName, fmt.Sprintf("%s %d", diskNumber, ioweight)),
	)

	resources = append(resources,
		readIOPSUpdater,
		readBPSUpdater,
		writeIOPSUpdater,
		writeBPSUpdater,
		ioWeightUpdater,
	)

	return
}

func (b *blkIOReconcile) getDiskNumberFromBlockCfg(block *slov1alpha1.BlockCfg, podMeta *statesinformer.PodMeta) (string, error) {
	var diskNumber string
	var err error
	switch block.BlockType {
	case slov1alpha1.BlockTypeDevice:
		if diskNumber, err = b.getDiskNumberFromDevice(block.Name); err != nil {
			return "", err
		}
	case slov1alpha1.BlockTypeVolumeGroup:
		if diskNumber, err = b.getDiskNumberFromVolumeGroup(block.Name); err != nil {
			return "", err
		}
	case slov1alpha1.BlockTypePodVolume:
		if podMeta == nil {
			return "", fmt.Errorf("pod meta is nil")
		}
		for _, volume := range podMeta.Pod.Spec.Volumes {
			if volume.Name == block.Name {
				// check if kind of volume is pvc or csi ephemeral volume
				if volume.PersistentVolumeClaim != nil {
					volumeName := b.statesInformer.GetVolumeName(podMeta.Pod.Namespace, volume.PersistentVolumeClaim.ClaimName)
					// /var/lib/kubelet/pods/[pod uuid]/volumes/kubernetes.io~csi/[pv name]/mount
					diskNumber, err = b.getDiskNumberFromPodVolume(podMeta, volumeName)
					if err != nil {
						return "", fmt.Errorf("fail to get disk number from pod %s/%s volume %s: %s", podMeta.Pod.Namespace, podMeta.Pod.Name, volumeName, err.Error())
					}
				}
				if volume.CSI != nil {
					// /var/lib/kubelet/pods/[pod uuid]/volumes/kubernetes.io~csi/[pod ephemeral volume name]/mount
					diskNumber, err = b.getDiskNumberFromPodVolume(podMeta, volume.Name)
					if err != nil {
						return "", fmt.Errorf("fail to get disk number from pod %s/%s volume %s: %s", podMeta.Pod.Namespace, podMeta.Pod.Name, volume.Name, err.Error())
					}
				}
			}
		}
		if diskNumber == "" {
			return "", fmt.Errorf("can not get diskNumber by pod %s/%s volume %s", podMeta.Pod.Namespace, podMeta.Pod.Name, block.Name)
		}
	default:
		return "", fmt.Errorf("block type %s is not supported", block.BlockType)
	}
	return diskNumber, nil
}

// configure cgroup root
// dynamicPath for root: ""
func getDiskConfigUpdaterFromBlockCfg(block *slov1alpha1.BlockCfg, diskNumber string, dynamicPath string) (resources []resourceexecutor.ResourceUpdater) {
	var (
		readlat, writelat                                                                                       int64 = DefaultIOLatency, DefaultIOLatency
		readlatPercent, writelatPercent                                                                         int64 = DefaultLatencyPercent, DefaultLatencyPercent
		enableUserModel                                                                                         bool  = false
		modelReadBPS, modelWriteBPS, modelReadSeqIOPS, modelWriteSeqIOPS, modelReadRandIOPS, modelWriteRandIOPS int64 = 0, 0, 0, 0, 0, 0
	)
	// disk io weight latency
	if value := block.IOCfg.ReadLatency; value != nil {
		readlat = *value
	}
	if value := block.IOCfg.WriteLatency; value != nil {
		writelat = *value
	}

	// disk io latency percent
	if value := block.IOCfg.ReadLatencyPercent; value != nil {
		readlatPercent = *value
	}
	if value := block.IOCfg.WriteLatencyPercent; value != nil {
		writelatPercent = *value
	}

	// user cost model configuration
	if value := block.IOCfg.EnableUserModel; value != nil {
		enableUserModel = *value
	}
	if enableUserModel {
		if value := block.IOCfg.ModelReadBPS; value != nil {
			modelReadBPS = *value
		}
		if value := block.IOCfg.ModelWriteBPS; value != nil {
			modelWriteBPS = *value
		}
		if value := block.IOCfg.ModelReadSeqIOPS; value != nil {
			modelReadSeqIOPS = *value
		}
		if value := block.IOCfg.ModelWriteSeqIOPS; value != nil {
			modelWriteSeqIOPS = *value
		}
		if value := block.IOCfg.ModelReadRandIOPS; value != nil {
			modelReadRandIOPS = *value
		}
		if value := block.IOCfg.ModelWriteRandIOPS; value != nil {
			modelWriteRandIOPS = *value
		}
	}

	ioQoSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioIOQoSName,
		dynamicPath,
		fmt.Sprintf("%s enable=1 ctrl=user rpct=%d rlat=%d wpct=%d wlat=%d", diskNumber, readlatPercent, readlat, writelatPercent, writelat),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioIOQoSName, fmt.Sprintf("%s enable=1 ctrl=user rpct=%d rlat=%d wpct=%d wlat=%d", diskNumber, readlatPercent, readlat, writelatPercent, writelat)),
	)

	resources = append(resources, ioQoSUpdater)

	if enableUserModel {
		ioModelUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
			system.BlkioIOModelName,
			dynamicPath,
			fmt.Sprintf("%s ctrl=user rbps=%d rseqiops=%d rrandiops=%d wbps=%d wseqiops=%d wrandiops=%d", diskNumber, modelReadBPS, modelReadSeqIOPS, modelReadRandIOPS, modelWriteBPS, modelWriteSeqIOPS, modelWriteRandIOPS),
			audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioIOModelName, fmt.Sprintf("%s ctrl=user rbps=%d rseqiops=%d rrandiops=%d wbps=%d wseqiops=%d wrandiops=%d", diskNumber, modelReadBPS, modelReadSeqIOPS, modelReadRandIOPS, modelWriteBPS, modelWriteSeqIOPS, modelWriteRandIOPS)),
		)

		resources = append(resources, ioModelUpdater)
	} else {
		ioModelUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
			system.BlkioIOModelName,
			dynamicPath,
			fmt.Sprintf("%s ctrl=auto", diskNumber),
			audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioIOModelName, fmt.Sprintf("%s ctrl=auto", diskNumber)),
		)

		resources = append(resources, ioModelUpdater)
	}

	return
}

func parseBlkIOResult(blkioResult string) (*slov1alpha1.BlkIOQOS, error) {
	podBlkIOQoS := &slov1alpha1.BlkIOQOS{}
	if err := json.Unmarshal([]byte(blkioResult), podBlkIOQoS); err != nil {
		return nil, fmt.Errorf("failed to parse BlkIO result %s: %s", blkioResult, err.Error())
	}
	return podBlkIOQoS, nil
}

// key of recorder is disk number
// value of recorder means whether to remove cgroup config of this disk
func getDiskRecorder(parentDir string, fileNames []string) (map[string]bool, error) {
	recorder := make(map[string]bool)
	for _, fileName := range fileNames {
		diskNumbers, err := getDiskNumbersFromCgroupFile(filepath.Join(parentDir, fileName))
		if err != nil {
			return nil, err
		}
		for _, number := range diskNumbers {
			recorder[number] = true
		}
	}
	return recorder, nil
}

func getDiskNumbersFromCgroupFile(filePath string) ([]string, error) {
	diskNumbers := []string{}
	numberReg := regexp.MustCompile("^[0-9]+:[0-9]+")
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	fileScanner := bufio.NewScanner(file)
	for fileScanner.Scan() {
		output := strings.Split(fileScanner.Text(), " ")
		if len(output) < 2 {
			return nil, fmt.Errorf("content of %s is not correct", filePath)
		}
		if numberReg.MatchString(output[0]) {
			diskNumbers = append(diskNumbers, output[0])
		}
	}
	return diskNumbers, nil
}

func getBlkIORecorder(path string) (map[string]bool, error) {
	fileNames := []string{
		system.BlkioTRIopsName,
		system.BlkioTRBpsName,
		system.BlkioTWIopsName,
		system.BlkioTWBpsName,
		system.BlkioIOWeightName,
	}
	recorder, err := getDiskRecorder(path, fileNames)
	if err != nil {
		return nil, err
	}
	return recorder, nil
}

func getDiskConfigRecorder(path string) (map[string]bool, error) {
	fileNames := []string{
		system.BlkioIOQoSName,
	}
	recorder, err := getDiskRecorder(path, fileNames)
	if err != nil {
		return nil, err
	}
	return recorder, nil
}

func getBlkIORemoverFromDiskNumber(diskNumber string, dynamicPath string) (resources []resourceexecutor.ResourceUpdater) {

	readIOPSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioTRIopsName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, DefaultReadIOPS),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioTRIopsName, fmt.Sprintf("%s %d", diskNumber, DefaultReadIOPS)),
	)
	readBPSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioTRBpsName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, DefaultReadBPS),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioTRBpsName, fmt.Sprintf("%s %d", diskNumber, DefaultReadBPS)),
	)
	writeIOPSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioTWIopsName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, DefaultWriteIOPS),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioTWIopsName, fmt.Sprintf("%s %d", diskNumber, DefaultWriteIOPS)),
	)
	writeBPSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioTWBpsName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, DefaultWriteBPS),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioTWBpsName, fmt.Sprintf("%s %d", diskNumber, DefaultWriteBPS)),
	)
	ioWeightUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioIOWeightName,
		dynamicPath,
		fmt.Sprintf("%s %d", diskNumber, DefaultIOWeightPercentage),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioIOWeightName, fmt.Sprintf("%s %d", diskNumber, DefaultIOWeightPercentage)),
	)

	resources = append(resources,
		readIOPSUpdater,
		readBPSUpdater,
		writeIOPSUpdater,
		writeBPSUpdater,
		ioWeightUpdater,
	)

	return
}

func getDiskConfigRemoverFromDiskNumber(diskNumber string, dynamicPath string) (resources []resourceexecutor.ResourceUpdater) {
	ioQoSUpdater, _ := resourceexecutor.NewBlkIOResourceUpdater(
		system.BlkioIOQoSName,
		dynamicPath,
		fmt.Sprintf("%s enable=0", diskNumber),
		audit.V(3).Group("blkio").Reason("UpdateBlkIO").Message("update %s/%s to %s", dynamicPath, system.BlkioIOQoSName, fmt.Sprintf("%s enable=0", diskNumber)),
	)
	resources = append(resources, ioQoSUpdater)
	return
}

func getDiskNumber(s *metriccache.NodeLocalStorageInfo, disk string) string {
	if s == nil {
		return ""
	}
	return s.DiskNumberMap[disk]
}

func getDiskByDevice(s *metriccache.NodeLocalStorageInfo, device string) string {
	if s == nil {
		return ""
	}
	if isDeviceDisk(s, device) {
		return device
	}
	return s.PartitionDiskMap[device]
}

func getDiskByVG(s *metriccache.NodeLocalStorageInfo, vgName string) string {
	if s == nil {
		return ""
	}
	return s.VGDiskMap[vgName]
}

func getDiskByMountPoint(s *metriccache.NodeLocalStorageInfo, mountpoint string) string {
	if s == nil {
		return ""
	}
	device := s.MPDiskMap[mountpoint]
	// check if device is disk
	_, exist := s.DiskNumberMap[device]
	if exist {
		return device
	}
	// check if device is part
	disk, exist := s.PartitionDiskMap[device]
	if exist {
		return disk
	}
	// check if device is lv
	vgName, exist := s.LVMapperVGMap[device]
	if exist {
		return s.VGDiskMap[vgName]
	}
	return ""
}

func isDeviceDisk(s *metriccache.NodeLocalStorageInfo, device string) bool {
	if s == nil {
		return false
	}
	_, yes := s.DiskNumberMap[device]
	return yes
}
