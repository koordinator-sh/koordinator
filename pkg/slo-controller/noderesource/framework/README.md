# NodeResource Framework

## Overview

The node resource controller is responsible for calculating the over-commit resources and updating the result on the
node. The node resource framework defines some extension points for resource calculation and updating strategy. Each
node resource plugin can implement one or more stages to reconcile the node resources.

The current extension stages provided by the node resource framework are as following:

- **Setup**: It setups the plugin with options like controller client, scheme and event recorder.

```go
type Plugin interface {
	Name() string
}

type SetupPlugin interface {
	Plugin
	Setup(opt *Option) error
}
```

- **Calculate**: It calculates the node resources according to the Node and NodeMetric and generate a list of calculated
node resource items. All node resource items will be merged into a `NodeResource` as the intermediate result of the
Calculate stage. In case of the NodeMetric is abnormal, a plugin can implement the degraded calculation inside this
stage. The Calculate plugin is also responsible for the reset of corresponding node resources when the colocation is
configured as disabled.

```go
type ResourceCalculatePlugin interface {
	Plugin
	Reset(node *Node, message string) []ResourceItem
	Calculate(strategy *ColocationStrategy, node *Node, podList *PodList, metrics *ResourceMetrics) ([]ResourceItem, error)
}

type ResourceItem struct {
	Name        ResourceName
	Quantity    *Quantity
	Labels      map[string]string
	Annotations map[string]string
	Message     string
	Reset       bool
}
```

- **PreUpdate**: It allows the plugin to preprocess for the calculated results called before updating the Node.
For example, a plugin can prepare and update some Objects like CRDs before updating the Node. And the plugin also can
mutate the internal NodeResource object according the fully calculated results.
It differs from the Prepare stage since a NodePreUpdatePlugin will be invoked only once in one loop (so the plugin
should consider implement a retry login itself if needed), while the NodePreparePlugin is not expected to update other
objects or mutate the NodeResource.

```go
type NodePreUpdatePlugin interface {
	Plugin
	PreUpdate(strategy *ColocationStrategy, node *Node, nr *NodeResource) error
}
```

- **Prepare**: It prepares the Node object with the calculated result `NodeResource`. Before the updating, it is
invoked after the Calculate so to allow the plugin to retry when the client updates conflicts.

```go
type NodePreparePlugin interface {
	Plugin
	Prepare(strategy *ColocationStrategy, node *Node, nr *NodeResource) error
}

type NodeResource struct {
	Resources   map[ResourceName]*Quantity
	Labels      map[string]string
	Annotations map[string]string
	Messages    map[ResourceName]string
	Resets      map[ResourceName]bool
}
```

- **NodeCheck**: It checks if the newly-prepared Node object should be synchronized to the kube-apiserver. To be more
specific, currently there are two types of NeedSync plugins for different client update methods, where one can determine
whether the node status should be updated and another determines whether node metadata should be updated.

```go
type NodeStatusCheckPlugin interface {
	Plugin
	NeedSync(strategy *ColocationStrategy, oldNode, newNode *Node) (bool, string)
}

type NodeMetaCheckPlugin interface {
	Plugin
	NeedSyncMeta(strategy *ColocationStrategy, oldNode, newNode *Node) (bool, string)
}
```

There is the workflow about how the node resource controller handles a dequeued Node object with plugins:

![framework-img](../../../../docs/images/noderesource-framework.svg)

## Example: Batch Resource Plugin

The default `BatchResource` plugin is responsible for calculating and updating the Batch-tier resources.
It implements the stages `Setup`, `Calculate`/`Reset`, `PreUpdate`, `Prepare` and `NodeStatusCheck`:

**Setup**:

In the initialization, the plugin sets the kube client, and add a watch for the NodeResourceTopology.

**Calculate**:

For each node, the plugin summarizes the resource allocated and the usage of high-priority (HP, priority classes higher
than Batch) pods, then derives the allocatable resources of the Batch-tier with the formula:

```
batchAllocatable := nodeAllocatable * thresholdPercent - podUsage(HP) - systemUsage
```

Besides, the plugin implements the `Reset` method to clean up the Batch resources when the node colocation is disabled.

**PreUpdate**:

Before updating the Node obj, the plugin updates the zone-level Batch resources for the NRT (NodeResourceTopology)
according to the calculated results from the `Calculate` stage.

**Prepare**:

The plugin sets the extended resources `kubernetes.io/batch-cpu`, `kubernetes.io/batch-memory` in the
`node.status.allocatable` according to the calculated results from the `Calculate`/`Reset` stage.

**NodeStatusCheck**:

The plugin checks the extended resources `kubernetes.io/batch-cpu`, `kubernetes.io/batch-memory` of the prepared node
and the old node. If the node's Batch resources have not been updated for too long or the calculated results changes
too much, it will update the prepared Node object to the kube-apiserver.

## What's More

The node resource framework is in *Alpha*. The defined stages may not be enough for some new scenarios. Please feel
free to send Issues and PRs for improving the framework.
