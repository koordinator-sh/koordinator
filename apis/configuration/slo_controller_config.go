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

package configuration

import (
	"github.com/mohae/deepcopy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	slov1alpha1 "github.com/koordinator-sh/koordinator/apis/slo/v1alpha1"
)

const (
	// keys in the configmap
	ColocationConfigKey            = "colocation-config"
	ResourceThresholdConfigKey     = "resource-threshold-config"
	ResourceQOSConfigKey           = "resource-qos-config"
	CPUBurstConfigKey              = "cpu-burst-config"
	SystemConfigKey                = "system-config"
	HostApplicationConfigKey       = "host-application-config"
	CPUNormalizationConfigKey      = "cpu-normalization-config"
	ResourceAmplificationConfigKey = "resource-amplification-config"
)

// +k8s:deepcopy-gen=true
type NodeCfgProfile struct {
	// like ID for different nodeSelector; it's useful for console so that we can modify nodeCfg or nodeSelector by name
	Name string `json:"name,omitempty"`
	// an empty label selector matches all objects while a nil label selector matches no objects
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

// +k8s:deepcopy-gen=true
type ColocationCfg struct {
	ColocationStrategy `json:",inline"`
	NodeConfigs        []NodeColocationCfg `json:"nodeConfigs,omitempty" validate:"dive"`
}

// +k8s:deepcopy-gen=true
type NodeColocationCfg struct {
	NodeCfgProfile `json:",inline"`
	ColocationStrategy
}

// +k8s:deepcopy-gen=true
type ResourceThresholdCfg struct {
	ClusterStrategy *slov1alpha1.ResourceThresholdStrategy `json:"clusterStrategy,omitempty"`
	NodeStrategies  []NodeResourceThresholdStrategy        `json:"nodeStrategies,omitempty" validate:"dive"`
}

// +k8s:deepcopy-gen=true
type NodeResourceThresholdStrategy struct {
	NodeCfgProfile `json:",inline"`
	*slov1alpha1.ResourceThresholdStrategy
}

// +k8s:deepcopy-gen=true
type NodeCPUBurstCfg struct {
	NodeCfgProfile `json:",inline"`
	*slov1alpha1.CPUBurstStrategy
}

// +k8s:deepcopy-gen=true
type CPUBurstCfg struct {
	ClusterStrategy *slov1alpha1.CPUBurstStrategy `json:"clusterStrategy,omitempty"`
	NodeStrategies  []NodeCPUBurstCfg             `json:"nodeStrategies,omitempty" validate:"dive"`
}

// +k8s:deepcopy-gen=true
type NodeSystemStrategy struct {
	NodeCfgProfile `json:",inline"`
	*slov1alpha1.SystemStrategy
}

// +k8s:deepcopy-gen=true
type SystemCfg struct {
	ClusterStrategy *slov1alpha1.SystemStrategy `json:"clusterStrategy,omitempty"`
	NodeStrategies  []NodeSystemStrategy        `json:"nodeStrategies,omitempty" validate:"dive"`
}

// +k8s:deepcopy-gen=true
type NodeHostApplicationCfg struct {
	NodeCfgProfile `json:",inline"`
	Applications   []slov1alpha1.HostApplicationSpec `json:"applications,omitempty"`
}

// +k8s:deepcopy-gen=true
type HostApplicationCfg struct {
	Applications []slov1alpha1.HostApplicationSpec `json:"applications,omitempty"`
	NodeConfigs  []NodeHostApplicationCfg          `json:"nodeConfigs,omitempty"`
}

// +k8s:deepcopy-gen=true
type ResourceQOSCfg struct {
	ClusterStrategy *slov1alpha1.ResourceQOSStrategy `json:"clusterStrategy,omitempty"`
	NodeStrategies  []NodeResourceQOSStrategy        `json:"nodeStrategies,omitempty" validate:"dive"`
}

// +k8s:deepcopy-gen=true
type NodeResourceQOSStrategy struct {
	NodeCfgProfile `json:",inline"`
	*slov1alpha1.ResourceQOSStrategy
}

// +k8s:deepcopy-gen=true
type ExtensionCfgMap struct {
	Object map[string]ExtensionCfg `json:",inline"`
}

// +k8s:deepcopy-gen=false
type ExtensionCfg struct {
	ClusterStrategy interface{}             `json:"clusterStrategy,omitempty"`
	NodeStrategies  []NodeExtensionStrategy `json:"nodeStrategies,omitempty"`
}

func (in *ExtensionCfg) DeepCopyInto(out *ExtensionCfg) {
	*out = *in
	if in.ClusterStrategy != nil {
		outIf := deepcopy.Copy(in.ClusterStrategy)
		out.ClusterStrategy = outIf
	}
	if in.NodeStrategies != nil {
		in, out := &in.NodeStrategies, &out.NodeStrategies
		*out = make([]NodeExtensionStrategy, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *ExtensionCfg) DeepCopy() *ExtensionCfg {
	if in == nil {
		return nil
	}
	out := new(ExtensionCfg)
	in.DeepCopyInto(out)
	return out
}

// +k8s:deepcopy-gen=false
type NodeExtensionStrategy struct {
	NodeCfgProfile `json:",inline"`
	NodeStrategy   interface{} `json:",inline"` // for third-party extension
}

func (in *NodeExtensionStrategy) DeepCopyInto(out *NodeExtensionStrategy) {
	*out = *in
	in.NodeCfgProfile.DeepCopyInto(&out.NodeCfgProfile)
	if in.NodeStrategy != nil {
		outIf := deepcopy.Copy(in.NodeStrategy)
		out.NodeStrategy = outIf
	}
}

func (in *NodeExtensionStrategy) DeepCopy() *NodeExtensionStrategy {
	if in == nil {
		return nil
	}
	out := new(NodeExtensionStrategy)
	in.DeepCopyInto(out)
	return out
}

// CalculatePolicy defines the calculate policy for resource overcommitment.
// Default is "usage".
type CalculatePolicy string

const (
	// CalculateByPodUsage is the calculate policy according to the pod resource usage.
	// When the policy="usage", the low-priority (LP) resources are calculated according to the high-priority (HP) pods'
	// usages, so LP pod can reclaim the requested but unused resources of the HP pods.
	// It is the default policy where the resources are over-committed between priority bands.
	CalculateByPodUsage CalculatePolicy = "usage"
	// CalculateByPodRequest is the calculate policy according to the pod resource request.
	// When the policy="request", the low-priority (LP) resources are calculated according to the high-priority (HP)
	// pods' requests, so LP pod can allocate the unallocated resources of the HP pods but can NOT reclaim the
	// requested but unused resources of the HP pods.
	// It is the policy where the resources are NOT over-committed between priority bands.
	CalculateByPodRequest CalculatePolicy = "request"
	// CalculateByPodMaxUsageRequest is the calculate policy according to the maximum of the pod usage and request.
	// When the policy="maxUsageRequest", the low-priority (LP) resources are calculated according to the sum of the
	// high-priority (HP) pods' maximum of its usage and its request, so LP pod can allocate the resources both
	// unallocated and unused by the HP pods.
	// It is the conservative policy where the resources are NOT over-committed between priority bands while HP's usage
	// is also protected from the overcommitment.
	CalculateByPodMaxUsageRequest CalculatePolicy = "maxUsageRequest"
)

// +k8s:deepcopy-gen=true
type ColocationStrategyExtender struct {
	Extensions ExtraFields `json:"extensions,omitempty"`
}

// +k8s:deepcopy-gen=false
type ExtraFields map[string]interface{}

func (in *ExtraFields) DeepCopyInto(out *ExtraFields) {
	if in == nil {
		return
	} else {
		outIf := deepcopy.Copy(*in)
		*out = outIf.(ExtraFields)
	}
}

func (in *ExtraFields) DeepCopy() *ExtraFields {
	if in == nil {
		return nil
	}
	out := new(ExtraFields)
	in.DeepCopyInto(out)
	return out
}

// ColocationStrategy defines the strategy for node colocation.
// +k8s:deepcopy-gen=true
type ColocationStrategy struct {
	Enable                         *bool                                `json:"enable,omitempty"`
	MetricAggregateDurationSeconds *int64                               `json:"metricAggregateDurationSeconds,omitempty" validate:"omitempty,min=1"`
	MetricReportIntervalSeconds    *int64                               `json:"metricReportIntervalSeconds,omitempty" validate:"omitempty,min=1"`
	MetricAggregatePolicy          *slov1alpha1.AggregatePolicy         `json:"metricAggregatePolicy,omitempty"`
	MetricMemoryCollectPolicy      *slov1alpha1.NodeMemoryCollectPolicy `json:"metricMemoryCollectPolicy,omitempty"`

	CPUReclaimThresholdPercent *int64 `json:"cpuReclaimThresholdPercent,omitempty" validate:"omitempty,min=0"`
	// CPUCalculatePolicy determines the calculation policy of the CPU resources for the Batch pods.
	// Supported: "usage" (default), "maxUsageRequest".
	CPUCalculatePolicy            *CalculatePolicy `json:"cpuCalculatePolicy,omitempty"`
	MemoryReclaimThresholdPercent *int64           `json:"memoryReclaimThresholdPercent,omitempty" validate:"omitempty,min=0"`
	// MemoryCalculatePolicy determines the calculation policy of the memory resources for the Batch pods.
	// Supported: "usage" (default), "request", "maxUsageRequest".
	MemoryCalculatePolicy      *CalculatePolicy `json:"memoryCalculatePolicy,omitempty"`
	DegradeTimeMinutes         *int64           `json:"degradeTimeMinutes,omitempty" validate:"omitempty,min=1"`
	UpdateTimeThresholdSeconds *int64           `json:"updateTimeThresholdSeconds,omitempty" validate:"omitempty,min=1"`
	ResourceDiffThreshold      *float64         `json:"resourceDiffThreshold,omitempty" validate:"omitempty,gt=0,max=1"`

	// AllocatableCPU[Mid]' := min(Reclaimable[Mid], NodeAllocatable * MidCPUThresholdPercent) + Unallocated[Mid] * midUnallocatedRatio.
	MidCPUThresholdPercent *int64 `json:"midCPUThresholdPercent,omitempty" validate:"omitempty,min=0,max=100"`
	// AllocatableMemory[Mid]' := min(Reclaimable[Mid], NodeAllocatable * MidMemoryThresholdPercent) + Unallocated[Mid] * midUnallocatedRatio.
	MidMemoryThresholdPercent *int64 `json:"midMemoryThresholdPercent,omitempty" validate:"omitempty,min=0,max=100"`
	// MidUnallocatedPercent defines the percentage of unallocated resources in the Mid-tier allocable resources.
	// Allocatable[Mid]' := min(Reclaimable[Mid], NodeAllocatable * thresholdRatio) + Unallocated[Mid] * midUnallocatedRatio.
	MidUnallocatedPercent *int64 `json:"midUnallocatedPercent,omitempty" validate:"omitempty,min=0,max=100"`

	// when batchCPUThresholdPercent != nil, AllocatableCPU[Batch]' :=  min(Node.Total*BatchCPUThresholdPercent, Node.Total - Node.SafetyMargin - System.Reserved - sum(Pod(Prod/Mid).Request))
	// when batchCPUThresholdPercent == nil, AllocatableCPU[Batch]' :=  Node.Total - Node.SafetyMargin - System.Reserved - sum(Pod(Prod/Mid).Request)
	BatchCPUThresholdPercent *int64 `json:"batchCPUThresholdPercent,omitempty" validate:"omitempty,min=0"`
	// when batchMemoryThresholdPercent != nil, AllocatableMem[Batch]' :=  min(Node.Total*BatchMemoryThresholdPercent, Node.Total - Node.SafetyMargin - System.Reserved - sum(Pod(Prod/Mid).Request))
	// when batchMemoryThresholdPercent == nil, AllocatableCPU[Batch]' :=  Node.Total - Node.SafetyMargin - System.Reserved - sum(Pod(Prod/Mid).Request)
	BatchMemoryThresholdPercent *int64 `json:"batchMemoryThresholdPercent,omitempty" validate:"omitempty,min=0"`

	ColocationStrategyExtender `json:",inline"` // for third-party extension
}

// CPUNormalizationCfg is the cluster-level configuration of the CPU normalization strategy.
// +k8s:deepcopy-gen=true
type CPUNormalizationCfg struct {
	CPUNormalizationStrategy `json:",inline"`
	NodeConfigs              []NodeCPUNormalizationCfg `json:"nodeConfigs,omitempty" validate:"dive"`
}

// NodeCPUNormalizationCfg is the node-level configuration of the CPU normalization strategy.
// +k8s:deepcopy-gen=true
type NodeCPUNormalizationCfg struct {
	NodeCfgProfile `json:",inline"`
	CPUNormalizationStrategy
}

// CPUNormalizationStrategy is the CPU normalization strategy.
// +k8s:deepcopy-gen=true
type CPUNormalizationStrategy struct {
	// Enable defines whether the cpu normalization is enabled.
	// If set to false, the node cpu normalization ratio will be removed.
	Enable *bool `json:"enable,omitempty"`
	// DefaultRatio defines the default cpu normalization.
	DefaultRatio *float64 `json:"defaultRatio,omitempty"`
	// RatioModel defines the cpu normalization ratio of each CPU model.
	// It maps the CPUModel of BasicInfo into the ratios.
	RatioModel map[string]ModelRatioCfg `json:"ratioModel,omitempty"`
}

// ModelRatioCfg defines the cpu normalization ratio of a CPU model.
// +k8s:deepcopy-gen=true
type ModelRatioCfg struct {
	// BaseRatio defines the ratio of which the CPU neither enables Hyper Thread, nor the Turbo.
	BaseRatio *float64 `json:"baseRatio,omitempty"`
	// HyperThreadEnabledRatio defines the ratio of which the CPU enables the Hyper Thread.
	HyperThreadEnabledRatio *float64 `json:"hyperThreadEnabledRatio,omitempty"`
	// TurboEnabledRatio defines the ratio of which the CPU enables the Turbo.
	TurboEnabledRatio *float64 `json:"turboEnabledRatio,omitempty"`
	// HyperThreadTurboEnabledRatio defines the ratio of which the CPU enables the Hyper Thread and Turbo.
	HyperThreadTurboEnabledRatio *float64 `json:"hyperThreadTurboEnabledRatio,omitempty"`
}

// ResourceAmplificationCfg is the cluster-level configuration of the resource amplification strategy.
// +k8s:deepcopy-gen=true
type ResourceAmplificationCfg struct {
	ResourceAmplificationStrategy `json:",inline"`
	NodeConfigs                   []NodeResourceAmplificationCfg `json:"nodeConfigs,omitempty" validate:"dive"`
}

// NodeResourceAmplificationCfg is the node-level configuration of the resource amplification strategy.
// +k8s:deepcopy-gen=true
type NodeResourceAmplificationCfg struct {
	NodeCfgProfile `json:",inline"`
	ResourceAmplificationStrategy
}

// ResourceAmplificationStrategy is the resource amplification strategy.
// +k8s:deepcopy-gen=true
type ResourceAmplificationStrategy struct {
	// Enable defines whether the resource amplification strategy is enabled.
	Enable *bool `json:"enable,omitempty"`
	// ResourceAmplificationRatio defines resource amplification ratio
	ResourceAmplificationRatio map[corev1.ResourceName]float64 `json:"resourceAmplificationRatio,omitempty"`
}

/*
Koordinator uses configmap to manage the configuration of SLO, the configmap is stored in
 <ConfigNameSpace>/<SLOCtrlConfigMap>, with the following keys respectively:
   - <extension.ColocationConfigKey>
   - <ResourceThresholdConfigKey>
   - <ResourceQOSConfigKey>
   - <CPUBurstConfigKey>

et.

For example, the configmap is as follows:

```
apiVersion: v1
data:
  colocation-config: |
    {
      "enable": false,
      "metricAggregateDurationSeconds": 300,
      "metricReportIntervalSeconds": 60,
      "metricAggregatePolicy": {
        "durations": [
          "5m",
          "10m",
          "15m"
        ]
      },
      "metricMemoryCollectPolicy": "usageWithoutPageCache",
      "cpuReclaimThresholdPercent": 60,
      "memoryReclaimThresholdPercent": 65,
      "memoryCalculatePolicy": "usage",
      "degradeTimeMinutes": 15,
      "updateTimeThresholdSeconds": 300,
      "resourceDiffThreshold": 0.1,
      "nodeConfigs": [
        {
          "name": "alios",
          "nodeSelector": {
            "matchLabels": {
              "kubernetes.io/kernel": "alios"
            }
          },
          "updateTimeThresholdSeconds": 360,
          "resourceDiffThreshold": 0.2
        }
      ]
    }
  cpu-burst-config: |
    {
      "clusterStrategy": {
        "policy": "none",
        "cpuBurstPercent": 1000,
        "cfsQuotaBurstPercent": 300,
        "cfsQuotaBurstPeriodSeconds": -1,
        "sharePoolThresholdPercent": 50
      },
      "nodeStrategies": [
        {
          "name": "alios",
          "nodeSelector": {
            "matchLabels": {
              "kubernetes.io/kernel": "alios"
            }
          },
          "policy": "cfsQuotaBurstOnly",
          "cfsQuotaBurstPercent": 400
        }
      ]
    }
  system-config: |-
    {
      "clusterStrategy": {
        "minFreeKbytesFactor": 100,
        "watermarkScaleFactor": 150
      }
      "nodeStrategies": [
        {
          "name": "alios",
          "nodeSelector": {
            "matchLabels": {
              "kubernetes.io/kernel": "alios"
            }
          },
          "minFreeKbytesFactor": 100,
          "watermarkScaleFactor": 150
        }
      ]
    }
  resource-qos-config: |
    {
      "clusterStrategy": {
        "lsrClass": {
          "cpuQOS": {
            "enable": false,
            "groupIdentity": 2
          },
          "memoryQOS": {
            "enable": false,
            "minLimitPercent": 0,
            "lowLimitPercent": 0,
            "throttlingPercent": 0,
            "wmarkRatio": 95,
            "wmarkScalePermill": 20,
            "wmarkMinAdj": -25,
            "priorityEnable": 0,
            "priority": 0,
            "oomKillGroup": 0
          },
          "resctrlQOS": {
            "enable": false,
            "catRangeStartPercent": 0,
            "catRangeEndPercent": 100,
            "mbaPercent": 100
          }
        },
        "lsClass": {
          "cpuQOS": {
            "enable": false,
            "groupIdentity": 2
          },
          "memoryQOS": {
            "enable": false,
            "minLimitPercent": 0,
            "lowLimitPercent": 0,
            "throttlingPercent": 0,
            "wmarkRatio": 95,
            "wmarkScalePermill": 20,
            "wmarkMinAdj": -25,
            "priorityEnable": 0,
            "priority": 0,
            "oomKillGroup": 0
          },
          "resctrlQOS": {
            "enable": false,
            "catRangeStartPercent": 0,
            "catRangeEndPercent": 100,
            "mbaPercent": 100
          }
        },
        "beClass": {
          "cpuQOS": {
            "enable": false,
            "groupIdentity": -1
          },
          "memoryQOS": {
            "enable": false,
            "minLimitPercent": 0,
            "lowLimitPercent": 0,
            "throttlingPercent": 0,
            "wmarkRatio": 95,
            "wmarkScalePermill": 20,
            "wmarkMinAdj": 50,
            "priorityEnable": 0,
            "priority": 0,
            "oomKillGroup": 0
          },
          "blkioQOS": {
            "blocks": [
              {
                "ioCfg": {
                  "ioWeightPercent": 60
                },
                "name": "ackdistro-pool",
                "type": "volumegroup"
              },
              {
                "ioCfg": {
                  "readBPS": 102400000,
                  "readIOPS": 20480,
                  "writeBPS": 204800004,
                  "writeIOPS": 10240
                },
                "name": "/dev/sdb",
                "type": "device"
              },
            ],
            "enable": true
          },
          "resctrlQOS": {
            "enable": false,
            "catRangeStartPercent": 0,
            "catRangeEndPercent": 30,
            "mbaPercent": 100
          }
        },
        "cgroupRoot": {
          "blkioQOS": {
            "blocks": [
              {
                "ioCfg": {
                  "readLatency": 3000,
                  "writeLatency": 3000,
                  "readLatencyPercent": 95,
                  "writeLatencyPercent": 95
                },
                "name": "ackdistro-pool",
                "type": "volumegroup"
              }
            ],
            "enable": true
          }
        }
      },
      "nodeStrategies": [
        {
          "name": "alios",
          "nodeSelector": {
            "matchLabels": {
              "kubernetes.io/kernel": "alios"
            }
          },
          "beClass": {
            "memoryQOS": {
              "wmarkRatio": 90
            }
          }
        }
      ]
    }
  resource-threshold-config: |
    {
      "clusterStrategy": {
        "enable": false,
        "cpuSuppressThresholdPercent": 65,
        "cpuSuppressPolicy": "cpuset",
        "memoryEvictThresholdPercent": 70
      },
      "nodeStrategies": [
        {
          "name": "alios",
          "nodeSelector": {
            "matchLabels": {
              "kubernetes.io/kernel": "alios"
            }
          },
          "cpuEvictBEUsageThresholdPercent": 80
        }
      ]
    }
  host-application-config: |
    {
      "applications": [
        {
          "name": "nginx",
          "priority": "koord-prod",
          "qos": "LS",
          "cgroupPath": {
            "base": "CgroupRoot",
            "parentDir": "host-latency-sensitive/",
            "relativePath": "nginx/",
          }
        }
      ],
      "nodeConfigs": [
        {
          "name": "colocation-pool",
          "nodeSelector": {
            "matchLabels": {
              "node-pool": "colocation"
            }
          },
          "applications": [
            {
              "name": "nginx",
              "priority": "koord-prod",
              "qos": "LS",
              "cgroupPath": {
                "base": "CgroupRoot",
                "parentDir": "host-latency-sensitive/",
                "relativePath": "nginx/",
              }
            }
          ]
        }
      ]
    }
  cpu-normalization-config: |
    {
      "enable": false,
      "ratioModel": {
        "Intel(R) Xeon(R) Platinum XXX CPU @ 2.50GHz": {
          "baseRatio": 1.5,
          "hyperThreadEnabledRatio": 1.0,
          "turboEnabledRatio": 1.8,
          "hyperThreadTurboEnabledRatio": 1.2
        },
        "Intel(R) Xeon(R) Platinum YYY CPU @ 2.50GHz": {
          "baseRatio": 1.8,
          "hyperThreadEnabledRatio": 1.2,
          "turboEnabledRatio": 2.16,
          "hyperThreadTurboEnabledRatio": 1.44
        }
      },
      "nodeConfigs": [
        {
          "name": "test",
          "nodeSelector": {
            "matchLabels": {
              "AAA": "BBB"
            }
          },
          "enable": true
        }
      ]
    }
kind: ConfigMap
metadata:
  annotations:
    meta.helm.sh/release-name: koordinator
    meta.helm.sh/release-namespace: default
  labels:
    app.kubernetes.io/managed-by: Helm
  name: slo-controller-config
  namespace: koordinator-system
```
*/
