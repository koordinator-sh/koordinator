---
title: Support Netqos
authors:
  - "@lucming"
reviewers:
  - "@zwzhang0107"
  - "@hormes"
  - "@eahydra"
  - "@FillZpp"
  - "@jasonliu747"
  - "@ZiMengSheng"
  - "@l1b0k"
creation-date: 2023-12-08
last-updated: 2023-12-08
---
# Support Netqos

## Table of Contents

<!--ts-->
- [Support Netqos](#support-netqos)
  - [Table of Contents](#table-of-contents)
  - [Glossary](#glossary)
  - [Summary](#summary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [User Stories](#user-stories)
    - [Story 1](#story-1)
    - [Story 2](#story-2)
    - [Story 3](#story-3)
  - [Design](#design)
    - [Design Principles](#design-principles)
    - [Implementation Details](#implementation-details)
      - [koordlet:](#koordlet)
        - [node level：](#node-level)
        - [pod level：](#pod-level)
      - [koord-scheduler](#koord-scheduler)
      - [koord-descheduler](#koord-descheduler)
    - [usage:](#usage)
  - [Implementation History](#implementation-history)
<!--te-->

## Glossary  

[ebpf](https://ebpf.io/what-is-ebpf/)  

[ebpf tc](https://arthurchiao.art/blog/cilium-bpf-xdp-reference-guide-zh/#prog_type_tc)  

[edt](https://arthurchiao.art/blog/better-bandwidth-management-with-ebpf-zh/#31-%E6%95%B4%E4%BD%93%E8%AE%BE%E8%AE%A1%E5%9F%BA%E4%BA%8E-bpfedt-%E5%AE%9E%E7%8E%B0%E5%AE%B9%E5%99%A8%E9%99%90%E9%80%9F)  

[terway-qos](https://github.com/AliyunContainerService/terway-qos/blob/main/README-zh_CN.md)  

## Summary

*netqos* is designed to resolve container network bandwidth contention problem in the mixed section scenarios.
It supports limiting bandwidth by single pod and by priority on node level. Aims to improve the QOS(quantity of service).

## Motivation

Currently, network bandwidth has not been taken into account in `koordinator`, 
and there may be certain pitfalls, such as:
1. Low network bandwidth utilisation; 
2. Uneven distribution of network bandwidth load on cluster nodes;
3. The QOS of high-priority processes cannot be guaranteed on a single machine.  

This pr is mainly designed to solve the node-side container network bandwidth preemption problem in the mixed section scenario.

### Goals

- Limits the amount of network bandwidth that pod can be used. Includes ingress and egress.
- On the same node, multiple containers can use and seize network bandwidth, design guideline: when the network bandwidth load is low, offline containers try to use all the bandwidth, when the network bandwidth is high, online containers give priority to use the network bandwidth.
- we defined an API/Config for network qos, which can work with external plugins, such as `terway-qos` or built-in plugins (implemented by tc in the future)

### Non-Goals/Future Work

- Proposes a netqos implementation based on `terway-qos`;
- Schedule/deschedule based on Network bandwidth on k8s cluster;
- Suppression/eviction based on network bandwidth on node;
- Observability: add some metrics for the network qos plugin itself.

## User Stories

### Story 1

As a cluster manager, it is hoped that the network bandwidth distribution of the entire cluster is more balanced, 
avoiding some nodes with too high network loads, which leads to resource preemption and affects the QOS of containers.

### Story 2

As a cluster manager, I would like to improve the node resource utilisation in a k8s cluster by deploying both online 
and offline services on the nodes. When network resources are idle, offline services can use node bandwidth as much as possible. 
When network resources are scrambled, you can give priority to guaranteeing network resources for online services, 
while also taking care that offline services are not starved to death.

### Story 3

When node containers experience severe bandwidth contention, administrators can temporarily adjust individual 
pod bandwidth limits without rebuilding the pod.

## Design

### Design Principles

- The netqos implementation should be scalable, `terway-qos` will be adapted first, still need to be compatible with other netqos solutions, just like TC.
- The netqos feature should be pluggable, and user can configure whether to enable the netqos feature or not.

### Implementation Details

#### koordlet:

##### node level：
`terway-qos` designed three priorities on the node, it been used to limit and ensure containers with different priority can use the network bandwidth, 
`koordinator` and `terway-qos` need to interact with a configuration file path in `/var/run/koordinator/net/node`, and the file content as follows:
```yaml
{
  "hw_tx_bps_max": 0,
  "hw_rx_bps_max": 0,
  "l1_tx_bps_min": 0,
  "l1_tx_bps_max": 0,
  "l2_tx_bps_min": 0,
  "l2_tx_bps_max": 0,
  "l1_rx_bps_min": 0,
  "l1_rx_bps_max": 0,
  "l2_rx_bps_min": 0,
  "l2_rx_bps_max": 0
}
```
and api in koordinator just like:
```go
type GlobalConfig struct {
	HwTxBpsMax int `json:"hw_tx_bps_max"`
	HwRxBpsMax int `json:"hw_rx_bps_max"`
	L1TxBpsMin int `json:"l1_tx_bps_min"`
	L1TxBpsMax int `json:"l1_tx_bps_max"`
	L2TxBpsMax int `json:"l2_tx_bps_max"`
	L1RxBpsMin int `json:"l1_rx_bps_min"`
	L1RxBpsMax int `json:"l1_rx_bps_max"`
	L2RxBpsMin int `json:"l2_rx_bps_min"`
	L2RxBpsMax int `json:"l2_rx_bps_max"`
}
```
In the config file above, the unit of each field is `bps`(byte per second), there are three priorities l0,l1,l2,
the higher the number the lower the priority, default is l0.
The largest value of l0 is the overall network bandwidth, `l0.min=total-l1.min-l2.min`, l1,l2 cannot over their network bandwidth limits.
when the load is high, priority is given to ensure that high-priority(`l0`) containers get network bandwidth first.
When the load is low, the network bandwidth for low-priority(`l2`) containers is accommodated as much as possible.

we will consider expanding the fields of `nodeslo` to add new parameters related to network bandwidth as follows:
```go
type NodeSLOSpec struct {
	// QoS config strategy for pods of different qos-class
	ResourceQOSStrategy *ResourceQOSStrategy `json:"resourceQOSStrategy,omitempty"`
	//node global system config
	SystemStrategy *SystemStrategy `json:"systemStrategy,omitempty"`
}

type ResourceQOSStrategy struct {
	// Policies of pod QoS.
	Policies *ResourceQOSPolicies `json:"policies,omitempty"`

	// ResourceQOS for LSR pods.
	LSRClass *ResourceQOS `json:"lsrClass,omitempty"`

	// ResourceQOS for LS pods.
	LSClass *ResourceQOS `json:"lsClass,omitempty"`

	// ResourceQOS for BE pods.
	BEClass *ResourceQOS `json:"beClass,omitempty"`

	// ResourceQOS for system pods
	SystemClass *ResourceQOS `json:"systemClass,omitempty"`

	// ResourceQOS for root cgroup.
	CgroupRoot *ResourceQOS `json:"cgroupRoot,omitempty"`
}

type ResourceQOS struct {
	...
	NetworkQOS *NetworkQOSCfg `json:"networkQOS,omitempty"`
}

type NetworkQOSCfg struct {
	Enable     *bool `json:"enable,omitempty"`
	NetworkQOS `json:",inline"`
}

type NetworkQOS struct {
	// IngressRequest describes the minimum network bandwidth guaranteed in the ingress direction.
	// unit: bps(bytes per second), two expressions are supported，int and string,
	// int: percentage based on total bandwidth，valid in 0-100
	// string: a specific network bandwidth value, eg: 50M.
	// +kubebuilder:default=0
	IngressRequest *intstr.IntOrString `json:"ingressRequest,omitempty"`
	// IngressLimit describes the maximum network bandwidth can be used in the ingress direction,
	// unit: bps(bytes per second), two expressions are supported，int and string,
	// int: percentage based on total bandwidth，valid in 0-100
	// string: a specific network bandwidth value, eg: 50M.
	// +kubebuilder:default=100
	IngressLimit *intstr.IntOrString `json:"ingressLimit,omitempty"`

	// EgressRequest describes the minimum network bandwidth guaranteed in the egress direction.
	// unit: bps(bytes per second), two expressions are supported，int and string,
	// int: percentage based on total bandwidth，valid in 0-100
	// string: a specific network bandwidth value, eg: 50M.
	// +kubebuilder:default=0
	EgressRequest *intstr.IntOrString `json:"egressRequest,omitempty"`
	// EgressLimit describes the maximum network bandwidth can be used in the egress direction,
	// unit: bps(bytes per second), two expressions are supported，int and string,
	// int: percentage based on total bandwidth，valid in 0-100
	// string: a specific network bandwidth value, eg: 50M.
	// +kubebuilder:default=100
	EgressLimit *intstr.IntOrString `json:"egressLimit,omitempty"`
}

type SystemStrategy struct {
	...
	// TotalNetworkBandwidth indicates the overall network bandwidth, cluster manager can set this field via "slo-controller-config" configmap, 
	// and default value just taken from /sys/class/net/${NIC_NAME}/speed, unit: Mbps
	TotalNetworkBandwidth resource.Quantity `json:"totalNetworkBandwidth,omitempty"`
}
```

##### pod level：
To enable fine-grained network bandwidth control for containers in a pod, `koordinator` will sync configuration file 
content to `/var/run/koordinator/net/pods`. and then `terway-qos` or other netqos plugin(eg: tc) will do something to 
limit the net bandwidth that container can use. content as follows:
```yaml
{
  "cgroup":"/sys/fs/cgroup/xxxx",
  "priority":0,
  "pod": "namespacedname",
  "podUID":"xxx",
  # todo: network bandwidth limiting and preemption based on port/dscp
  "qos-config": {}
}
```

TODO: The `qos-config` is used to define the configuration for network bandwidth limitation and preemption based on port/dscp, which may look like this:
```yaml
{
    "ingress": [
        {
            "matchs": [{
                "type": "ip"
            }],
            "actions": [
                {
                    "action": "qos-class",
                    "value": "l1",
                }
            ],
        }
    ],
    "egress": [
         {
            "matchs": [{
                "type": "port",
                "expr": "=80"
            }],
            "actions": [
                {
                    "action": "qos-class",
                    "value": "l1",
                },
                {
                    "action": "dscp",
                    "value": "",
                },
                {
                    "action": "bandwidth_min",
                    "value": "1000",
                },
                {
                    "action": "bandwidth_max",
                    "value": "1000",
                }
            ],
        }
    ]
}
```

We will declare the pod-level netqos configuration via pod.annotation["koordinator.sh/networkQOS"] with the following api definition:
```go
type PodNetworkQOS struct {
	NetworkQOS
	QoSClass        extension.QoSClass // BE/LS/LSR
	// todo: network bandwidth limiting & preemption based on container port
	// PortsNetwrokQOS []PortNetworkQOS
}

// todo: netqos api based on cotainer port.
type PortNetworkQOS struct {
	NetworkQOS
	Port     int
	QoSClass extension.QoSClass // BE/LS/LSR
}
```


#### koord-scheduler
A `NetBandwidth` scheduler plugin needs to be added to score the node according to the node network bandwidth load.
The higher the node network bandwidth load, the lower the score, so as to ensure that the newly created pod can be scheduled
to a node with relatively idle network bandwidth.
```
score = (node.capacity.netbandwidth - node.netbandwidth.used) * int64(framework.MaxNodeScore)) / node.capacity.netbandwidth
```

#### koord-descheduler
 The `LowNodeLoad` rescheduler plugin needs to take into account the actual load on the node's network bandwidth when balancing. 
 We need to add the `netBandwidth` threshold to the parameters of the `LowNodeLoad` plugin in koord-descheduler-config.yaml.
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: descheduler-config
  namespace: system
data:
  koord-descheduler-config: |
    ...
      - name: LowNodeLoad
        args:
          ...
          lowThresholds:
            netBandwidth: **
          highThresholds:
            netBandwidth: **
```

### usage:
Cluster administrators can configure the [slo-controller-config.yaml](https://github.com/koordinator-sh/koordinator/blob/main/config/manager/slo-controller-config.yaml)
to Configure cluster or node level network bandwidth, if the node bandwidth is not configured, the network bandwidth reported by the koordlet will be used,
The default network bandwidth request percentage for each level is `l0:l1:l2=40%:30%:30%`, limit all 100% by default, administrators can configure it by themselves.
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: slo-controller-config
  namespace: kube-system
data:
  colocation-config: |
    {
      "enable": true
    }
  resource-threshold-config: |
    {
      "clusterStrategy": {
        "enable": true
      }
    }
  resource-qos-config: |
    {
      "clusterStrategy": {
        "lsrClass": {
          "networkQOS": {
            "enable": true,
            "ingressRequest": 40,
            "ingressLimit": 100,
            "egressRequest": 40,
            "egressLimit": 100
          },
        },
        "lsClass": {
          "networkQOS": {
            "enable": true,
            "ingressRequest": 40,
            "ingressLimit": 100,
            "egressRequest": 40,
            "egressLimit": 100
          },
        },
        "beClass": {
          "networkQOS": {
            "enable": true,
            "ingressRequest": 30,
            "ingressLimit": 100,
            "egressRequest": 30,
            "egressLimit": 100
          },
        }
      },
      system-config: |-
        {
          "clusterStrategy": {
            "totalNetworkBandwidth": 1000M
          }
        }
    }
```

## Implementation History

- [ ] 12/08/2023: Open PR for initial draft
