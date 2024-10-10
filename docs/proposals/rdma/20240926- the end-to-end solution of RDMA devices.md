

# Supports the end-to-end solution of RDMA devices

## Summary

Currently, only CPU, memory, and GPU resource allocation is supported, and RDMA network device scheduling is not supported. The purpose of this proposal is to expand the end-side capabilities of RDMA devices, improve the scheduling and distribution capabilities of RDMA devices, and enhance the ability of container pass-through RDMA#PF or VF(SR-IOV) devices.

## Motivation

This involves RDMA device registration, resource reporting, updating node status, scheduling and allocation, annotating Pods, and modifying multi-NIC CNI plug-ins. The middle side does not rely on device-plugin, but continues the existing koordlet component. koordinator's scheduling component already supports RDMA and joint scheduling of RDMA and GPU, but the allocation result does not have the BDF information of PF, resulting in the failure to allocate PF to the container. In order to improve the end-to-end capability of RDMA equipment, it is necessary to put forward a set of solutions to realize the above functions and complete the whole process of RDMA equipment.

### Goals

1. Realize RDMA network device registration, including PF and VF. Add the PF/VF topology information of the RDMA device in DeviceCRD;
2. Update the number of RDMA devices to the status field of the cluster node;
3. Add the BDF address information for PF to the device assignment result on the Pod annotations;
4. Modify the mutlus-cni plug-in source code, parse the DeviceId in the Pod annotation and inject it into the container;
5. according to the label of the RDMA device, the scheduler is limited to complete the PF/VF scheduling and allocation in this range, such as: a GPU server has RDMA1 for GPUs, and RDMA2 for storage, Pod1 applies for GPU and RDMA device (PF), Pod2 applies for VF of RDMA2.

### Non-Goals/Future Work

1. None

## User stories

### Story 1

As a user, I want to complete the joint scheduling of GPU and RDMA devices, and successfully mount the PF device inside the container and start it successfully.


### Story 2

As a user of a NIC device with the SR-IOV function, I want to request a certain number of VFs that meet the topology affinity, pass through the container, and let the container use VF to send and receive network traffic.

## Proposal

### Expand koordlet's ability to discover and register NIC devices

Develop a device plug-in called RDMA that implements the DeviceCollector interface for collecting rdma device information.   

```go
package rdma

const (
   DeviceCollectorName = "RDMA"
)

type rdmaCollector struct {
   enabled bool
}

func New(opt *framework.Options) framework.DeviceCollector {
	return &rdmaCollector{
		enabled: features.DefaultKoordletFeatureGate.Enabled(features.NetDevices),
	}
}
......
func (g *rdmaCollector) Infos() metriccache.Devices {
	netDevices, err := GetNetDevice()
	if err != nil {
		klog.Errorf("failed to get net device: %v", err)
	}
	return netDevices
}
```

Register the RDMA device collector plug-in with the metricsadvisor in the code file plugins_profile.go.

```go
var (
	devicePlugins = map[string]framework.DeviceFactory{
		gpu.DeviceCollectorName:  gpu.New,
		rdma.DeviceCollectorName: rdma.New,
	}
    ......
)
```

#### Extend the rdma controller plug-in to update the node status

Since the framework does not support the updating of rdma device resources on nodes, it is necessary to develop an rdma controller plug-in based on the existing framework. The plug-in is named RDMADeviceResource and updates node.status in time and the resource name is koordinator.sh/rdma. Currently, only the number of RDMA devices can be counted, including PF or VF. 

If the network adapter on the Node is in VF mode, the number of VF devices is counted. Otherwise, the number of PF devices is counted, and the node is updated to Node.status.This plug-in named RDMADeviceResource needs to implement the following interfaces: SetupPlugin,NodePreparePlugin, ResourceCalculatePlugin, NodeStatusCheckPlugin and ResourceResetPlugin.

```
const PluginName = "RDMADeviceResource"
......
func (p *Plugin) Name() string {
	return PluginName
}

func (p *Plugin) Setup(opt *framework.Option) error {
	client = opt.Client

	opt.Builder = opt.Builder.Watches(&schedulingv1alpha1.Device{}, &RDMADeviceHandler{})

	return nil
}

func (p *Plugin) NeedSync(strategy *configuration.ColocationStrategy, oldNode, newNode *corev1.Node) (bool, string) {
	......
}
func (p *Plugin) Prepare(_ *configuration.ColocationStrategy, node *corev1.Node, nr *framework.NodeResource) error {
	......
}

func (p *Plugin) Reset(node *corev1.Node, message string) []framework.ResourceItem {
	......
}

func (p *Plugin) Calculate(_ *configuration.ColocationStrategy, node *corev1.Node, _ *corev1.PodList, _ *framework.ResourceMetrics) ([]framework.ResourceItem, error) {
	......
}

```

The final number of rdma devices is written to the node state, for example:

```yaml
Capacity:
  cpu:                               64
  hugepages-1Gi:                     0
  hugepages-2Mi:                     0
  koordinator.sh/gpu:                400
  koordinator.sh/gpu-core:           400
  koordinator.sh/gpu-memory:         90Gi
  koordinator.sh/gpu-memory-ratio:   400
  koordinator.sh/gpu.shared:         400
  koordinator.sh/rdma:               4
```

The node in this example contains four network card devices. However, all the four nics are either PF or VF. 

### The scheduler schedules Pods which request RMDA devices and add BDF to the annotation

When the Pod applies for RMDA devices, koord-scheduler completes the scheduling, calculates the PF/VF assignment result, and writes the assignment result to the Pod annotation.The current framework already supports the scheduling, allocation rdma devices. The assignment results on pod annotations are shown here, for example:

```yaml
scheduling.koordinator.sh/device-allocated: '{rdma":[{"minor":0,"resources":{"koordinator.sh/rdma":"1"},"extension":{"vfs":[{"minor":-1,"busID":"0000:01:00.2"}]}}]}'
```

In this example, a device whose BDF is 0000:01:00.2 is a VF.

The framework does not support the BDF address record of PF, so the field busID is added this time. The PF‘s BDF address is added by extending the BusID field to the DeviceAllocation data structure. The field value is filled in the preBind phase.

```go
type DeviceAllocation struct {
	Minor     int32                      `json:"minor"`
	Resources corev1.ResourceList        `json:"resources"`
	Extension *DeviceAllocationExtension `json:"extension,omitempty"`
	BusID     string 					 `json:"busID,omitempty"`
}
```

For comparison, the new allocation results are annotated as follows:

```yaml
scheduling.koordinator.sh/device-allocated: '{rdma":[{"minor":0,"resources":{"koordinator.sh/rdma":"1"},"extension":{"vfs":[{"minor":-1,"busID":"0000:01:00.2"}]},"busID":"0000:01:00.0"}]}'
```

In this example, a device whose BDF is 0000:01:00.2 is a VF, and a device whose BDF is 0000:01:00.0 is a PF.

### Multus-cni

After Pod is scheduled to a certain node and binding is completed, node kubelet will call multus-cni plug-in to set up the network. At this time, cni can obtain the NIC device information and the network interface will be injected into the network namespace of the Pod.

### PS

*Since Multus-cni is a third-party CNI plugin, it is not part of Project koordinator. Proposals will then be added to discuss technical solutions.*

​	