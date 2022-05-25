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
	"math"
	"math/bits"
	"os"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/apis/extension"
	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/util"
	"github.com/koordinator-sh/koordinator/pkg/util/system"
)

const (
	// RootResctrlGroup is the name of the root resctrl group
	RootResctrlGroup = ""
	// LSRResctrlGroup is the name of LSR resctrl group
	LSRResctrlGroup = "LSR"
	// LSResctrlGroup is the name of LS resctrl group
	LSResctrlGroup = "LS"
	// BEResctrlGroup is the name of BE resctrl group
	BEResctrlGroup = "BE"
	// UnknownResctrlGroup is the resctrl group which is unknown to reconcile
	UnknownResctrlGroup = "Unknown"
	// L3SchemataPrefix is the prefix of l3 cat schemata
	L3SchemataPrefix = "L3:"
	// MbSchemataPrefix is the prefix of l3 cat schemata
	MbSchemataPrefix = "MB:"
)

var (
	// resctrlGroupList is the list of resctrl groups to be reconcile
	resctrlGroupList = []string{LSRResctrlGroup, LSResctrlGroup, BEResctrlGroup}
)

type ResctrlReconcile struct {
	resManager *resmanager
	executor   *ResourceUpdateExecutor
}

func NewResctrlReconcile(resManager *resmanager) *ResctrlReconcile {
	executor := NewResourceUpdateExecutor("ResctrlExecutor", resManager.config.ReconcileIntervalSeconds*60)
	return &ResctrlReconcile{
		resManager: resManager,
		executor:   executor,
	}
}

func (r *ResctrlReconcile) RunInit(stopCh <-chan struct{}) error {
	r.executor.Run(stopCh)
	return nil
}

func getPodResctrlGroup(pod *corev1.Pod) string {
	podQoS := extension.GetPodQoSClass(pod)
	switch podQoS {
	case extension.QoSLSR:
		return LSRResctrlGroup
	case extension.QoSLS:
		return LSResctrlGroup
	case extension.QoSBE:
		return BEResctrlGroup
	}
	return UnknownResctrlGroup
}

func getResourceQoSForResctrlGroup(strategy *slov1alpha1.ResourceQoSStrategy, group string) *slov1alpha1.ResourceQoS {
	if strategy == nil {
		return nil
	}
	switch group {
	case LSRResctrlGroup:
		return strategy.LSR
	case LSResctrlGroup:
		return strategy.LS
	case BEResctrlGroup:
		return strategy.BE
	}
	return nil
}

func initCatResctrl() error {
	// check if the resctrl root and l3_cat feature are enabled correctly
	if err := system.CheckAndTryEnableResctrlCat(); err != nil {
		klog.Errorf("check resctrl cat failed, err: %s", err)
		return err
	}
	for _, group := range resctrlGroupList {
		if err := initCatGroupIfNotExist(group); err != nil {
			klog.Errorf("init cat group dir %v failed, error %v", group, err)
		} else {
			klog.V(5).Infof("create cat dir for group %v successfully", group)
		}
	}
	return nil
}

func initCatGroupIfNotExist(group string) error {
	path := system.GetResctrlGroupRootDirPath(group)
	_, err := os.Stat(path)
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check dir %v for group %s but got unexpected err: %v", path, group, err)
	}
	err = os.Mkdir(path, 0755)
	if err != nil {
		return fmt.Errorf("create dir %v failed for group %s, err: %v", path, group, err)
	}
	return nil
}

func calculateCatL3MaskValue(cbm uint, startPercent, endPercent int64) (string, error) {
	// check if the parsed cbm value is valid, eg. 0xff, 0x1, 0x7ff, ...
	// NOTE: (Cache Bit Masks) X86 hardware requires that these masks have all the '1' bits in a contiguous block.
	//       ref: https://www.kernel.org/doc/Documentation/x86/intel_rdt_ui.txt
	// since the input cbm here is the cbm value of the resctrl root, every lower bit is required to be `1` additionally
	if bits.OnesCount(cbm+1) != 1 {
		return "", fmt.Errorf("illegal cbm %v", cbm)
	}

	// check if the startPercent and endPercent are valid
	if startPercent < 0 || endPercent > 100 || endPercent <= startPercent {
		return "", fmt.Errorf("illegal l3 cat percent: start %v, end %v", startPercent, endPercent)
	}

	// calculate a bit mask belonging to interval [startPercent% * ways, endPercent% * ways)
	// eg.
	// cbm 0x3ff ('b1111111111), start 10%, end 80%
	// ways 10, l3Mask 0xfe ('b11111110)
	// cbm 0x7ff ('b11111111111), start 10%, end 50%
	// ways 11, l3Mask 0x3c ('b111100)
	// cbm 0x7ff ('b11111111111), start 0%, end 30%
	// ways 11, l3Mask 0xf ('b1111)
	ways := float64(bits.Len(cbm))
	startWay := uint64(math.Ceil(ways * float64(startPercent) / 100))
	endWay := uint64(math.Ceil(ways * float64(endPercent) / 100))

	var l3Mask uint64 = (1 << endWay) - (1 << startWay)
	return strconv.FormatUint(l3Mask, 16), nil
}

func calculateL3SchemataResource(group, schemataDelta string, l3Num int) ResourceUpdater {
	schemata := L3SchemataPrefix
	// the last ';' will be auto ignored
	for i := 0; i < l3Num; i++ {
		schemata = schemata + strconv.Itoa(i) + "=" + schemataDelta + ";"
	}
	// the trailing '\n' is necessary to append
	schemata += "\n"

	schemataFile := system.GetResctrlSchemataFilePath(group)

	// write to $schemataFile with valued $schemata
	updaterKey := schemataFile + ":" + L3SchemataPrefix
	return NewDetailCommonResourceUpdater(updaterKey, schemataFile, schemata, GroupOwnerRef(group), updateResctrlSchemataFunc)
}

func calculateMbSchemataResource(group, schemataDelta string, l3Num int) ResourceUpdater {
	schemata := MbSchemataPrefix
	// the last ';' will be auto ignored
	for i := 0; i < l3Num; i++ {
		schemata = schemata + strconv.Itoa(i) + "=" + schemataDelta + ";"
	}
	// the trailing '\n' is necessary to append
	schemata += "\n"

	schemataFile := system.GetResctrlSchemataFilePath(group)

	// write to $schemataFile with valued $schemata
	updaterKey := schemataFile + ":" + MbSchemataPrefix
	return NewDetailCommonResourceUpdater(updaterKey, schemataFile, schemata, GroupOwnerRef(group), updateResctrlSchemataFunc)
}

func calculateMbaPercentForGroup(group string, mbaPercentConfig *int64) string {
	if mbaPercentConfig == nil {
		klog.Warningf("cat MBA will not change, since MBAPercent is nil for group %v, "+
			"mbaPercentConfig %v", mbaPercentConfig, group)
		return ""
	}

	if *mbaPercentConfig <= 0 || *mbaPercentConfig > 100 {
		klog.Warningf("cat MBA will not change, since MBAPercent is not in [1,100] for group %v, "+
			"MBAPercent %d", group, *mbaPercentConfig)
		return ""
	}

	if *mbaPercentConfig%10 != 0 {
		actualPercent := *mbaPercentConfig/10*10 + 10
		klog.Warningf("cat MBA must multiple of 10, group: %v, mbaPercentConfig is %d, actualMBAPercent will be %d",
			group, *mbaPercentConfig, actualPercent)
		return strconv.FormatInt(actualPercent, 10)
	}

	return strconv.FormatInt(*mbaPercentConfig, 10)
}

func getPodCgroupNewTaskIds(podMeta *statesinformer.PodMeta, tasksMap map[int]struct{}) []int {
	var taskIds []int

	pod := podMeta.Pod
	containerMap := make(map[string]*corev1.Container, len(pod.Spec.Containers))
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		containerMap[container.Name] = container
	}
	for _, containerStat := range pod.Status.ContainerStatuses {
		// reconcile containers
		container, exist := containerMap[containerStat.Name]
		if !exist {
			klog.Warningf("container %s/%s/%s lost during reconcile resctrl group", pod.Namespace,
				pod.Name, containerStat.Name)
			continue
		}

		ids, err := util.GetContainerCurTasks(podMeta.CgroupDir, &containerStat)
		if err != nil {
			klog.Warningf("failed to get pod container cgroup task ids for container %s/%s/%s, err: %s",
				pod.Namespace, pod.Name, container.Name, err)
			continue
		}

		// only append the non-mapped ids
		if tasksMap == nil {
			taskIds = append(taskIds, ids...)
			continue
		}
		for _, id := range ids {
			if _, ok := tasksMap[id]; !ok {
				taskIds = append(taskIds, id)
			}
		}
	}

	return taskIds
}

func calculateL3TasksResource(group string, taskIds []int) ResourceUpdater {
	// join ids into updater value and make the id updates one by one
	tasksPath := system.GetResctrlTasksFilePath(group)

	// use ordered slice
	sort.Ints(taskIds)
	var builder strings.Builder
	for _, id := range taskIds {
		builder.WriteString(strconv.Itoa(id))
		builder.WriteByte('\n')
	}

	return NewDetailCommonResourceUpdater(tasksPath, tasksPath, builder.String(), GroupOwnerRef(group), updateResctrlTasksFunc)
}

func (r *ResctrlReconcile) calculateAndApplyCatL3PolicyForGroup(group string, cbm uint, l3Num int,
	resourceQoS *slov1alpha1.ResourceQoS) error {
	if resourceQoS == nil || resourceQoS.ResctrlQoS == nil || resourceQoS.ResctrlQoS.CATRangeStartPercent == nil ||
		resourceQoS.ResctrlQoS.CATRangeEndPercent == nil {
		klog.Warningf("skipped, since resourceQoS or startPercent or endPercent is nil for group %v, "+
			"resourceQoS %v", resourceQoS, group)
		return nil
	}

	startPercent, endPercent := *resourceQoS.ResctrlQoS.CATRangeStartPercent, *resourceQoS.ResctrlQoS.CATRangeEndPercent
	// calculate policy
	l3MaskValue, err := calculateCatL3MaskValue(cbm, startPercent, endPercent)
	if err != nil {
		klog.Warningf("failed to calculate l3 cat schemata for group %v, err: %v", group, err)
		return err
	}

	// calculate updating resource
	resource := calculateL3SchemataResource(group, l3MaskValue, l3Num)

	// write policy into resctrl files if need update
	isUpdated, err := r.executor.UpdateByCache(resource)
	if err != nil {
		klog.Warningf("failed to write l3 cat policy on schemata for group %s, err: %s", group, err)
		return err
	}
	klog.V(5).Infof("apply l3 cat policy for group %s finished, schemata %v, l3 number %v, isUpdated %v",
		group, l3MaskValue, l3Num, isUpdated)

	return nil
}

func (r *ResctrlReconcile) calculateAndApplyCatMbPolicyForGroup(group string, l3Num int, resourceQoS *slov1alpha1.ResourceQoS) error {
	if resourceQoS == nil || resourceQoS.ResctrlQoS == nil {
		klog.Warningf("skipped, since resourceQoS or ResctrlQoS is nil for group %v, "+
			"resourceQoS %v", resourceQoS, group)
		return nil
	}

	memBwPercent := calculateMbaPercentForGroup(group, resourceQoS.ResctrlQoS.MBAPercent)
	if memBwPercent == "" {
		return nil
	}
	// calculate updating resource
	resource := calculateMbSchemataResource(group, memBwPercent, l3Num)

	// write policy into resctrl files if need update
	isUpdated, err := r.executor.UpdateByCache(resource)
	if err != nil {
		klog.Warningf("failed to write mb cat policy on schemata for group %s, err: %s", group, err)
		return err
	}
	klog.V(5).Infof("apply mb cat policy for group %s finished, schemata %v, l3 number %v, isUpdated %v",
		group, memBwPercent, l3Num, isUpdated)
	return nil
}

func (r *ResctrlReconcile) calculateAndApplyCatL3GroupTasks(group string, taskIds []int) error {
	resource := calculateL3TasksResource(group, taskIds)

	// write policy into resctrl files
	// NOTE: the operation should not be cacheable, since old tid has chance to be reused by a new task and here the
	// tasks ids are the realtime diff between cgroup and resctrl
	err := r.executor.Update(resource)
	if err != nil {
		klog.Warningf("failed to write l3 cat policy on tasks for group %s, err: %s", group, err)
		return err
	}
	klog.V(5).Infof("apply l3 cat tasks for group %s finished, len(taskIds) %v", group, len(taskIds))

	return nil
}

func (r *ResctrlReconcile) reconcileCatResctrlPolicy(qosStrategy *slov1alpha1.ResourceQoSStrategy) {
	// 1. retrieve rdt configs from nodeSLOSpec
	// 2.1 get cbm and l3 numbers, which are general for all resctrl groups
	// 2.2 calculate applying resctrl policies, like cat policy and so on, with each rdt config
	// 3. apply the policies onto resctrl groups

	// read cat l3 cbm
	nodeCPUInfo, err := r.resManager.metricCache.GetNodeCPUInfo(&metriccache.QueryParam{})
	if err != nil {
		klog.Warningf("failed to get nodeCPUInfo, err: %v", err)
		return
	}
	if nodeCPUInfo == nil {
		klog.Warning("failed to get nodeCPUInfo, the value is nil")
		return
	}
	cbmStr := nodeCPUInfo.BasicInfo.CatL3CbmMask
	if len(cbmStr) <= 0 {
		klog.Warning("failed to get cat l3 cbm, cbm is empty")
		return
	}
	cbmValue, err := strconv.ParseUint(cbmStr, 16, 32)
	if err != nil {
		klog.Warningf("failed to parse cat l3 cbm %s, err: %v", cbmStr, err)
		return
	}
	cbm := uint(cbmValue)

	// get the number of l3 caches; it is larger than 0
	l3Num := int(nodeCPUInfo.TotalInfo.NumberL3s)
	if l3Num <= 0 {
		klog.Warningf("failed to get the number of l3 caches, invalid value %v", l3Num)
		return
	}

	// calculate and apply l3 cat policy for each group
	for _, group := range resctrlGroupList {
		resQoSStrategy := getResourceQoSForResctrlGroup(qosStrategy, group)
		err = r.calculateAndApplyCatL3PolicyForGroup(group, cbm, l3Num, resQoSStrategy)
		if err != nil {
			klog.Warningf("failed to apply l3 cat policy for group %v, err: %v", group, err)
		}
		err = r.calculateAndApplyCatMbPolicyForGroup(group, l3Num, resQoSStrategy)
		if err != nil {
			klog.Warningf("failed to apply cat MB policy for group %v, err: %v", group, err)
		}
	}
}

func (r *ResctrlReconcile) reconcileResctrlGroups(qosStrategy *slov1alpha1.ResourceQoSStrategy) {
	// 1. retrieve task ids for each slo by reading cgroup task file of every pod container
	// 2. add the related task ids in resctrl groups

	// NOTE: pid_max can be found in `/proc/sys/kernel/pid_max` on linux.
	// the maximum pid on 32-bit/64-bit platforms is always less than 4194304, so the int type is bigger enough.
	// here we only append the task ids which only appear in cgroup but not in resctrl to reduce resctrl writes
	var err error

	curTaskMaps := map[string]map[int]struct{}{}
	for _, group := range resctrlGroupList {
		curTaskMaps[group], err = system.ReadResctrlTasksMap(group)
		if err != nil {
			klog.Warningf("failed to read Cat L3 tasks for resctrl group %s, err: %s", group, err)
		}
	}

	taskIds := map[string][]int{}
	podsMeta := r.resManager.statesInformer.GetAllPods()
	for _, podMeta := range podsMeta {
		pod := podMeta.Pod
		// only Running and Pending pods are considered
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}

		// only extension-QoS-specified pod are considered
		podQoSCfg := getPodResourceQoSByQoSClass(pod, qosStrategy, r.resManager.config)
		if podQoSCfg.ResctrlQoS.Enable == nil || !(*podQoSCfg.ResctrlQoS.Enable) {
			klog.V(5).Infof("pod %v with qos %v disabled resctrl", util.GetPodKey(pod), extension.GetPodQoSClass(pod))
			continue
		}

		// TODO https://github.com/koordinator-sh/koordinator/pull/94#discussion_r858779795
		if group := getPodResctrlGroup(pod); group != UnknownResctrlGroup {
			ids := getPodCgroupNewTaskIds(podMeta, curTaskMaps[group])
			taskIds[group] = append(taskIds[group], ids...)
		}
	}

	// write Cat L3 tasks for each resctrl group
	for _, group := range resctrlGroupList {
		err = r.calculateAndApplyCatL3GroupTasks(group, taskIds[group])
		if err != nil {
			klog.Warningf("failed to apply l3 cat tasks for group %s, err %s", group, err)
		}
	}
}

func (r *ResctrlReconcile) reconcile() {
	// Step 0. create and init them if resctrl groups do not exist
	// Step 1. reconcile rdt policies against `schemata` file
	// Step 2. reconcile resctrl groups against `tasks` file

	// Step 0.
	if r.resManager == nil || r.executor == nil {
		klog.Warning("ResctrlReconcile failed, uninitialized")
		return
	}
	nodeSLO := r.resManager.getNodeSLOCopy()
	if nodeSLO == nil || nodeSLO.Spec.ResourceQoSStrategy == nil {
		// do nothing if nodeSLO == nil || nodeSLO.spec.ResourceStrategy == nil
		klog.Warningf("nodeSLO is nil %v, or nodeSLO.Spec.ResourceQoSStrategy is nil %v",
			nodeSLO == nil, nodeSLO.Spec.ResourceQoSStrategy == nil)
		return
	}

	// skip if host not support resctrl
	if support, err := system.IsSupportResctrl(); err != nil {
		klog.Warningf("check support resctrl failed, err: %s", err)
		return
	} else if !support {
		klog.V(5).Infof("ResctrlReconcile skipped, cpu not support CAT/MBA")
		return
	}

	if err := initCatResctrl(); err != nil {
		klog.Warningf("ResctrlReconcile failed, cannot initialize cat resctrl group, err: %s", err)
		return
	}
	r.reconcileCatResctrlPolicy(nodeSLO.Spec.ResourceQoSStrategy)
	r.reconcileResctrlGroups(nodeSLO.Spec.ResourceQoSStrategy)
}
