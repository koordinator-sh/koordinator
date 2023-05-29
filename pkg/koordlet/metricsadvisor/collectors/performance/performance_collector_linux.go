//go:build linux
// +build linux

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

package performance

import (
	"sync"
	"time"

	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/koordinator-sh/koordinator/pkg/features"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metriccache"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metrics"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/metricsadvisor/framework"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/resourceexecutor"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/statesinformer"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/perf"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/util/system"
	tools "github.com/koordinator-sh/koordinator/pkg/util"
)

type performanceCollector struct {
	cpiCollectInterval        time.Duration
	psiCollectInterval        time.Duration
	collectTimeWindowDuration time.Duration

	started        *atomic.Bool
	statesInformer statesinformer.StatesInformer
	metricCache    metriccache.MetricCache
	cgroupReader   resourceexecutor.CgroupReader
}

func New(opt *framework.Options) framework.Collector {
	return &performanceCollector{
		cpiCollectInterval:        opt.Config.CPICollectorInterval,
		psiCollectInterval:        opt.Config.PSICollectorInterval,
		collectTimeWindowDuration: opt.Config.CPICollectorTimeWindow,

		started:        atomic.NewBool(false),
		statesInformer: opt.StatesInformer,
		metricCache:    opt.MetricCache,
		cgroupReader:   opt.CgroupReader,
	}
}

func (p *performanceCollector) Enabled() bool {
	return features.DefaultKoordletFeatureGate.Enabled(features.CPICollector) || features.DefaultKoordletFeatureGate.Enabled(features.PSICollector)
}

func (p *performanceCollector) Setup(s *framework.Context) {}

func (p *performanceCollector) Run(stopCh <-chan struct{}) {
	if !cache.WaitForCacheSync(stopCh, p.statesInformer.HasSynced) {
		// Koordlet exit because of statesInformer sync failed.
		klog.Fatalf("timed out waiting for states informer caches to sync")
	}
	if features.DefaultKoordletFeatureGate.Enabled(features.PSICollector) {
		p.collectPSI(stopCh)
	}
	if features.DefaultKoordletFeatureGate.Enabled(features.CPICollector) {
		go wait.Until(p.collectContainerCPI, p.cpiCollectInterval, stopCh)
	}
}

func (p *performanceCollector) Started() bool {
	return p.started.Load()
}

func (p *performanceCollector) collectContainerCPI() {
	klog.V(6).Infof("start collectContainerCPI")
	timeWindow := time.Now()
	containerStatusesMap := map[*corev1.ContainerStatus]*statesinformer.PodMeta{}
	podMetas := p.statesInformer.GetAllPods()
	for _, meta := range podMetas {
		pod := meta.Pod
		for i := range pod.Status.ContainerStatuses {
			containerStat := &pod.Status.ContainerStatuses[i]
			containerStatusesMap[containerStat] = meta
		}
	}
	metricsChan := make(chan metriccache.MetricSample, len(containerStatusesMap)*2)
	pciMetrics := make([]metriccache.MetricSample, 0)
	// get container PCI Metric
	go p.getContainerPCIMetrics(containerStatusesMap, metricsChan)

	for item := range metricsChan {
		pciMetrics = append(pciMetrics, item)
	}
	// save container PCI metric to tsdb
	p.saveMetric(pciMetrics)

	p.started.Store(true)
	klog.V(5).Infof("collectContainerCPI for time window %s finished at %s, container num %d",
		timeWindow, time.Now(), len(containerStatusesMap))
}

func (p *performanceCollector) getContainerPCIMetrics(containerStatusesMap map[*corev1.ContainerStatus]*statesinformer.PodMeta, metricsChan chan<- metriccache.MetricSample) {
	defer close(metricsChan)

	var wg sync.WaitGroup
	wg.Add(len(containerStatusesMap))
	nodeCpuInfo, err := p.metricCache.GetNodeCPUInfo(&metriccache.QueryParam{})
	metrics.ResetContainerCPI()
	if err != nil {
		klog.Errorf("failed to get node cpu info : %v", err)
		return
	}
	cpuNumber := nodeCpuInfo.TotalInfo.NumberCPUs

	collectTime := time.Now()
	// get container CPI collectors for each container
	for containerStatus, parentPod := range containerStatusesMap {
		go func(status *corev1.ContainerStatus, podMeta *statesinformer.PodMeta, c chan<- metriccache.MetricSample) {
			defer wg.Done()
			collectorOnSingleContainer, err := p.getAndStartCollectorOnSingleContainer(podMeta.CgroupDir, status, cpuNumber)
			if err != nil {
				return
			}
			time.Sleep(p.collectTimeWindowDuration)
			// get single container cpi metrics
			p.profilePerfOnSingleContainer(status, collectorOnSingleContainer, podMeta.Pod, collectTime, metricsChan)
		}(containerStatus, parentPod, metricsChan)
	}
	wg.Wait()
}

func (p *performanceCollector) getAndStartCollectorOnSingleContainer(podParentCgroupDir string, containerStatus *corev1.ContainerStatus, number int32) (*perf.PerfCollector, error) {
	perfCollector, err := util.GetContainerPerfCollector(podParentCgroupDir, containerStatus, number)
	if err != nil {
		klog.Errorf("get and start container %s collector err: %v", containerStatus.Name, err)
		return nil, err
	}
	return perfCollector, nil
}

func (p *performanceCollector) profilePerfOnSingleContainer(status *corev1.ContainerStatus, collectorOnSingleContainer *perf.PerfCollector, pod *corev1.Pod, collectTime time.Time, metricsChan chan<- metriccache.MetricSample) {

	cycles, instructions, err := util.GetContainerCyclesAndInstructions(collectorOnSingleContainer)
	if err != nil {
		klog.Errorf("collect container %s cpi err: %v", status.Name, err)
		return
	}

	pciCycle, err01 := metriccache.ContainerCPI.GenerateSample(metriccache.MetricPropertiesFunc.ContainerCPI(string(pod.GetUID()), status.ContainerID, string(metriccache.CPIResourceCycle)), collectTime, float64(cycles))
	pciInstruction, err02 := metriccache.ContainerCPI.GenerateSample(metriccache.MetricPropertiesFunc.ContainerCPI(string(pod.GetUID()), status.ContainerID, string(metriccache.CPIResourceInstruction)), collectTime, float64(instructions))

	if err01 != nil || err02 != nil {
		klog.Warningf("failed to collect Container PCI, Cycle err: %s, Instruction err: %s", err01, err02)
		return
	}

	metricsChan <- pciCycle
	metricsChan <- pciInstruction

	err1 := collectorOnSingleContainer.CleanUp()
	if err1 != nil {
		klog.Errorf("collectorOnSingleContainer cleanup err : %v", err1)
	}

	metrics.RecordContainerCPI(status, pod, float64(cycles), float64(instructions))
}

func (p *performanceCollector) collectContainerPSI() {
	klog.V(6).Infof("start collectContainerPSI")
	timeWindow := time.Now()
	containerStatusesMap := map[*corev1.ContainerStatus]*statesinformer.PodMeta{}
	podMetas := p.statesInformer.GetAllPods()
	for _, meta := range podMetas {
		pod := meta.Pod
		for i := range pod.Status.ContainerStatuses {
			containerStat := &pod.Status.ContainerStatuses[i]
			containerStatusesMap[containerStat] = meta
		}
	}

	metricsChan := make(chan metriccache.MetricSample, len(containerStatusesMap)*2)
	psiMetrics := make([]metriccache.MetricSample, 0)

	go p.getContainerPSIMetrics(containerStatusesMap, metricsChan)

	for item := range metricsChan {
		psiMetrics = append(psiMetrics, item)
	}

	// save container's psi metrics to tsdb
	p.saveMetric(psiMetrics)

	p.started.Store(true)
	klog.V(5).Infof("collectContainerPSI for time window %s finished at %s, container num %d",
		timeWindow, time.Now(), len(containerStatusesMap))
}

func (p *performanceCollector) getContainerPSIMetrics(containerStatusesMap map[*corev1.ContainerStatus]*statesinformer.PodMeta, metricChan chan<- metriccache.MetricSample) {
	defer close(metricChan)

	var wg sync.WaitGroup
	wg.Add(len(containerStatusesMap))
	metrics.ResetContainerPSI()
	collectTime := time.Now()
	for containerStatus, podMeta := range containerStatusesMap {
		pod := podMeta.Pod
		cgroupDir := podMeta.CgroupDir
		go func(parentDir string, status *corev1.ContainerStatus, pod *corev1.Pod) {
			defer wg.Done()
			p.collectSingleContainerPSI(parentDir, status, pod, collectTime, metricChan)
		}(cgroupDir, containerStatus, pod)
	}
	wg.Wait()
}

func (p *performanceCollector) collectSingleContainerPSI(podParentCgroupDir string, containerStatus *corev1.ContainerStatus, pod *corev1.Pod, collectTime time.Time, metricChan chan<- metriccache.MetricSample) {
	containerPath, err := util.GetContainerCgroupParentDir(podParentCgroupDir, containerStatus)
	if err != nil {
		klog.Errorf("failed to get container path for container %v/%v/%v cgroup path failed, error: %v", pod.Namespace, pod.Name, containerStatus.Name, err)
		return
	}
	containerPSI, err := p.cgroupReader.ReadPSI(containerPath)
	if err != nil {
		klog.Errorf("collect container %s psi err: %v", containerStatus.Name, err)
		return
	}

	cpuSomeAvg10, err01 := metriccache.ContainerPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.ContainerPSI(string(pod.GetUID()), containerStatus.ContainerID, string(metriccache.PSIResourceCPU), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeSome)), collectTime, containerPSI.CPU.Some.Avg10)
	memSomeAvg10, err02 := metriccache.ContainerPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.ContainerPSI(string(pod.GetUID()), containerStatus.ContainerID, string(metriccache.PSIResourceMem), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeSome)), collectTime, containerPSI.Mem.Some.Avg10)
	ioSomeAvg10, err03 := metriccache.ContainerPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.ContainerPSI(string(pod.GetUID()), containerStatus.ContainerID, string(metriccache.PSIResourceIO), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeSome)), collectTime, containerPSI.IO.Some.Avg10)

	cpuFullAvg10, err04 := metriccache.ContainerPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.ContainerPSI(string(pod.GetUID()), containerStatus.ContainerID, string(metriccache.PSIResourceCPU), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeFull)), collectTime, containerPSI.CPU.Full.Avg10)
	memFullAvg10, err05 := metriccache.ContainerPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.ContainerPSI(string(pod.GetUID()), containerStatus.ContainerID, string(metriccache.PSIResourceMem), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeFull)), collectTime, containerPSI.Mem.Full.Avg10)
	ioFullAvg10, err06 := metriccache.ContainerPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.ContainerPSI(string(pod.GetUID()), containerStatus.ContainerID, string(metriccache.PSIResourceIO), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeFull)), collectTime, containerPSI.IO.Full.Avg10)

	cpuFullSupported, err07 := metriccache.ContainerPSICPUFullSupportedMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.PodContainer(string(pod.GetUID()), containerStatus.ContainerID), collectTime, tools.BoolToFloat64(containerPSI.CPU.FullSupported))

	if err01 != nil || err02 != nil || err03 != nil || err04 != nil || err05 != nil ||
		err06 != nil || err07 != nil {
		klog.Warningf(
			"failed to collect Container %s/%s/%s PSI failed, cpuSomeAvg10 err: %s, memSomeAvg10 err: %s, ioSomeAvg10 err: %s, cpuFullAvg10 err: %s, memFullAvg10 err: %s, ioFullAvg10 err: %s, cpuFullSupported err: %s",
			pod.GetNamespace(), pod.GetName(), containerStatus.Name, err02, err03, err04, err05, err06, err07)
		return
	}

	metricChan <- cpuSomeAvg10
	metricChan <- memSomeAvg10
	metricChan <- ioSomeAvg10
	metricChan <- cpuFullAvg10
	metricChan <- memFullAvg10
	metricChan <- ioFullAvg10
	metricChan <- cpuFullSupported

	metrics.RecordContainerPSI(containerStatus, pod, containerPSI)
}

func (p *performanceCollector) collectPodPSI() {
	klog.V(6).Infof("start collectPodPSI")
	timeWindow := time.Now()
	podMetas := p.statesInformer.GetAllPods()
	metrics.ResetPodPSI()

	metricChan := make(chan metriccache.MetricSample, len(podMetas)*2)
	psiMetrics := make([]metriccache.MetricSample, 0)

	go p.getPodPSIMetrics(podMetas, metricChan)
	for item := range metricChan {
		psiMetrics = append(psiMetrics, item)
	}

	// save pod psi metrics to tsdb
	p.saveMetric(psiMetrics)

	p.started.Store(true)
	klog.V(5).Infof("collectPodPSI for time window %s finished at %s, pod num %d",
		timeWindow, time.Now(), len(podMetas))
}

func (p *performanceCollector) getPodPSIMetrics(podMetas []*statesinformer.PodMeta, metricChan chan<- metriccache.MetricSample) {
	defer close(metricChan)

	var wg sync.WaitGroup
	wg.Add(len(podMetas))
	metrics.ResetPodPSI()
	collectTime := time.Now()
	for _, meta := range podMetas {
		pod := meta.Pod
		podCgroupDir := meta.CgroupDir
		go func(pod *corev1.Pod, podCgroupDir string, collectTime time.Time, metricChan chan<- metriccache.MetricSample) {
			defer wg.Done()
			p.collectSinglePodPSI(pod, podCgroupDir, collectTime, metricChan)
		}(pod, podCgroupDir, collectTime, metricChan)
	}
	wg.Wait()
}

func (p *performanceCollector) collectSinglePodPSI(pod *corev1.Pod, podCgroupDir string, collectTime time.Time, metricChan chan<- metriccache.MetricSample) {
	podPSI, err := p.cgroupReader.ReadPSI(podCgroupDir)
	if err != nil {
		klog.Errorf("collect pod %v/%v psi err: %v", pod.Namespace, pod.Name, err)
		return
	}

	cpuSomeAvg10, err01 := metriccache.PodPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.PodPSI(string(pod.GetUID()), string(metriccache.PSIResourceCPU), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeSome)), collectTime, podPSI.CPU.Some.Avg10)
	memSomeAvg10, err02 := metriccache.PodPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.PodPSI(string(pod.GetUID()), string(metriccache.PSIResourceMem), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeSome)), collectTime, podPSI.Mem.Some.Avg10)
	ioSomeAvg10, err03 := metriccache.PodPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.PodPSI(string(pod.GetUID()), string(metriccache.PSIResourceIO), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeSome)), collectTime, podPSI.IO.Some.Avg10)

	cpuFullAvg10, err04 := metriccache.PodPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.PodPSI(string(pod.GetUID()), string(metriccache.PSIResourceCPU), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeFull)), collectTime, podPSI.CPU.Full.Avg10)
	memFullAvg10, err05 := metriccache.PodPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.PodPSI(string(pod.GetUID()), string(metriccache.PSIResourceMem), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeFull)), collectTime, podPSI.Mem.Full.Avg10)
	ioFullAvg10, err06 := metriccache.PodPSIMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.PodPSI(string(pod.GetUID()), string(metriccache.PSIResourceIO), string(metriccache.PSIPrecision10), string(metriccache.PSIDegreeFull)), collectTime, podPSI.IO.Full.Avg10)
	cpuFullSupported, err07 := metriccache.PodPSICPUFullSupportedMetric.GenerateSample(
		metriccache.MetricPropertiesFunc.Pod(string(pod.GetUID())), collectTime, tools.BoolToFloat64(podPSI.CPU.FullSupported))

	if err01 != nil || err02 != nil || err03 != nil || err04 != nil || err05 != nil ||
		err06 != nil || err07 != nil {
		klog.Warningf(
			"failed to collect pod %s/%s PSI, cpuSomeAvg10 err: %s, memSomeAvg10 err: %s, ioSomeAvg10 err: %s, cpuFullAvg10 err: %s, memFullAvg10 err: %s, ioFullAvg10 err: %s, cpuFullSupported err: %s",
			pod.GetNamespace(), pod.GetName(), err02, err03, err04, err05, err06, err07)
		return
	}

	metricChan <- cpuSomeAvg10
	metricChan <- memSomeAvg10
	metricChan <- ioSomeAvg10
	metricChan <- cpuFullAvg10
	metricChan <- memFullAvg10
	metricChan <- ioFullAvg10
	metricChan <- cpuFullSupported

	metrics.RecordPodPSI(pod, podPSI)
}

func (p *performanceCollector) collectPSI(stopCh <-chan struct{}) {
	// CgroupV1 psi collector support only on anolis os currently
	if system.GetCurrentCgroupVersion() == system.CgroupVersionV1 {
		cpuPressureCheck, _ := system.CPUAcctCPUPressure.IsSupported("")
		memPressureCheck, _ := system.CPUAcctMemoryPressure.IsSupported("")
		ioPressureCheck, _ := system.CPUAcctIOPressure.IsSupported("")
		if !(cpuPressureCheck && memPressureCheck && ioPressureCheck) {
			klog.V(4).Infof("Collect psi failed, system now not support psi feature in CgroupV1, please check pressure file exist and readable in cpuacct directory.")
			//skip collect psi when system not support
			p.started.Store(true)
			return
		}
	}
	go wait.Until(func() {
		p.collectContainerPSI()
		p.collectPodPSI()
	}, p.psiCollectInterval, stopCh)
}

func (p *performanceCollector) saveMetric(samples []metriccache.MetricSample) error {
	if len(samples) == 0 {
		return nil
	}

	appender := p.metricCache.Appender()
	if err := appender.Append(samples); err != nil {
		klog.ErrorS(err, "Append container metrics error")
		return err
	}

	if err := appender.Commit(); err != nil {
		klog.ErrorS(err, "Commit container metrics failed")
		return err
	}

	return nil
}
