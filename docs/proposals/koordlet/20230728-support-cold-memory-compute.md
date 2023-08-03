---
title: Support to collect and report cold memory
authors:
  - "@BUPT-wxq"
reviewers:
  - "@saintube"
  - "@zwzhang0107"
  - "@eahydra"
  - "@hormes"
  - "@FillZpp"
creation-date: 2023-07-28
last-updated: 2023-07-28
---

## Table of Contents


- [Title](#title)
  - [Table of Contents](#table-of-contents)
  - [Glossary](#glossary)
  - [Motivation](#motivation)
    - [Goals](#goals)
    - [Non-Goals/Future Work](#non-goalsfuture-work)
  - [Proposal](#proposal)
    - [Design](#design)
    - [Collect idle memory info and insert memory with hot page](#collect-idle-memory-info-and-insert-memory-with-hot-page)
	- [Report memory usage including hot page](#report-memory-usage-including-hot-page)
	- [Define cold memory API](#define-cold-memory-api)
  - [Alternatives](#alternatives)

## Glossary

**kidled**

Kidled, an open source cold memory collection solution, identifies the hot and cold conditions of nodes, pod and container memory in the cluster. The link introduces kidled in detail.

https://github.com/alibaba/cloud-kernel/blob/linux-next/Documentation/vm/kidled.rst

**cold page**

The part of memory that is allocated to application but probably unused for a while is called the "cold" memory. And the part of that in page cache is called cold page.

**hot page**

Relatively, the memory which may be reclaimable but would be reallocated shortly after is the "hot" memory. And the part of that in page cache is called hot page. 

## Summary

Implement the cold page info collecting and reporting. Based on that, we can incorporate cold memory compute into memory usage to support more fine-grained memory overcommitment in koordinator.

## Motivation

In general colocation scenarios, the resources that the high-priority (HP) applications have requested but not used are reclaimable to the low-priority (LP) applications to allocate. Resource reclaiming helps enhance the utilization of machines while increasing the risks of contentions. For memory resources, there are proportions of memory that are reusable on different levels. The free memory is allocatable to any process, while the page cache is potentially reusable after it is reclaimed by the system and no longer yields again. But  kernel does not proactively reclaim the page cache because for the better performance of the system, the kernel do not keep page cache idle and allocate the page cache to application as cache. To construct a more reliable and fine-grained memory overcommitment, the free and cold memory of the HP is preferred to reclaim to LP first. And if not required, the hot memory of HP should not be overcommitted, or the performance of HP can be affected. 

In the Koordinator, we want to improve memory overcommitment by building the cold page reclaim ability, which can report the quantity of cold memory on the nodes and help assure that resource reclaiming like batch-memory are sound and efficient for the colocated applications.

https://github.com/koordinator-sh/koordinator/issues/1178

### Goals

Define the API for the cold pages. Implement the cold page info collecting and reporting.

### Non-Goals/Future Work

Cold memory supports  scheduling optimization in koord-manager or koord-scheduler.

## Proposal

### Design

#### Collect idle memory info and insert memory with hot page

![image](../../images/support-cold-memory-1.svg)

Cold memory is idle pages in page cache. This proposal applies kidled to collect idle pages. Kidled will export a file named memory.idle_stat which includes idle page. The file will exist at each hierarchical of cgroup memory. The process of collect idle memory is as shown in figure. First, kidled exports memory.idle_stat file. Second, the corresponding collector(noderesource and podresource) will collect idle info and insert metric.

The proposal add a file idleinfo.go in pkg/koordlet/util/idleinfo.go

Define an idleinfo struct as follows. It corresponds to this memory.idle_stat file information. For example  c means clean. d means dirty. s means swap. f means file. e means evict. u means uevict.  i means inactive. a means active. ``csea`` means the pages are clean && swappable && evictable && active. More details are in https://github.com/alibaba/cloud-kernel/blob/linux-next/Documentation/vm/kidled.rst.

```go
type IdleInfo struct {
	Version             string   `json:"version"`
	PageScans           uint64   `json:"page_scans"`
	SlabScans           uint64   `json:"slab_scans"`
	ScanPeriodInSeconds uint64   `json:"scan_period_in_seconds"`
	UseHierarchy        uint64   `json:"use_hierarchy"`
	Buckets             []uint64 `json:"buckets"`
	Csei                []uint64 `json:"csei"`
	Dsei                []uint64 `json:"dsei"`
	Cfei                []uint64 `json:"cfei"`
	Dfei                []uint64 `json:"dfei"`
	Csui                []uint64 `json:"csui"`
	Dsui                []uint64 `json:"dsui"`
	Cfui                []uint64 `json:"cfui"`
	Dfui                []uint64 `json:"dfui"`
	Csea                []uint64 `json:"csea"`
	Dsea                []uint64 `json:"dsea"`
	Cfea                []uint64 `json:"cfea"`
	Dfea                []uint64 `json:"dfea"`
	Csua                []uint64 `json:"csua"`
	Dsua                []uint64 `json:"dsua"`
	Cfua                []uint64 `json:"cfua"`
	Dfua                []uint64 `json:"dfua"`
	Slab                []uint64 `json:"slab"`
}
```

Define a function named readidleInfo() in koordlet util moudle. It can read idleinfo memory.idle_stat which exists  under every hierarchy of cgroup memory. (node e.g. /sys/fs/cgroup/memory/memory.idle_stat)

```go
func readIdleInfo(path string) (*IdleInfo, error) 
```

Define a function named GetIdleInfoFilePath(). It is uesd to return idleinfo file path.(node e.g. /sys/fs/cgroup/memory/memory.idle_stat)

```go
func GetIdleInfoFilePath(idleFileRelativePath string) string 
```

Define a function named GetIdleMemoryTotalKB(). It is uesd to return size of total idle page. The unit is byte.

```go
func GetIdleMemoryTotalKB(idleFileRelativePath string) (uint64, error) 
```

Define two functions  in pkg/koordlet/util/meminfo.go  to get node's MemTotal and MemFree. The values are used to compute Mem usage with hot page.

```go
// GetMemTotalKB returns the node's memory total quantity (kB)
func GetMemTotalKB() (int64, error) 
// GetMemFreeKB returns the node's memory free quantity (kB)
func GetMemFreeKB() (int64, error)
```

Define a function named collectNodeMemoryWithHotPageUsage().  Compute node memory usage with hot page and generate metric. collectNodeResUsed() function will call that and insert memoryWithHotPage metric.

```go
func (n *nodeResourceCollector) collectNodeMemoryWithHotPageUsage(collectTime time.Time) (metriccache.MetricSample, error) 
```

Define a function named collectNodeMemoryColdPageSize().  Compute node memory cold page size and generate metric. collectNodeResUsed() function will call that and insert memoryColdPageSize metric.

```go
func (n *nodeResourceCollector) collectNodeMemoryColdPageSize(collectTime time.Time) (metriccache.MetricSample, error)
```

The same process is executed in podresource collector to collect idle page. Do not repeat.

#### Report memory usage including hot page

![image](../../images/support-cold-memory-2.svg)

collectNodeMetric() is used to query node metirc and return CPU And MemUsed in pkg/koordlet/statesinformer/impl/states_nodemetirc.go file.

We can report memory usage including hot pages in collectNodeMetric().

The calculation formulas of node, pod and container are as follows.  

node memory usage: nodeMemWithHotPageUasge=MemTotal-MemFree+NodeMeMWithHotPageSize

pod memory usage: podMemWithHotPageUasge=m.InactiveAnon + m.ActiveAnon + m.Unevictable+PodMeMwithHotPageSize

container memory usage: containerMemWithHotPageUasge=m.InactiveAnon + m.ActiveAnon + m.Unevictable+ContainerMeMwithHotPageSize

The same process is executed pod informer to report memory usage. Do not repeat.

#### Define cold memory API

Provide memory collect policy access.

Add field named MemoryCollectPolicy to represent which method is used to collect memory usage. Such as usageWithHotPageCache, usageWithoutPageCache,  usageWithPageCache.

You can create a crd nodemeric resource and specify value of spec.CollectPolicy.MemoryCollectPolicy to start collect cold memory compute. 

```go
// NodeMetricCollectPolicy defines the Metric collection policy
type NodeMetricCollectPolicy struct {
	// AggregateDurationSeconds represents the aggregation period in seconds
	AggregateDurationSeconds *int64 `json:"aggregateDurationSeconds,omitempty"`
	// ReportIntervalSeconds represents the report period in seconds
	ReportIntervalSeconds *int64 `json:"reportIntervalSeconds,omitempty"`
	// NodeAggregatePolicy represents the target grain of node aggregated usage
	NodeAggregatePolicy *AggregatePolicy `json:"nodeAggregatePolicy,omitempty"`
	//MemoryWithHotPageCollectPolicy represents whether collet memory usage with hot page instead of memory usage without page cache
	MemoryCollectPolicy string `json:"memoryCollectPolicy,omitempty"`
}
```
## Alternatives

**kidled**: an open source cold memory collection solution.

https://github.com/alibaba/cloud-kernel/blob/linux-next/Documentation/vm/kidled.rst

**kstaled**: Michel's patch was developed on a early kernel version 3.0 and was similar to kidled. kstable also use /sys/kernel/mm/kstaled/scan_seconds and export idle_page_stats file. But kidled did not cherry pick the original kstaled's patch directly and made improvements. (e.g. design use_hierarchy ).

But I did not see the /sys/kernel/mm/kstaled/scan_seconds in ubuntu 18 and 20 image. Perhaps kstaled is on google linux kernel.

https://lwn.net/Articles/459269/

https://lore.kernel.org/lkml/20110922161448.91a2e2b2.akpm@google.com/T/

**DAMON**: DAMON was used  by amazon linux kernel.

https://lwn.net/Articles/858682/

why we choose kidled? The reasons are as below.

1) Anolis OS has already supported kidled. We can directly use the api and it is very simple.
2) Kidled have a global scanner to do this job. That avoids a lot of unnecessary job. For example switch between user and kernel mode and handle share mappings.