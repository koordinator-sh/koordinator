# GPU Monitor on DCGM

## Summary

To support koordinator's GPU monitoring capabilities, koordinator needs smooth integration with the GPU monitoring component dcgm. In order to achieve this goal, a joint adaptation scheme of koordinator and dcgm is proposed to collect GPU indicators on a single node.


## Motivation

dcgm is a GPU monitoring component, which reads the kubelet.sock on the side to obtain pod and allocate GPU information, and then associate GPU monitoring indicators. The GPU allocation of Koordlet framework is a centralized allocation architecture, and kublete does not have GPU allocation information on the end side, so adaptation modification is required.

### Goals
- The GPU NRI HOOK writes the Pod list and corresponding GPU list information to the mount file on the GPU NRI maintenance terminal.
- Adjust the logic of the DCGM component reading Pod and GPU: switch from reading from kubelet.sock to reading from the above mount file

## Proposal

### User Stories

#### Story 1

As a user, I want to observe the performance metrics of GPU at runtime.

#### Story 2

As a cluster administrator, I want to monitor the GPU resource usage of the cluster.

### Design

#### GPU NRI Hook

The GPU NRI Hook maintains a list of all Pods on the node and a list of corresponding allocated GPUs in the gpu.go code each time a Pod's GPU request is processed.

The path of the gpu.go code file is:

```go
pkg/koordlet/runtimehooks/hooks/gpu
                       - gpu.go
```

Define a function in the gpu.go code file: maintainPodList().

Maintain PodList logic is implemented in the function maintainPodList(), and function named InjectContainerGPUEnv() in the Gpu.go code file actively calls the function to dynamically maintain the latest Pod list each time the GPU environment variable is set.The function definition and function call are as follows in the gpu.go code file:

```go
func maintainPodList(request ContainerRequest) error {
	...

}

func (p *gpuPlugin) InjectContainerGPUEnv(proto protocol.HooksProtocol) error {
	...
	
	maintainPodList(request)
}
```

In order to be compatible with dcgm's pod and GPU mapping data structures, a structure named PodInfo is defined in the NRI HOOK to maintain Pod information, including the following properties: Pod's name, namespace, container name, and list of assigned GPUs.

The structure PodInfo is defined as follows:

```go
type PodInfo struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Container string   `json:"container"`
	GPUList   []string `json:"gpu_list"`
}
```

Then koordlet mounts the directory file of the host, such as /var/lib/kubelet/pod-resources/podlist.json, which is used to write Pod and GPU information. Mount information is edited at koordlet.yaml:

```yaml
volumeMounts:
- mountPath: /podlist.json
  name: podInfo
......
volumes:
- hostPath:
  path: /var/lib/kubelet/pod-resources/podlist.json
  type: ""
  name: podInfo
```

The GPU NRI Hook is then responsible for converting the maintained podInfo list information into a json string and writing it to a file named podlist.json.

<img src="/docs/images/dcgm-gpuhook-architecture.png" style="zoom:25%;" />

#### DCGM

In the DCGM native project, the logic to extract the list of Pods and the list of Gpus is shown in the following code:

```go
#dcgm\pkg\dcgmexporter\kubernetes.go

func (p *PodMapper) Process(metrics MetricsByCounter, sysInfo SystemInfo) error {
	socketPath := p.Config.PodResourcesKubeletSocket
	_, err := os.Stat(socketPath)
	if os.IsNotExist(err) {
		logrus.Info("No Kubelet socket, ignoring")
		return nil
	}

	c, cleanup, err := connectToServer(socketPath)
	if err != nil {
		return err
	}
	defer cleanup()

	pods, err := p.listPods(c)
	if err != nil {
		return err
	}

	deviceToPod := p.toDeviceToPod(pods, sysInfo)

	......

	return nil
}
```

The connectToServer(socketPath) above indicates: PodResourcesKubeletSocket pointing Kubernetes is a particular node in the cluster on the Unix domain socket file, through this socket can query scheduling to the Pod of this node and its information resources.That is, use gRPC to query Pod resource information from kubelet.

Because the architecture of the koordinator framework allocates GPU devices centrally on the central side, rather than by Kubelet end-to-side, kubelet does not save Pod and GPU allocation information. Therefore, the logic of DCGM querying pod and GPU devices needs to be adjusted, from the previous query kubelet to read the file podlist.json to query. Since dcgm is deployed in DaemonSet, the dcgm component on each node must be mounted to the directory of the host machine: /var/lib/kubelet/pod-resources/podlist.json, so that the file can be accessed directly from inside the dcgm container.The architecture diagram of dcgm is as follows:

<img src="/docs/images/dcgm-gpuhook-readwrite.png" style="zoom:25%;" />

Defines a function that reads a json file to construct a pod and gpu mapping.

```go
func (p *PodMapper) listPods(string filePath) ([]PodInfo, error) {}
```

Define another function to handle the listPods return value: the PodInfo array. Walk through the set, converted to map[string]PodInfo

```go
func (p *PodMapper) toDeviceToPod([]PodInfo, sysInfo SystemInfo) map[string]PodInfo {}
```

Finally, the function Process of dcgm is changed to:

```
#dcgm\pkg\dcgmexporter\kubernetes.go

func (p *PodMapper) Process(metrics MetricsByCounter, sysInfo SystemInfo) error {
	socketPath := p.Config.PodResourcesKubeletSocket
	_, err := os.Stat(socketPath)
	if os.IsNotExist(err) {
		logrus.Info("No Kubelet socket, ignoring")
		return nil
	}

	c, cleanup, err := connectToServer(socketPath)
	if err != nil {
		return err
	}
	defer cleanup()
	
	pods, err := p.listPods(/podlist.json)
	if err != nil {
		return err
	}

	deviceToPod := p.toDeviceToPod(pods, sysInfo)

	......

	return nil
}
```

