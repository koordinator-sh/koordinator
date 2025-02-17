---
title: Pod Resources Proxy
authors:
- "@ZiMengSheng"
- "@ferris-cx"
reviewers:
- "@hormes"
- "@saintube"
creation-date: 2024-12-10
last-updated: 2025-02-17
---
# Pod Resources Proxy 

## Motivation
In the Kubernetes community, devices are allocated by Kubelet, but the monitoring of these devices and how to make them visible within containers is left to the device vendors to customize.
For example, the monitoring of GPUs is managed by components such as NVIDIA's DCGM Exporter, while inserting network interfaces into a Pod's net namespace is handled by components like Multus-CNI.

The question is: how can components like Multus-CNI or DCGM Exporter know which devices Kubelet has allocated to Pods? This requires the device allocator to provide some interfaces to expose this information.

Fortunately, The kubelet already provides the pod-resources endpoint, which allows third-party consumers to inspect the mapping between devices and pods. 
This interface has been adopted by Multus-CNI and DCGM Exporter to obtain the network cards and GPUs allocated to pods. 

However, Koordinator uses a centralized scheduler to allocate devices, and the kubelet does not have device allocation information, so adaptation is required.

### Goals

1. Provides an interface that is exactly the same as the Kubelet PodResources interface, but with a different socket address. 
This allows third-party consumers to query the device allocation for Pods in Koordinator without modifying any code.
2. Provides the modifications that Multus and DCGM need to make in their deployment YAML files to obtain device allocation information for Pods in Koordinator.

## Proposal

### User Stories

#### Story 1

As a user, when using Koordinator to allocate RDMA NICs for Pods, I can simply modify the deployment file of Multus-CNI as follows:

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kube-multus-ds
  namespace: kube-system
  labels:
    tier: node
    app: multus
    name: multus
spec:
  selector:
    matchLabels:
      name: multus
  updateStrategy:
    type: RollingUpdate
  template:
    metadata:
      labels:
        tier: node
        app: multus
        name: multus
    spec:
      containers:
        - name: kube-multus
          volumeMounts:
            ...
            - name: host-var-lib-kubelet
              mountPath: /var/lib/kubelet/pod-resources
              mountPropagation: HostToContainer
            ...
      volumes:
        ...
        - name: host-var-lib-kubelet
          hostPath:
            path: /var/run/koordlet/pod-resources
        ...
```

#### Story 2
As a user, I have adopted Koordinator to allocate GPUs for Pods. When I want to monitor the GPUs using DCGM, I can simply modify the DCGM deployment YAML file as follows:

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: dcgm-exporter
  namespace: dcgm-namespace
  labels:
    app.kubernetes.io/component: "dcgm-exporter"
spec:
  updateStrategy:
    type: RollingUpdate
  selector:
    matchLabels:
      app.kubernetes.io/component: "dcgm-exporter"
  template:
    metadata:
      labels:
        app.kubernetes.io/component: "dcgm-exporter"
    spec:
      volumes:
      - name: "pod-gpu-resources"
        hostPath:
          path: "/var/lib/kubelet/pod-resources"
      containers:
      - name: exporter
        volumeMounts:
        - name: "pod-gpu-resources"
          readOnly: true
          mountPath: "/var/lib/kubelet/pod-resources"
```

### Design

In Koordinator, the device information allocated to Pods is included in the annotations of the Pods. Therefore, we can create a proxy at the node level that will:

1. Access the Kubelet's PodResources interface to obtain the raw results.
2. Access the Kubelet's /pods interface to retrieve the annotation information for all Pods on the node.
3. Parse step 2 to extract the device information allocated to the Pods, fill this information into the results obtained in step 1, and return the result to the caller.

We implement this proxy in the statesInformer module of Koordlet.

## Alternatives

### Modifying Multus-CNI or DCGM Code
This approach is considered quite invasive for Multus or DCGM, requiring Koordinator to fork the corresponding repository code, resulting in high maintenance costs.
### Obtaining Device Information Allocated to Pods Through the NRI Interface
The NRI's RunPodSandbox interface call occurs after the CNI interface. Therefore, using this method, Multus-CNI cannot obtain device allocation information at the time the CNI Add is called.
### Using DRA Allocation Logic
Currently, DRA does not support simply using extended resources to request resources. Additionally, on the node side, Multus-CNI or DCGM needs to be aware of the relevant allocation information of Resource Claims.