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

package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/cgroup"
	"github.com/koordinator-sh/koordinator/pkg/koordlet/qosmanager/plugins/psi/podcgroup"
)

const AnnotaionPSIExport = "koordinator.sh/psi-export"

var DefaultPSIExporter Operator = &PSIExport{
	CPU: &StallThreshold{
		Avg10:  20,
		Avg60:  20,
		Avg300: 20,
	},
	Memory: &StallThreshold{
		Avg10:  20,
		Avg60:  20,
		Avg300: 20,
	},
	IO: &StallThreshold{
		Avg10:  20,
		Avg60:  20,
		Avg300: 20,
	},
}

type PSIExport struct {
	CPU    *StallThreshold `json:"cpu,omitempty"`
	Memory *StallThreshold `json:"memory,omitempty"`
	IO     *StallThreshold `json:"io,omitempty"`

	clientset kubernetes.Interface `json:"-"`
}

func (pe *PSIExport) Name() string {
	return "PSIExport"
}

func (pe *PSIExport) Init() error {
	config, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}
	pe.clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to new clientset: %w", err)
	}
	return nil
}

func (pe *PSIExport) Update(g Operator) error {
	if v, ok := g.(*PSIExport); ok {
		pe.CPU = v.CPU
		pe.Memory = v.Memory
		pe.IO = v.IO
		return nil
	}
	return fmt.Errorf("not a PSIExport")
}

func (pe *PSIExport) Exec(pods map[types.UID]*podcgroup.PodCgroup, node *v1.Node) error {
	var errs []error
	nowTime := metav1.Now()
	for _, pc := range pods {
		if pc.Pod.Annotations[AnnotaionPSIExport] != "true" {
			continue
		}
		psi := GetPSIStatus(pc.Cgroup)
		pod := pc.Pod.DeepCopy()
		newPod := pc.Pod.DeepCopy()
		if pe.CPU != nil {
			if err := pe.pressureCondition(newPod, nowTime, PodCpuInPressure, psi.Cpu, *pe.CPU); err != nil {
				errs = append(errs, fmt.Errorf("failed to update %s for pod %s: %w", PodCpuInPressure, klog.KObj(newPod), err))
				continue
			}
		}
		if pe.Memory != nil {
			if err := pe.pressureCondition(newPod, nowTime, PodMemoryInPressure, psi.Memory, *pe.Memory); err != nil {
				errs = append(errs, fmt.Errorf("failed to update %s for pod %s: %w", PodMemoryInPressure, klog.KObj(newPod), err))
				continue
			}
		}
		if pe.IO != nil {
			if err := pe.pressureCondition(newPod, nowTime, PodIOInPressure, psi.IO, *pe.IO); err != nil {
				errs = append(errs, fmt.Errorf("failed to update %s for pod %s: %w", PodIOInPressure, klog.KObj(newPod), err))
				continue
			}
		}
		if reflect.DeepEqual(pod.Status.Conditions, newPod.Status.Conditions) {
			continue
		}
		pod, err := pe.patchPodStatus(pod, newPod)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to patch pod %s: %w", klog.KObj(newPod), err))
			continue
		}
		pc.Pod = pod
	}
	return errors.Join(errs...)
}

func (pe *PSIExport) AddPod(*podcgroup.PodCgroup) error { return nil }

func (pe *PSIExport) DeletePod(*podcgroup.PodCgroup) error { return nil }

func (pe *PSIExport) pressureCondition(pod *v1.Pod, nowTime metav1.Time, conditionType v1.PodConditionType, info StallInformation, th StallThreshold) error {
	var condition *v1.PodCondition

	for i := range pod.Status.Conditions {
		if pod.Status.Conditions[i].Type == conditionType {
			condition = &pod.Status.Conditions[i]
			break
		}
	}

	newCondition := false
	if condition == nil {
		condition = &v1.PodCondition{
			Type:   conditionType,
			Status: v1.ConditionUnknown,
		}
		newCondition = true
	}

	if th.Exploded(info) {
		if condition.Status != v1.ConditionTrue || condition.Message != info.String() {
			condition.Status = v1.ConditionTrue
			condition.LastTransitionTime = nowTime
			condition.Reason = string(conditionType)
			condition.Message = info.String()
		}
	} else if condition.Status != v1.ConditionFalse {
		condition.Status = v1.ConditionFalse
		condition.LastTransitionTime = nowTime
		condition.Reason = strings.ReplaceAll(string(conditionType), "In", "NotIn")
		condition.Message = ""
	}
	if newCondition {
		pod.Status.Conditions = append(pod.Status.Conditions, *condition)
	}
	return nil
}

func (pe *PSIExport) patchPodStatus(pod *v1.Pod, newPod *v1.Pod) (*v1.Pod, error) {
	newPod.Spec = pod.Spec
	oldData, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	newData, err := json.Marshal(newPod)
	if err != nil {
		return nil, err
	}
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, v1.Pod{})
	if err != nil {
		return nil, err
	}
	pod, err = pe.clientset.CoreV1().Pods(pod.Namespace).Patch(context.TODO(), pod.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, "status")
	if err != nil {
		return nil, err
	}
	klog.InfoS("Patch pod %s success", "pod", klog.KObj(pod), "patch", string(patchBytes))
	return pod, nil
}

const (
	PodCpuInPressure    v1.PodConditionType = "PodCpuInPressure"
	PodMemoryInPressure v1.PodConditionType = "PodMemoryInPressure"
	PodIOInPressure     v1.PodConditionType = "PodIOInPressure"
)

type PSIStatus struct {
	Cpu       StallInformation `json:"cpu"`
	Memory    StallInformation `json:"memory"`
	IO        StallInformation `json:"io"`
	Timestamp metav1.Time      `json:"timestamp"`
}

type StallInformation struct {
	Avg10  float64 `json:"avg10"`
	Avg60  float64 `json:"avg60"`
	Avg300 float64 `json:"avg300"`
}

func (si StallInformation) String() string {
	b, err := json.Marshal(si)
	if err != nil {
		return fmt.Sprintf("%#v", si)
	}
	return string(b)
}

func GetPSIStatus(cg *cgroup.Cgroup) *PSIStatus {
	return &PSIStatus{
		Cpu: StallInformation{
			Avg10:  cg.Cpu.Pressure10(),
			Avg60:  cg.Cpu.Pressure60(),
			Avg300: cg.Cpu.Pressure300(),
		},
		Memory: StallInformation{
			Avg10:  cg.Memory.Pressure10(),
			Avg60:  cg.Memory.Pressure60(),
			Avg300: cg.Memory.Pressure300(),
		},
		IO: StallInformation{
			Avg10:  cg.Iops.Pressure10(),
			Avg60:  cg.Iops.Pressure60(),
			Avg300: cg.Iops.Pressure300(),
		},
		Timestamp: metav1.Now(),
	}
}

type StallThreshold StallInformation

func (th *StallThreshold) Exploded(info StallInformation) bool {
	return info.Avg10 > th.Avg10 || info.Avg60 > th.Avg60 || info.Avg300 > th.Avg300
}
