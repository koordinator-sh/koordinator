---
title: Heterogeneous GPU device reporting
authors:
  - "@ZhuZhezz"
reviewers:
  - "@zqzten"
  - "@ZiMengSheng"
  - "@hormes"
creation-date: 2025-04-25
last-updated: 2025-04-25
status: provisional
---

# Heterogeneous GPU device reporting

## Summary

This proposal introduces a heterogeneous GPU device reporting mechanism to koordlet, particularly for GPU devices with dynamic virtualization. The main goal is to decouple device awareness from Koordlet, maitain a clean Koordlet architecture and facilitate integration for third-party vendors.

## Motivation
For now, Koordlet is able to report NVIDIA GPU from NVML. But for third-party GPU devices, there is no standard way. If third-party GPU devices are integrated into Koordlet, it will introduce vendor-specific logic and SDK, such as DCML(Ascend Card) and IXML(Iluvatar Card). Meanwhile, we need to address the cgo dependencies issue. This will make Koordlet hard to maintain and more complex.

This PR proposes a mechanism to report devices, and makes Koordlet more extensible and maintainable.

### Goals

- Introduce a mechanism for reporting heterogeneous GPU devices.
- Allow Koordlet integrates third-party GPU devices quickly.
- Keep Koordlet architecture clean.

### Non-Goals
- Currently, unified solutions for device allocation on Node side would not be provided. Instead, existing vendor-specific device plugins would be reused and adaption for different device plugins would be done in scheduler to simplify the process of integration and usage.

## Proposal

### Architecture
![image](/docs/images/GPU-device-reporting.svg)

### Implementation Details

- Thirty-party vendors write [device infos](#Device-Infos-Formate-In-File) into directory `/etc/kubernetes/koordlet/device-infos/`. Examples:[Device Infos Exampels](#Device-Infos-Examples).
- Koordlet will read the device infos from the directory and report them to apiserver.
  - The topology infos from directory will be synced to the annotation of the Device.
  - The status from directory will be synced to the "Healthy" condition of the Device.
  - The vendor and model from directory will be synced to the labels of the Device.
```yaml
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: Device
metadata:
  labels:
    # Existing label
    node.koordinator.sh/gpu-partition-policy: "Honor"
    node.koordinator.sh/gpu-model: Tesla-T4 # Ascend-910B/Ascend-310P3
    # New label
    node.koordinator.sh/gpu-vendor: nvidia # huawei/cambricon/
  annotations:
    # The topology from device infos will be synced to the annotation of the Device.
    scheduling.koordinator.sh/gpu-partitions: |
      {
        "1": [
                {
                  "minors": [
                      0
                  ],
                  "gpuLinkType": "NVLink",
                  "ringBusBandwidth": "400Gi",
                  "allocationScore": "1"
                }
        ],
        "4": [
                {
                    "minors": [
                       0,1,2,3 
                    ],
                    # This is the topology of the Ascend910B.
                    # https://www.hiascend.com/doc_center/source/zh/mindx-dl/50rc1/ref/dl_affinity_0002.html
                    "gpuLinkType": "HCCS",
                    "allocationScore": "1"
                }
        ]
      }
  name: node-1
spec:
  devices:
  - health: true
    id: GPU-fd971b33-4891-fd2e-ed42-ce6adf324615
    minor: 0
    resources:
      koordinator.sh/gpu-core: "100"
      koordinator.sh/gpu-memory: 15Gi
      koordinator.sh/gpu-memory-ratio: "100"
    topology:
      busID: 0000:3b:00.0
      nodeID: 0
      pcieID: pci0000:3a
      socketID: -1
    type: gpu
    # Add Healthy condition
    conditions:
    - lastTransitionTime: "2025-04-25T10:00:00Z"
      status: "True"
      type: Healthy
  - health: true
    id: GPU-0ca35a5f-551e-91e4-f3a3-c9b103afe1db
    minor: 1
    resources:
      koordinator.sh/gpu-core: "100"
      koordinator.sh/gpu-memory: 15Gi
      koordinator.sh/gpu-memory-ratio: "100"
    topology:
      busID: 0000:3b:01.0
      nodeID: 0
      pcieID: pci0000:3a
      socketID: -1
    type: gpu
    # Add Healthy condition
    conditions:
    - lastTransitionTime: "2025-04-25T10:00:00Z"
      message: "XID 74: Nvlink Error"
      reason: "NvlinkError"
      status: "False"
      type: Healthy

```

- Add the featuregate to enable the heterogeneous GPU device reporting. It is not recommended to enable both feature gates at the same time.
```GO
// Accelerators enables GPU related feature in koordlet. Only Nvidia GPUs supported.
    Accelerators featuregate.Feature = "Accelerator"
// XAccelerators enables heterogeneous GPU devices reporting.
    Accelerators featuregate.Feature = "XAccelerators"
``` 

### Device Infos Formate In File
```Go
type DeviceInfo struct {
    Vendor string `json:"vendor"`
    Model string `json:"model"` 
    UUID string `json:"uuid"`//the Identifier of the device
    Minor string `json:"minor"`// /dev/xxx0
    Resources map[string]string `json:"resourece,omitempty"`
    Topology *DeviceTopology `json:"topology,omitempty"`
    Status *DeviceStatus `json:"status,omitempty"`
}

type DeviceTopology struct {
    P2PLinks []DeviceP2PLink `json:"p2pLinks,omitempty"`
}

type DeviceP2PLink struct {
	PeerMinor  string `json:"peerMinor"`
	Type DeviceP2PLinkType `json:"type"`
}

type DeviceP2PLinkType string //like nvlink/hccs

type DeviceStatus {
    Healthy bool `json:"healthy"`
    ErrCode string `json:"errCode,omitempty"`
    ErrMessage string `json:"errMessage,omitempty"`
}
```
### Device Infos Examples
- NVIDIA: Tesla-T4
```json
{
    "vendor": "nvidia",
    "model": "Tesla-T4",
    "uuid": "GPU-0ca35a5f-551e-91e4-f3a3-c9b103afe1db",
    "minor": "1",
    "resources": {
        "koordinator.sh/gpu-memory": "15Gi",
        "koordinator.sh/gpu-core": "100"
    },
    "topology": {
        "p2pLinks": [
            {
                "peerMinor": "1,2,3",
                "type": "NVLink"
            }
        ]
    },
    "status": {
        "healthy": false,
        "errCode": "XID 74",
        "errMessage": "Nvlink Error"
    }
}
```

- HUAWEI: Ascend-910B
```json
{
    "vendor": "huawei",
    "model": "Ascend-910B",
    // vdieId
    "uuid": "185011D4-21104518-A0C4ED94-14CC040A-56102003",
    "minor": "0",
// https://www.hiascend.com/document/detail/zh/mindx-dl/600/clusterscheduling/clusterscheduling/cpaug/mxdlug_021.html
    "resources": {
        "huawei.com/npu-core": "32",
        "hauwei.com/npu-cpu": "14",
        "koordinator.sh/gpu-memory": "32Gi",
        "huawei.com/npu-vpc": "16",
        "huawei.com/npu-vdec": "16",
        "huawei.com/npu-jpegd": "16",
        "huawei.com/npu-pngd": "24",
        "huawei.com/npu-jpege": "8"
    },
    "topology": {
        "p2pLinks": [
            {
                "peerMinor": "1,2,3",
                "type": "HCCS"
            }
        ]
    },
    "status": {
        "healthy": true
    }
}
```

