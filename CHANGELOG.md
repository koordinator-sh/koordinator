# Change Log

## v0.2.0

## Isolate resources for best-effort workloads

In Koodinator v0.2.0, we refined the ability to isolate resources for best-effort worklods.

`koordlet` will set the cgroup parameters according to the resources described in the Pod Spec. Currently supports setting CPU Request/Limit, and Memory Limit.

For CPU resources, only the case of `request == limit` is supported, and the support for the scenario of `request <= limit` will be supported in the next version.

## Active eviction mechanism based on memory safety thresholds

When latency-sensitiv applications are serving, memory usage may increase due to bursty traffic. Similarly, there may be similar scenarios for best-effort workloads, for example, the current computing load exceeds the expected resource Request/Limit.

These scenarios will lead to an increase in the overall memory usage of the node, which will have an unpredictable impact on the runtime stability of the node side. For example, it can reduce the quality of service of latency-sensitiv applications or even become unavailable. Especially in a co-location environment, it is more challenging.

We implemented an active eviction mechanism based on memory safety thresholds in Koodinator.

`koordlet` will regularly check the recent memory usage of node and Pods to check whether the safty threshold is exceeded. If it exceeds, it will evict some best-effort Pods to release memory. This mechanism can better ensure the stability of node and latency-sensitiv applications.

`koordlet` currently only evicts best-effort Pods, sorted according to the Priority specified in the Pod Spec. The lower the priority, the higher the priority to be evicted, the same priority will be sorted according to the memory usage rate (RSS), the higher the memory usage, the higher the priority to be evicted. This eviction selection algorithm is not static. More dimensions will be considered in the future, and more refined implementations will be implemented for more scenarios to achieve more reasonable evictions.

The current memory utilization safety threshold default value is 70%. You can modify the `memoryEvictThresholdPercent` in ConfigMap `slo-controller-config` according to the actual situation,

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: slo-controller-config
  namespace: koordinator-system
data:
  colocation-config: |
    {
      "enable": true
    }
  resource-threshold-config: |
    {
      "clusterStrategy": {
        "enable": true,
        "memoryEvictThresholdPercent": 70
      }
    }
```

## v0.1.0

### Node Metrics 

Koordinator defines the `NodeMetrics` CRD, which is used to record the resource utilization of a single node and all Pods on the node. koordlet will regularly report and update `NodeMetrics`. You can view `NodeMetrics` with the following commands.

```shell
$ kubectl get nodemetrics node-1 -o yaml
apiVersion: slo.koordinator.sh/v1alpha1
kind: NodeMetric
metadata:
  creationTimestamp: "2022-03-30T11:50:17Z"
  generation: 1
  name: node-1
  resourceVersion: "2687986"
  uid: 1567bb4b-87a7-4273-a8fd-f44125c62b80
spec: {}
status:
  nodeMetric:
    nodeUsage:
      resources:
        cpu: 138m
        memory: "1815637738"
  podsMetric:
  - name: storage-service-6c7c59f868-k72r5
    namespace: default
    podUsage:
      resources:
        cpu: "300m"
        memory: 17828Ki
```

### Colocation Resources

After the Koordinator is deployed in the K8s cluster, the Koordinator will calculate the CPU and Memory resources that have been allocated but not used according to the data of `NodeMetrics`. These resources are updated in Node in the form of extended resources. 

`koordinator.sh/batch-cpu` represents the CPU resources for Best Effort workloads, 
`koordinator.sh/batch-memory` represents the Memory resources for Best Effort workloads. 

You can view these resources with the following commands.

```shell
$ kubectl describe node node-1
Name:               node-1
....
Capacity:
  cpu:                          8
  ephemeral-storage:            103080204Ki
  koordinator.sh/batch-cpu:     4541
  koordinator.sh/batch-memory:  17236565027
  memory:                       32611012Ki
  pods:                         64
Allocatable:
  cpu:                          7800m
  ephemeral-storage:            94998715850
  koordinator.sh/batch-cpu:     4541
  koordinator.sh/batch-memory:  17236565027
  memory:                       28629700Ki
  pods:                         64
```


### Cluster-level Colocation Profile

In order to make it easier for everyone to use Koordinator to co-locate different workloads, we defined `ClusterColocationProfile` to help gray workloads use co-location resources. A `ClusterColocationProfile` is CRD like the one below. Please do edit each parameter to fit your own use cases.

```yaml
apiVersion: config.koordinator.sh/v1alpha1
kind: ClusterColocationProfile
metadata:
  name: colocation-profile-example
spec:
  namespaceSelector:
    matchLabels:
      koordinator.sh/enable-colocation: "true"
  selector:
    matchLabels:
      sparkoperator.k8s.io/launched-by-spark-operator: "true"
  qosClass: BE
  priorityClassName: koord-batch
  koordinatorPriority: 1000
  schedulerName: koord-scheduler
  labels:
    koordinator.sh/mutated: "true"
  annotations: 
    koordinator.sh/intercepted: "true"
  patch:
    spec:
      terminationGracePeriodSeconds: 30
```

Various Koordinator components ensure scheduling and runtime quality through labels `koordinator.sh/qosClass`, `koordinator.sh/priority` and kubernetes native priority.

With the webhook mutating mechanism provided by Kubernetes, koord-manager will modify Pod resource requirements to co-located resources, and inject the QoS and Priority defined by Koordinator into Pod.

Taking the above Profile as an example, when the Spark Operator creates a new Pod in the namespace with the `koordinator.sh/enable-colocation=true` label, the Koordinator QoS label `koordinator.sh/qosClass` will be injected into the Pod. According to the Profile definition PriorityClassName, modify the Pod's PriorityClassName and the corresponding Priority value. Users can also set the Koordinator Priority according to their needs to achieve more fine-grained priority management, so the Koordinator Priority label `koordinator.sh/priority` is also injected into the Pod. Koordinator provides an enhanced scheduler koord-scheduler, so you need to modify the Pod's scheduler name koord-scheduler through Profile.

If you expect to integrate Koordinator into your own system, please learn more about the [core concepts](/docs/core-concepts/architecture).

### CPU Suppress

In order to ensure the runtime quality of different workloads in co-located scenarios, Koordinator uses the CPU Suppress mechanism provided by koordlet on the node side to suppress workloads of the Best Effort type when the load increases. Or increase the resource quota for Best Effort type workloads when the load decreases.

When installing through the helm chart, the ConfigMap `slo-controller-config` will be created in the koordinator-system namespace, and the CPU Suppress mechanism is enabled by default. If it needs to be closed, refer to the configuration below, and modify the configuration of the resource-threshold-config section to take effect.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: slo-controller-config
  namespace: {{ .Values.installation.namespace }}
data:
  ...
  resource-threshold-config: |
    {
      "clusterStrategy": {
        "enable": false
      }
    }
```

### Colocation Resources Balance
Koordinator currently adopts a strategy for node co-location resource scheduling, which prioritizes scheduling to machines with more resources remaining in co-location to avoid Best Effort workloads crowding together. More rich scheduling capabilities are on the way.
