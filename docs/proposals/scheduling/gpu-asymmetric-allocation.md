# GPU 非对称分配和碎片整理设计方案

## 元数据

- **标题**: GPU 非对称分配和碎片整理
- **作者**: 基于用户需求设计
- **状态**: 提案
- **创建时间**: 2026-01-12
- **最后更新**: 2026-01-12

## 目录

- [摘要](#摘要)
- [动机](#动机)
- [目标](#目标)
- [非目标](#非目标)
- [提案](#提案)
  - [用户故事](#用户故事)
  - [API 设计](#api-设计)
  - [分配算法设计](#分配算法设计)
  - [碎片整理设计](#碎片整理设计)
- [设计细节](#设计细节)
- [实现阶段](#实现阶段)
- [测试计划](#测试计划)
- [风险和缓解措施](#风险和缓解措施)
- [替代方案](#替代方案)

---

## 摘要

本提案旨在解决生产集群中严重的 GPU 内存碎片化问题，通过支持非对称/非均匀 GPU 分配，提高 GPU 资源利用率。当前调度器假设每个 GPU 分配相同的资源量（例如 2 张卡各 20GB），但实际场景中 GPU 可用内存不均匀（例如 GPU 0 有 10GB，GPU 1 有 30GB），导致调度失败和资源浪费。

本提案提供了一套完整的解决方案：
1. **API 增强**: 支持非对称分配的 API 扩展
2. **分配算法**: 多种分配策略（贪婪装箱、碎片优先、均衡分配等）
3. **碎片整理**: 可选的重调度和碎片整理机制
4. **环境变量注入**: 支持每个 GPU 不同的资源限制

---

## 动机

### 问题描述

生产集群中存在严重的 GPU 内存碎片化问题：

**示例场景**:
- GPU 0 有 10GB 可用内存
- GPU 1 有 30GB 可用内存
- Pod 请求: 2 张卡，总共 40GB 内存

**当前行为**:
- 调度器尝试平均分配：每张卡 20GB
- GPU 0 只有 10GB 可用，导致调度失败（Pending）

**期望行为**:
- 调度器支持非对称分配：GPU 0 分配 10GB，GPU 1 分配 30GB
- 总资源满足需求，调度成功

### 资源浪费的严重性

从生产环境观察（NVTOP 数据）：
- 许多 GPU 的内存使用率很高，但计算使用率为 0%
- 可用内存非常分散：
  - 某些 GPU 有 50% 可用内存
  - 某些 GPU 只有 10% 可用内存
- **如果没有非对称分配，无法将这些"零散"的内存碎片合并起来运行更大的工作负载**

### 业务影响

1. **资源利用率低**: 大量 GPU 内存碎片无法被利用
2. **调度失败率高**: 本可以满足的请求因分配策略限制而失败
3. **成本浪费**: GPU 资源昂贵，无法充分利用导致成本增加
4. **用户体验差**: Pod 长时间 Pending，影响业务运行

---

## 目标

1. **支持非对称 GPU 分配**: 允许每个 GPU 分配不同数量的资源
2. **提供多种分配策略**:
   - 贪婪装箱算法（最大化资源利用）
   - 碎片优先策略（优先使用碎片节点）
   - 均衡分配策略（保持向后兼容）
3. **灵活的配置**: 支持 Pod 级别和集群级别的策略配置
4. **环境变量注入**: 为每个 GPU 注入独立的资源限制信息
5. **可选的碎片整理**: 提供重调度机制来减少碎片化
6. **向后兼容**: 不影响现有工作负载

---

## 非目标

1. **不改变 GPU 资源模型**: 不修改 Kubernetes 原生资源模型
2. **不保证实时碎片整理**: 碎片整理是可选的，不是强制的
3. **不支持跨节点 GPU 分配**: 仅支持单节点内的多 GPU 分配
4. **不处理 GPU 故障**: GPU 健康检查和故障处理不在本提案范围内

---

## 提案

### 用户故事

#### 故事 1: 机器学习训练任务

**作为** 机器学习工程师
**我想要** 使用 2 张 GPU 卡，总共 40GB 内存来训练模型
**以便** 能够在内存碎片化的集群中成功调度任务

**当前问题**:
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: ml-training
spec:
  containers:
  - name: trainer
    resources:
      requests:
        koordinator.sh/gpu: "2"
        koordinator.sh/gpu-memory: "40Gi"
```

当前调度器会尝试找到 2 张各有 20GB 内存的 GPU，但实际上节点有 GPU0(10GB) 和 GPU1(30GB)，导致调度失败。

**期望结果**:
调度器应该能够将任务调度到该节点，分配 GPU0(10GB) + GPU1(30GB)。

#### 故事 2: 推理服务

**作为** 推理服务运维人员
**我想要** 优先使用碎片化的 GPU 节点
**以便** 为高优先级的训练任务保留整卡资源

**期望配置**:
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: inference-service
  annotations:
    scheduling.koordinator.sh/device-allocate-hint: |
      {
        "gpu": {
          "allocateStrategy": "FragmentPacking",
          "allocationMode": "Asymmetric"
        }
      }
spec:
  containers:
  - name: inference
    resources:
      requests:
        koordinator.sh/gpu: "2"
        koordinator.sh/gpu-memory: "20Gi"
```

#### 故事 3: 碎片整理

**作为** 集群管理员
**我想要** 在低谷期自动整理 GPU 内存碎片
**以便** 提高整体资源利用率

**期望配置**:
```yaml
apiVersion: config.koordinator.sh/v1alpha1
kind: ClusterColocationProfile
metadata:
  name: gpu-defragmentation
spec:
  deviceDefragmentation:
    enabled: true
    schedule: "0 2 * * *"  # 每天凌晨 2 点执行
    strategy:
      fragmentationThreshold: 0.3  # 碎片率超过 30% 时触发
      maxMigrationsPerCycle: 10    # 每次最多迁移 10 个 Pod
      podSelector:
        matchLabels:
          defrag: "allowed"  # 只迁移允许的 Pod
```

---

### API 设计

本提案的 API 设计遵循以下原则：
1. **向后兼容**: 不破坏现有 API
2. **渐进增强**: 通过新字段支持新功能
3. **灵活配置**: 支持 Pod 级别和集群级别配置
4. **清晰语义**: API 字段含义明确

#### 1. DeviceHint API 扩展

在现有的 `DeviceHint` 结构中添加新字段来支持非对称分配：

```go
// apis/extension/device_share.go

type DeviceHint struct {
    // 现有字段
    Selector *metav1.LabelSelector `json:"selector,omitempty"`
    VFSelector *metav1.LabelSelector `json:"vfSelector,omitempty"`
    AllocateStrategy DeviceAllocateStrategy `json:"allocateStrategy,omitempty"`
    RequiredTopologyScope DeviceTopologyScope `json:"requiredTopologyScope,omitempty"`
    ExclusivePolicy DeviceExclusivePolicy `json:"exclusivePolicy,omitempty"`

    // 新增字段
    // AllocationMode 控制资源分配模式
    // +optional
    AllocationMode DeviceAllocationMode `json:"allocationMode,omitempty"`

    // PackingStrategy 控制装箱策略
    // +optional
    PackingStrategy DevicePackingStrategy `json:"packingStrategy,omitempty"`

    // PreferFragmented 表示是否优先使用碎片化节点
    // +optional
    PreferFragmented *bool `json:"preferFragmented,omitempty"`

    // MinResourcePerDevice 指定每个设备的最小资源量
    // 用于确保每个设备至少分配一定的资源
    // +optional
    MinResourcePerDevice corev1.ResourceList `json:"minResourcePerDevice,omitempty"`

    // MaxResourcePerDevice 指定每个设备的最大资源量
    // 用于限制单个设备的资源分配
    // +optional
    MaxResourcePerDevice corev1.ResourceList `json:"maxResourcePerDevice,omitempty"`
}

// DeviceAllocationMode 定义资源分配模式
type DeviceAllocationMode string

const (
    // SymmetricAllocation 表示对称分配（默认，向后兼容）
    // 每个设备分配相同的资源量
    SymmetricAllocation DeviceAllocationMode = "Symmetric"

    // AsymmetricAllocation 表示非对称分配
    // 允许每个设备分配不同的资源量
    AsymmetricAllocation DeviceAllocationMode = "Asymmetric"

    // PreferSymmetric 表示优先对称分配，但允许非对称
    // 首先尝试对称分配，如果失败则回退到非对称分配
    PreferSymmetric DeviceAllocationMode = "PreferSymmetric"
)

// DevicePackingStrategy 定义装箱策略
type DevicePackingStrategy string

const (
    // BestFitPacking 表示最佳适配装箱
    // 选择能够最紧密匹配请求的设备组合
    BestFitPacking DevicePackingStrategy = "BestFit"

    // FirstFitPacking 表示首次适配装箱
    // 选择第一个能够满足请求的设备组合（性能最优）
    FirstFitPacking DevicePackingStrategy = "FirstFit"

    // WorstFitPacking 表示最差适配装箱（碎片优先）
    // 选择剩余资源最多的设备组合，优先使用碎片化节点
    WorstFitPacking DevicePackingStrategy = "WorstFit"

    // BalancedPacking 表示均衡装箱
    // 尝试均衡各设备的资源使用，减少未来碎片化
    BalancedPacking DevicePackingStrategy = "Balanced"
)
```

#### 2. DeviceAllocation API 扩展

增强 `DeviceAllocation` 结构来记录实际分配的资源：

```go
// apis/extension/device_share.go

type DeviceAllocation struct {
    // 现有字段
    Minor     int32               `json:"minor"`
    Resources corev1.ResourceList `json:"resources"`
    ID        string              `json:"id,omitempty"`
    Extension *DeviceAllocationExtension `json:"extension,omitempty"`

    // 新增字段
    // AllocationScore 表示该设备的分配得分
    // 用于记录分配时的决策信息，便于调试和优化
    // +optional
    AllocationScore int64 `json:"allocationScore,omitempty"`

    // FragmentationScore 表示分配后该设备的碎片化得分
    // 范围 0-100，0 表示无碎片，100 表示严重碎片
    // +optional
    FragmentationScore int32 `json:"fragmentationScore,omitempty"`
}

type DeviceAllocationExtension struct {
    // 现有字段
    VirtualFunctions          []VirtualFunction `json:"vfs,omitempty"`
    GPUSharedResourceTemplate string            `json:"gpuSharedResourceTemplate,omitempty"`

    // 新增字段
    // ResourceLimits 为每个 GPU 指定独立的资源限制
    // 用于环境变量注入，让应用程序感知到每个 GPU 的实际可用资源
    // +optional
    ResourceLimits corev1.ResourceList `json:"resourceLimits,omitempty"`

    // AllocationMode 记录实际使用的分配模式
    // +optional
    AllocationMode DeviceAllocationMode `json:"allocationMode,omitempty"`
}
```

#### 3. 集群级别配置 API

在 `ClusterColocationProfile` 中添加 GPU 碎片整理配置：

```go
// apis/config/v1alpha1/clustercolocationprofile_types.go

type ClusterColocationProfileSpec struct {
    // 现有字段...

    // DeviceDefragmentation 定义设备碎片整理策略
    // +optional
    DeviceDefragmentation *DeviceDefragmentationSpec `json:"deviceDefragmentation,omitempty"`
}

type DeviceDefragmentationSpec struct {
    // Enabled 表示是否启用碎片整理
    // +optional
    Enabled bool `json:"enabled,omitempty"`

    // Schedule 定义碎片整理的执行时间（Cron 表达式）
    // 例如: "0 2 * * *" 表示每天凌晨 2 点执行
    // +optional
    Schedule string `json:"schedule,omitempty"`

    // Strategy 定义碎片整理策略
    // +optional
    Strategy DefragmentationStrategy `json:"strategy,omitempty"`
}

type DefragmentationStrategy struct {
    // FragmentationThreshold 定义触发碎片整理的阈值
    // 范围 0.0-1.0，表示节点碎片率超过该值时触发整理
    // 碎片率 = (可用资源 - 最大连续可用资源) / 总资源
    // +optional
    FragmentationThreshold float64 `json:"fragmentationThreshold,omitempty"`

    // MaxMigrationsPerCycle 定义每次整理周期最多迁移的 Pod 数量
    // 用于控制整理的影响范围，避免对集群造成过大压力
    // +optional
    MaxMigrationsPerCycle int `json:"maxMigrationsPerCycle,omitempty"`

    // MaxMigrationsPerNode 定义每个节点最多迁移的 Pod 数量
    // +optional
    MaxMigrationsPerNode int `json:"maxMigrationsPerNode,omitempty"`

    // PodSelector 定义允许被迁移的 Pod 选择器
    // 只有匹配该选择器的 Pod 才会被考虑迁移
    // +optional
    PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`

    // ExcludedPods 定义不允许被迁移的 Pod 列表
    // 格式: namespace/podName
    // +optional
    ExcludedPods []string `json:"excludedPods,omitempty"`

    // DryRun 表示是否只执行模拟，不实际迁移
    // +optional
    DryRun bool `json:"dryRun,omitempty"`

    // MigrationTimeout 定义迁移超时时间
    // +optional
    MigrationTimeout *metav1.Duration `json:"migrationTimeout,omitempty"`

    // NodeSelector 定义参与碎片整理的节点选择器
    // +optional
    NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`

    // PreferredTargetUtilization 定义期望的目标利用率
    // 范围 0.0-1.0，整理后尽量让节点利用率接近该值
    // +optional
    PreferredTargetUtilization float64 `json:"preferredTargetUtilization,omitempty"`
}
```

#### 4. NodeMetric API 扩展

在 `NodeMetric` 中添加碎片化指标：

```go
// apis/slo/v1alpha1/nodemetric_types.go

type NodeMetricStatus struct {
    // 现有字段...

    // DeviceFragmentation 记录设备碎片化信息
    // +optional
    DeviceFragmentation []DeviceFragmentationMetric `json:"deviceFragmentation,omitempty"`
}

type DeviceFragmentationMetric struct {
    // DeviceType 设备类型
    DeviceType string `json:"deviceType"`

    // FragmentationScore 碎片化得分（0-100）
    // 0 表示无碎片，100 表示严重碎片
    FragmentationScore int32 `json:"fragmentationScore"`

    // TotalDevices 总设备数
    TotalDevices int `json:"totalDevices"`

    // FragmentedDevices 碎片化的设备数
    FragmentedDevices int `json:"fragmentedDevices"`

    // AvgUtilization 平均利用率
    AvgUtilization float64 `json:"avgUtilization"`

    // MaxContiguousMemory 最大连续可用内存
    MaxContiguousMemory resource.Quantity `json:"maxContiguousMemory"`

    // UpdateTime 更新时间
    UpdateTime metav1.Time `json:"updateTime"`
}
```

#### 5. 使用示例

##### 示例 1: 基本非对称分配

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: ml-training
  annotations:
    scheduling.koordinator.sh/device-allocate-hint: |
      {
        "gpu": {
          "allocationMode": "Asymmetric",
          "packingStrategy": "BestFit"
        }
      }
spec:
  containers:
  - name: trainer
    resources:
      requests:
        koordinator.sh/gpu: "2"
        koordinator.sh/gpu-memory: "40Gi"
```

**分配结果** (假设节点有 GPU0(10GB) 和 GPU1(30GB)):
```json
{
  "gpu": [
    {
      "minor": 0,
      "resources": {
        "koordinator.sh/gpu-memory": "10Gi"
      },
      "extension": {
        "resourceLimits": {
          "koordinator.sh/gpu-memory": "10Gi"
        },
        "allocationMode": "Asymmetric"
      }
    },
    {
      "minor": 1,
      "resources": {
        "koordinator.sh/gpu-memory": "30Gi"
      },
      "extension": {
        "resourceLimits": {
          "koordinator.sh/gpu-memory": "30Gi"
        },
        "allocationMode": "Asymmetric"
      }
    }
  ]
}
```

##### 示例 2: 碎片优先策略

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: inference-service
  annotations:
    scheduling.koordinator.sh/device-allocate-hint: |
      {
        "gpu": {
          "allocationMode": "Asymmetric",
          "packingStrategy": "WorstFit",
          "preferFragmented": true,
          "minResourcePerDevice": {
            "koordinator.sh/gpu-memory": "4Gi"
          }
        }
      }
spec:
  containers:
  - name: inference
    resources:
      requests:
        koordinator.sh/gpu: "2"
        koordinator.sh/gpu-memory": "20Gi"
```

**说明**:
- `packingStrategy: "WorstFit"`: 选择剩余资源最多的设备（碎片优先）
- `preferFragmented: true`: 优先选择碎片化的节点
- `minResourcePerDevice`: 确保每个 GPU 至少分配 4GB

##### 示例 3: 优先对称分配

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: distributed-training
  annotations:
    scheduling.koordinator.sh/device-allocate-hint: |
      {
        "gpu": {
          "allocationMode": "PreferSymmetric",
          "packingStrategy": "Balanced"
        }
      }
spec:
  containers:
  - name: trainer
    resources:
      requests:
        koordinator.sh/gpu: "4"
        koordinator.sh/gpu-memory": "80Gi"
```

**说明**:
- `allocationMode: "PreferSymmetric"`: 优先尝试对称分配（每卡 20GB），失败后回退到非对称
- `packingStrategy: "Balanced"`: 均衡各设备的资源使用

---

### 分配算法设计

本节详细设计各种分配算法的实现逻辑。

#### 1. 算法概述

| 算法 | PackingStrategy | 优先级 | 适用场景 | 优点 | 缺点 |
|------|-----------------|--------|----------|------|------|
| 首次适配 | FirstFit | 最高（性能） | 通用 | 快速，低延迟 | 可能不是最优解 |
| 最佳适配 | BestFit | 高（质量） | 需要精确匹配 | 最小化剩余碎片 | 计算开销较大 |
| 最差适配 | WorstFit | 中（碎片优先） | 推理服务 | 优先使用碎片节点 | 可能造成资源浪费 |
| 均衡装箱 | Balanced | 中（长期优化） | 长期运行任务 | 减少未来碎片化 | 当前可能不是最优 |

#### 2. 核心数据结构

```go
// pkg/scheduler/plugins/deviceshare/allocator_gpu.go

// GPUCandidate 表示一个候选 GPU
type GPUCandidate struct {
    Minor             int32
    Available         corev1.ResourceList
    Total             corev1.ResourceList
    Utilization       float64
    FragmentationScore int32
    TopologyInfo      *DeviceTopology
}

// AllocationPlan 表示一个分配方案
type AllocationPlan struct {
    Devices            []*DeviceAllocation
    TotalScore         int64
    FragmentationScore int32
    IsSymmetric        bool
    RemainingResources map[int32]corev1.ResourceList
}
```

#### 3. 算法实现

##### 3.1 非对称分配主流程

```go
// allocateAsymmetric 实现非对称分配算法
func (p *Plugin) allocateAsymmetric(
    state *preFilterState,
    deviceRes *nodeDevice,
    hint *extension.DeviceHint,
    pod *corev1.Pod,
    nodeInfo *framework.NodeInfo,
) (apiext.DeviceAllocations, *framework.Status) {
    // 1. 解析请求
    gpuCount := int(state.skip[schedulingv1alpha1.GPU])
    totalMemory := state.podRequests[extension.ResourceGPUMemory]

    // 2. 获取候选 GPU 列表
    candidates := p.getCandidateGPUs(deviceRes, hint, nodeInfo)
    if len(candidates) < gpuCount {
        return nil, framework.NewStatus(framework.Unschedulable, "not enough GPUs")
    }

    // 3. 根据装箱策略选择算法
    var plan *AllocationPlan
    var err error

    switch hint.PackingStrategy {
    case extension.BestFitPacking:
        plan, err = p.bestFitAllocate(candidates, gpuCount, totalMemory, hint)
    case extension.FirstFitPacking:
        plan, err = p.firstFitAllocate(candidates, gpuCount, totalMemory, hint)
    case extension.WorstFitPacking:
        plan, err = p.worstFitAllocate(candidates, gpuCount, totalMemory, hint)
    case extension.BalancedPacking:
        plan, err = p.balancedAllocate(candidates, gpuCount, totalMemory, hint)
    default:
        plan, err = p.firstFitAllocate(candidates, gpuCount, totalMemory, hint)
    }

    if err != nil {
        return nil, framework.NewStatus(framework.Unschedulable, err.Error())
    }

    // 4. 验证分配方案
    if !p.validateAllocationPlan(plan, hint) {
        return nil, framework.NewStatus(framework.Unschedulable, "allocation plan validation failed")
    }

    // 5. 构建分配结果
    allocations := p.buildAllocations(plan, hint)

    return allocations, nil
}
```

##### 3.2 首次适配算法 (FirstFit)

```go
// firstFitAllocate 实现首次适配算法
// 选择第一个能够满足请求的设备组合
func (p *Plugin) firstFitAllocate(
    candidates []*GPUCandidate,
    gpuCount int,
    totalMemory resource.Quantity,
    hint *extension.DeviceHint,
) (*AllocationPlan, error) {
    // 1. 按照 Minor 排序（确保稳定性）
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].Minor < candidates[j].Minor
    })

    // 2. 尝试分配
    plan := &AllocationPlan{
        Devices: make([]*extension.DeviceAllocation, 0, gpuCount),
        RemainingResources: make(map[int32]corev1.ResourceList),
    }

    remainingMemory := totalMemory.DeepCopy()

    for _, candidate := range candidates {
        if len(plan.Devices) >= gpuCount {
            break
        }

        availableMemory := candidate.Available[extension.ResourceGPUMemory]

        // 检查是否满足最小资源要求
        if hint.MinResourcePerDevice != nil {
            minMemory := hint.MinResourcePerDevice[extension.ResourceGPUMemory]
            if availableMemory.Cmp(minMemory) < 0 {
                continue
            }
        }

        // 检查是否超过最大资源限制
        if hint.MaxResourcePerDevice != nil {
            maxMemory := hint.MaxResourcePerDevice[extension.ResourceGPUMemory]
            if availableMemory.Cmp(maxMemory) > 0 {
                availableMemory = maxMemory.DeepCopy()
            }
        }

        // 分配资源
        allocMemory := availableMemory.DeepCopy()
        if allocMemory.Cmp(remainingMemory) > 0 {
            allocMemory = remainingMemory.DeepCopy()
        }

        alloc := &extension.DeviceAllocation{
            Minor: candidate.Minor,
            Resources: corev1.ResourceList{
                extension.ResourceGPUMemory: allocMemory,
            },
            Extension: &extension.DeviceAllocationExtension{
                ResourceLimits: corev1.ResourceList{
                    extension.ResourceGPUMemory: allocMemory.DeepCopy(),
                },
                AllocationMode: extension.AsymmetricAllocation,
            },
        }

        plan.Devices = append(plan.Devices, alloc)
        remainingMemory.Sub(allocMemory)

        // 记录剩余资源
        remaining := candidate.Available[extension.ResourceGPUMemory].DeepCopy()
        remaining.Sub(allocMemory)
        plan.RemainingResources[candidate.Minor] = corev1.ResourceList{
            extension.ResourceGPUMemory: remaining,
        }
    }

    // 3. 检查是否满足请求
    if len(plan.Devices) < gpuCount || remainingMemory.Value() > 0 {
        return nil, fmt.Errorf("insufficient GPU memory")
    }

    // 4. 计算得分
    plan.TotalScore = p.calculatePlanScore(plan, candidates)
    plan.FragmentationScore = p.calculateFragmentationScore(plan)
    plan.IsSymmetric = p.isSymmetricPlan(plan)

    return plan, nil
}
```

##### 3.3 最佳适配算法 (BestFit)

```go
// bestFitAllocate 实现最佳适配算法
// 选择能够最紧密匹配请求的设备组合，最小化剩余碎片
func (p *Plugin) bestFitAllocate(
    candidates []*GPUCandidate,
    gpuCount int,
    totalMemory resource.Quantity,
    hint *extension.DeviceHint,
) (*AllocationPlan, error) {
    // 1. 生成所有可能的组合
    combinations := p.generateCombinations(candidates, gpuCount)

    var bestPlan *AllocationPlan
    minWaste := resource.MustParse("1000Ti") // 一个很大的值

    // 2. 遍历所有组合，找到最小浪费的组合
    for _, combo := range combinations {
        plan := p.tryAllocateCombination(combo, totalMemory, hint)
        if plan == nil {
            continue
        }

        // 计算总浪费（剩余资源）
        totalWaste := resource.NewQuantity(0, resource.BinarySI)
        for _, remaining := range plan.RemainingResources {
            mem := remaining[extension.ResourceGPUMemory]
            totalWaste.Add(mem)
        }

        // 选择浪费最小的方案
        if totalWaste.Cmp(minWaste) < 0 {
            minWaste = *totalWaste
            bestPlan = plan
        }
    }

    if bestPlan == nil {
        return nil, fmt.Errorf("no suitable allocation found")
    }

    return bestPlan, nil
}

// generateCombinations 生成所有可能的 GPU 组合
func (p *Plugin) generateCombinations(candidates []*GPUCandidate, k int) [][]*GPUCandidate {
    n := len(candidates)
    if k > n {
        return nil
    }

    var result [][]*GPUCandidate
    var current []*GPUCandidate

    var backtrack func(start int)
    backtrack = func(start int) {
        if len(current) == k {
            combo := make([]*GPUCandidate, k)
            copy(combo, current)
            result = append(result, combo)
            return
        }

        for i := start; i < n; i++ {
            current = append(current, candidates[i])
            backtrack(i + 1)
            current = current[:len(current)-1]
        }
    }

    backtrack(0)
    return result
}
```

##### 3.4 最差适配算法 (WorstFit) - 碎片优先

```go
// worstFitAllocate 实现最差适配算法（碎片优先）
// 选择剩余资源最多的设备组合，优先使用碎片化节点
func (p *Plugin) worstFitAllocate(
    candidates []*GPUCandidate,
    gpuCount int,
    totalMemory resource.Quantity,
    hint *extension.DeviceHint,
) (*AllocationPlan, error) {
    // 1. 按照碎片化得分降序排序（优先使用碎片化的 GPU）
    sort.Slice(candidates, func(i, j int) bool {
        // 碎片化得分高的排在前面
        if candidates[i].FragmentationScore != candidates[j].FragmentationScore {
            return candidates[i].FragmentationScore > candidates[j].FragmentationScore
        }
        // 如果碎片化得分相同，按可用内存降序排序
        memI := candidates[i].Available[extension.ResourceGPUMemory]
        memJ := candidates[j].Available[extension.ResourceGPUMemory]
        return memI.Cmp(memJ) > 0
    })

    // 2. 选择碎片化得分最高的 GPU 组合
    selectedCandidates := candidates
    if len(candidates) > gpuCount {
        selectedCandidates = candidates[:gpuCount]
    }

    // 3. 尝试分配
    plan := p.tryAllocateCombination(selectedCandidates, totalMemory, hint)
    if plan == nil {
        return nil, fmt.Errorf("insufficient GPU memory")
    }

    return plan, nil
}

// calculateFragmentationScore 计算单个 GPU 的碎片化得分
func (p *Plugin) calculateFragmentationScore(gpu *GPUCandidate) int32 {
    total := gpu.Total[extension.ResourceGPUMemory]
    available := gpu.Available[extension.ResourceGPUMemory]

    if total.IsZero() {
        return 0
    }

    // 碎片化得分 = (1 - 可用率) * 100 * 利用率调整因子
    utilization := float64(total.Value()-available.Value()) / float64(total.Value())
    availabilityRatio := float64(available.Value()) / float64(total.Value())

    // 如果可用率很低（< 20%）或很高（> 80%），碎片化得分较低
    // 中等可用率（20%-80%）的 GPU 碎片化得分较高
    var adjustmentFactor float64
    if availabilityRatio < 0.2 || availabilityRatio > 0.8 {
        adjustmentFactor = 0.5
    } else {
        adjustmentFactor = 1.0
    }

    score := int32((1 - availabilityRatio) * 100 * adjustmentFactor * utilization)

    return score
}
```

##### 3.5 均衡装箱算法 (Balanced)

```go
// balancedAllocate 实现均衡装箱算法
// 尝试均衡各设备的资源使用，减少未来碎片化
func (p *Plugin) balancedAllocate(
    candidates []*GPUCandidate,
    gpuCount int,
    totalMemory resource.Quantity,
    hint *extension.DeviceHint,
) (*AllocationPlan, error) {
    if len(candidates) < gpuCount {
        return nil, fmt.Errorf("not enough GPUs")
    }

    // 1. 选择利用率最均衡的 GPU 组合
    // 目标：选择 GPU 后，所有 GPU 的利用率方差最小

    var bestPlan *AllocationPlan
    minVariance := math.MaxFloat64

    combinations := p.generateCombinations(candidates, gpuCount)

    for _, combo := range combinations {
        // 尝试均衡分配
        plan := p.tryBalancedAllocation(combo, totalMemory, hint)
        if plan == nil {
            continue
        }

        // 计算分配后的利用率方差
        variance := p.calculateUtilizationVariance(plan, combo)

        if variance < minVariance {
            minVariance = variance
            bestPlan = plan
        }
    }

    if bestPlan == nil {
        return nil, fmt.Errorf("no suitable allocation found")
    }

    return bestPlan, nil
}

// tryBalancedAllocation 尝试均衡分配资源到给定的 GPU 组合
func (p *Plugin) tryBalancedAllocation(
    candidates []*GPUCandidate,
    totalMemory resource.Quantity,
    hint *extension.DeviceHint,
) *AllocationPlan {
    gpuCount := len(candidates)

    // 1. 计算每个 GPU 应该分配的目标内存
    // 目标：使所有 GPU 的利用率尽可能接近

    // 计算总可用内存
    totalAvailable := resource.NewQuantity(0, resource.BinarySI)
    for _, candidate := range candidates {
        mem := candidate.Available[extension.ResourceGPUMemory]
        totalAvailable.Add(mem)
    }

    // 检查是否有足够的内存
    if totalAvailable.Cmp(totalMemory) < 0 {
        return nil
    }

    // 2. 按比例分配
    plan := &AllocationPlan{
        Devices: make([]*extension.DeviceAllocation, 0, gpuCount),
        RemainingResources: make(map[int32]corev1.ResourceList),
    }

    remainingToAllocate := totalMemory.DeepCopy()

    for i, candidate := range candidates {
        availableMemory := candidate.Available[extension.ResourceGPUMemory]

        // 按比例计算应该分配的内存
        var allocMemory resource.Quantity
        if i == gpuCount-1 {
            // 最后一个 GPU 分配剩余的所有内存
            allocMemory = remainingToAllocate.DeepCopy()
        } else {
            // 按比例分配：(可用内存 / 总可用内存) * 总请求内存
            ratio := float64(availableMemory.Value()) / float64(totalAvailable.Value())
            allocValue := int64(float64(totalMemory.Value()) * ratio)
            allocMemory = *resource.NewQuantity(allocValue, resource.BinarySI)

            // 不能超过可用内存
            if allocMemory.Cmp(availableMemory) > 0 {
                allocMemory = availableMemory.DeepCopy()
            }
        }

        // 检查最小/最大资源限制
        if hint.MinResourcePerDevice != nil {
            minMemory := hint.MinResourcePerDevice[extension.ResourceGPUMemory]
            if allocMemory.Cmp(minMemory) < 0 {
                allocMemory = minMemory.DeepCopy()
            }
        }

        if hint.MaxResourcePerDevice != nil {
            maxMemory := hint.MaxResourcePerDevice[extension.ResourceGPUMemory]
            if allocMemory.Cmp(maxMemory) > 0 {
                allocMemory = maxMemory.DeepCopy()
            }
        }

        // 不能超过剩余需要分配的内存
        if allocMemory.Cmp(remainingToAllocate) > 0 {
            allocMemory = remainingToAllocate.DeepCopy()
        }

        alloc := &extension.DeviceAllocation{
            Minor: candidate.Minor,
            Resources: corev1.ResourceList{
                extension.ResourceGPUMemory: allocMemory,
            },
            Extension: &extension.DeviceAllocationExtension{
                ResourceLimits: corev1.ResourceList{
                    extension.ResourceGPUMemory: allocMemory.DeepCopy(),
                },
                AllocationMode: extension.AsymmetricAllocation,
            },
        }

        plan.Devices = append(plan.Devices, alloc)
        remainingToAllocate.Sub(allocMemory)

        // 记录剩余资源
        remaining := availableMemory.DeepCopy()
        remaining.Sub(allocMemory)
        plan.RemainingResources[candidate.Minor] = corev1.ResourceList{
            extension.ResourceGPUMemory: remaining,
        }
    }

    // 3. 检查是否满足请求
    if remainingToAllocate.Value() > 0 {
        return nil
    }

    // 4. 计算得分
    plan.TotalScore = p.calculatePlanScore(plan, candidates)
    plan.FragmentationScore = p.calculateFragmentationScore(plan)
    plan.IsSymmetric = p.isSymmetricPlan(plan)

    return plan
}

// calculateUtilizationVariance 计算分配后各 GPU 利用率的方差
func (p *Plugin) calculateUtilizationVariance(
    plan *AllocationPlan,
    candidates []*GPUCandidate,
) float64 {
    var utilizations []float64

    for i, alloc := range plan.Devices {
        candidate := candidates[i]
        total := candidate.Total[extension.ResourceGPUMemory]
        allocated := alloc.Resources[extension.ResourceGPUMemory]

        // 计算分配后的利用率
        currentUsed := total.DeepCopy()
        available := candidate.Available[extension.ResourceGPUMemory]
        currentUsed.Sub(available)
        currentUsed.Add(allocated)

        utilization := float64(currentUsed.Value()) / float64(total.Value())
        utilizations = append(utilizations, utilization)
    }

    // 计算方差
    mean := 0.0
    for _, u := range utilizations {
        mean += u
    }
    mean /= float64(len(utilizations))

    variance := 0.0
    for _, u := range utilizations {
        diff := u - mean
        variance += diff * diff
    }
    variance /= float64(len(utilizations))

    return variance
}
```

#### 4. 优先对称分配 (PreferSymmetric)

```go
// allocatePreferSymmetric 实现优先对称分配
// 首先尝试对称分配，如果失败则回退到非对称分配
func (p *Plugin) allocatePreferSymmetric(
    state *preFilterState,
    deviceRes *nodeDevice,
    hint *extension.DeviceHint,
    pod *corev1.Pod,
    nodeInfo *framework.NodeInfo,
) (apiext.DeviceAllocations, *framework.Status) {
    // 1. 首先尝试对称分配
    symmetricHint := hint.DeepCopy()
    symmetricHint.AllocationMode = extension.SymmetricAllocation

    allocations, status := p.allocateSymmetric(state, deviceRes, symmetricHint, pod, nodeInfo)
    if status.IsSuccess() {
        return allocations, status
    }

    // 2. 对称分配失败，回退到非对称分配
    klog.V(4).InfoS("Symmetric allocation failed, falling back to asymmetric allocation",
        "pod", klog.KObj(pod),
        "reason", status.Message())

    asymmetricHint := hint.DeepCopy()
    asymmetricHint.AllocationMode = extension.AsymmetricAllocation

    return p.allocateAsymmetric(state, deviceRes, asymmetricHint, pod, nodeInfo)
}
```

#### 5. 节点打分增强

```go
// scoreNodeForAsymmetricAllocation 为非对称分配场景下的节点打分
func (p *Plugin) scoreNodeForAsymmetricAllocation(
    ctx context.Context,
    state *framework.CycleState,
    pod *corev1.Pod,
    nodeName string,
) (int64, *framework.Status) {
    nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
    if err != nil {
        return 0, framework.AsStatus(err)
    }

    deviceRes := p.getNodeDevice(nodeName)
    if deviceRes == nil {
        return 0, nil
    }

    hint, err := extension.GetDeviceAllocateHints(pod.Annotations)
    if err != nil {
        return 0, framework.AsStatus(err)
    }

    gpuHint := hint[schedulingv1alpha1.GPU]
    if gpuHint == nil {
        return 0, nil
    }

    // 1. 计算碎片化得分
    fragmentationScore := p.calculateNodeFragmentationScore(deviceRes)

    // 2. 计算资源匹配度
    matchScore := p.calculateResourceMatchScore(deviceRes, state)

    // 3. 根据策略调整得分
    var score int64

    if gpuHint.PreferFragmented != nil && *gpuHint.PreferFragmented {
        // 优先碎片节点：碎片化得分越高，总分越高
        score = fragmentationScore*60 + matchScore*40
    } else {
        // 默认：资源匹配度优先
        score = matchScore*70 + (100-fragmentationScore)*30
    }

    return score, nil
}

// calculateNodeFragmentationScore 计算节点的碎片化得分
func (p *Plugin) calculateNodeFragmentationScore(deviceRes *nodeDevice) int64 {
    if len(deviceRes.devices) == 0 {
        return 0
    }

    var totalScore int64
    for _, gpu := range deviceRes.devices {
        candidate := &GPUCandidate{
            Minor:     gpu.Minor,
            Available: gpu.Resources,
            Total:     gpu.Total,
        }
        score := p.calculateFragmentationScore(candidate)
        totalScore += int64(score)
    }

    // 返回平均碎片化得分
    return totalScore / int64(len(deviceRes.devices))
}

// calculateResourceMatchScore 计算资源匹配度得分
func (p *Plugin) calculateResourceMatchScore(
    deviceRes *nodeDevice,
    state *framework.CycleState,
) int64 {
    preFilterState, err := getPreFilterState(state)
    if err != nil {
        return 0
    }

    gpuCount := int(preFilterState.skip[schedulingv1alpha1.GPU])
    totalMemory := preFilterState.podRequests[extension.ResourceGPUMemory]

    // 计算总可用内存
    totalAvailable := resource.NewQuantity(0, resource.BinarySI)
    validGPUCount := 0

    for _, gpu := range deviceRes.devices {
        if !gpu.Health {
            continue
        }
        mem := gpu.Resources[extension.ResourceGPUMemory]
        totalAvailable.Add(mem)
        validGPUCount++
    }

    if validGPUCount < gpuCount {
        return 0
    }

    // 匹配度 = min(可用内存 / 请求内存, 1.0) * 100
    ratio := float64(totalAvailable.Value()) / float64(totalMemory.Value())
    if ratio > 1.0 {
        // 可用内存越接近请求内存，得分越高
        // 避免过度分配导致资源浪费
        ratio = 1.0 + (1.0 / ratio)
    }

    score := int64(ratio * 100)
    if score > 100 {
        score = 100
    }

    return score
}
```

---

### 碎片整理设计

碎片整理是一个复杂且有风险的操作，需要谨慎设计。

#### 1. 碎片整理控制器

```go
// pkg/descheduler/defragmentation_controller.go

type DefragmentationController struct {
    client        kubernetes.Interface
    koordClient   koordinatorclientset.Interface
    config        *DefragmentationConfig
    metrics       *DefragmentationMetrics
}

type DefragmentationConfig struct {
    Enabled                    bool
    Schedule                   string
    FragmentationThreshold     float64
    MaxMigrationsPerCycle      int
    MaxMigrationsPerNode       int
    PodSelector                *metav1.LabelSelector
    ExcludedPods               []string
    DryRun                     bool
    MigrationTimeout           time.Duration
    NodeSelector               *metav1.LabelSelector
    PreferredTargetUtilization float64
}

type DefragmentationMetrics struct {
    TotalMigrations       int64
    SuccessfulMigrations  int64
    FailedMigrations      int64
    FragmentationReduced  float64
    LastRunTime           time.Time
}
```

#### 2. 碎片整理流程

```go
// Run 启动碎片整理控制器
func (c *DefragmentationController) Run(ctx context.Context) error {
    if !c.config.Enabled {
        klog.InfoS("Defragmentation controller is disabled")
        return nil
    }

    // 解析 Cron 表达式
    cronSchedule, err := cron.ParseStandard(c.config.Schedule)
    if err != nil {
        return fmt.Errorf("invalid cron schedule: %w", err)
    }

    ticker := time.NewTicker(time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return nil
        case now := <-ticker.C:
            // 检查是否到达执行时间
            next := cronSchedule.Next(c.metrics.LastRunTime)
            if now.After(next) {
                if err := c.runDefragmentation(ctx); err != nil {
                    klog.ErrorS(err, "Failed to run defragmentation")
                }
                c.metrics.LastRunTime = now
            }
        }
    }
}

// runDefragmentation 执行一次碎片整理
func (c *DefragmentationController) runDefragmentation(ctx context.Context) error {
    klog.InfoS("Starting defragmentation cycle")

    // 1. 获取需要整理的节点列表
    nodes, err := c.getFragmentedNodes(ctx)
    if err != nil {
        return fmt.Errorf("failed to get fragmented nodes: %w", err)
    }

    if len(nodes) == 0 {
        klog.InfoS("No fragmented nodes found")
        return nil
    }

    klog.InfoS("Found fragmented nodes", "count", len(nodes))

    // 2. 为每个节点生成迁移计划
    var allMigrations []*MigrationPlan
    for _, node := range nodes {
        plans, err := c.generateMigrationPlans(ctx, node)
        if err != nil {
            klog.ErrorS(err, "Failed to generate migration plans", "node", node.Name)
            continue
        }
        allMigrations = append(allMigrations, plans...)
    }

    if len(allMigrations) == 0 {
        klog.InfoS("No migration plans generated")
        return nil
    }

    // 3. 优先级排序
    sort.Slice(allMigrations, func(i, j int) bool {
        return allMigrations[i].Priority > allMigrations[j].Priority
    })

    // 4. 限制迁移数量
    if len(allMigrations) > c.config.MaxMigrationsPerCycle {
        allMigrations = allMigrations[:c.config.MaxMigrationsPerCycle]
    }

    // 5. 执行迁移
    if c.config.DryRun {
        klog.InfoS("Dry run mode, skipping actual migration", "plans", len(allMigrations))
        for _, plan := range allMigrations {
            klog.InfoS("Dry run migration plan",
                "pod", klog.KObj(plan.Pod),
                "sourceNode", plan.SourceNode,
                "targetNode", plan.TargetNode,
                "fragmentationReduction", plan.FragmentationReduction)
        }
        return nil
    }

    return c.executeMigrations(ctx, allMigrations)
}

// getFragmentedNodes 获取需要整理的碎片化节点
func (c *DefragmentationController) getFragmentedNodes(ctx context.Context) ([]*corev1.Node, error) {
    // 1. 获取所有节点
    nodeList, err := c.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
    if err != nil {
        return nil, err
    }

    // 2. 应用节点选择器
    var selectedNodes []*corev1.Node
    for i := range nodeList.Items {
        node := &nodeList.Items[i]

        if c.config.NodeSelector != nil {
            selector, err := metav1.LabelSelectorAsSelector(c.config.NodeSelector)
            if err != nil {
                return nil, err
            }
            if !selector.Matches(labels.Set(node.Labels)) {
                continue
            }
        }

        selectedNodes = append(selectedNodes, node)
    }

    // 3. 获取节点指标，计算碎片化得分
    var fragmentedNodes []*corev1.Node

    for _, node := range selectedNodes {
        // 获取节点的 Device CR
        device, err := c.koordClient.SchedulingV1alpha1().Devices().Get(ctx, node.Name, metav1.GetOptions{})
        if err != nil {
            klog.ErrorS(err, "Failed to get device", "node", node.Name)
            continue
        }

        // 获取节点指标
        nodeMetric, err := c.koordClient.SloV1alpha1().NodeMetrics().Get(ctx, node.Name, metav1.GetOptions{})
        if err != nil {
            klog.V(4).InfoS("Node metric not found", "node", node.Name)
            // 如果没有指标，手动计算碎片化得分
            fragmentationScore := c.calculateDeviceFragmentation(device)
            if fragmentationScore >= c.config.FragmentationThreshold {
                fragmentedNodes = append(fragmentedNodes, node)
            }
            continue
        }

        // 检查碎片化得分
        for _, df := range nodeMetric.Status.DeviceFragmentation {
            if df.DeviceType == string(schedulingv1alpha1.GPU) {
                score := float64(df.FragmentationScore) / 100.0
                if score >= c.config.FragmentationThreshold {
                    fragmentedNodes = append(fragmentedNodes, node)
                    break
                }
            }
        }
    }

    return fragmentedNodes, nil
}
```

#### 3. 迁移计划生成

```go
// MigrationPlan 表示一个迁移计划
type MigrationPlan struct {
    Pod                     *corev1.Pod
    SourceNode              string
    TargetNode              string
    Priority                int
    FragmentationReduction  float64
    EstimatedDowntime       time.Duration
}

// generateMigrationPlans 为给定节点生成迁移计划
func (c *DefragmentationController) generateMigrationPlans(
    ctx context.Context,
    node *corev1.Node,
) ([]*MigrationPlan, error) {
    // 1. 获取节点上的所有使用 GPU 的 Pod
    pods, err := c.getGPUPodsOnNode(ctx, node.Name)
    if err != nil {
        return nil, err
    }

    // 2. 过滤不允许迁移的 Pod
    var candidatePods []*corev1.Pod
    for _, pod := range pods {
        if c.isPodMigratable(pod) {
            candidatePods = append(candidatePods, pod)
        }
    }

    if len(candidatePods) == 0 {
        return nil, nil
    }

    // 3. 获取当前节点的碎片化得分
    currentFragmentation := c.calculateNodeFragmentation(node)

    // 4. 为每个候选 Pod 生成迁移计划
    var plans []*MigrationPlan

    for _, pod := range candidatePods {
        // 模拟迁移后的碎片化得分
        newFragmentation := c.simulateFragmentationAfterMigration(node, pod)

        // 计算碎片化改善程度
        reduction := currentFragmentation - newFragmentation

        // 只有改善程度足够大时才考虑迁移
        if reduction < 0.05 { // 至少改善 5%
            continue
        }

        // 查找合适的目标节点
        targetNode, err := c.findTargetNode(ctx, pod)
        if err != nil || targetNode == "" {
            klog.V(4).InfoS("No suitable target node found", "pod", klog.KObj(pod))
            continue
        }

        plan := &MigrationPlan{
            Pod:                    pod,
            SourceNode:             node.Name,
            TargetNode:             targetNode,
            Priority:               c.calculateMigrationPriority(pod, reduction),
            FragmentationReduction: reduction,
            EstimatedDowntime:      c.estimateDowntime(pod),
        }

        plans = append(plans, plan)
    }

    return plans, nil
}

// isPodMigratable 检查 Pod 是否允许迁移
func (c *DefragmentationController) isPodMigratable(pod *corev1.Pod) bool {
    // 1. 检查是否在排除列表中
    podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
    for _, excluded := range c.config.ExcludedPods {
        if excluded == podKey {
            return false
        }
    }

    // 2. 检查 Pod 选择器
    if c.config.PodSelector != nil {
        selector, err := metav1.LabelSelectorAsSelector(c.config.PodSelector)
        if err != nil {
            return false
        }
        if !selector.Matches(labels.Set(pod.Labels)) {
            return false
        }
    }

    // 3. 检查 Pod 注解
    if pod.Annotations != nil {
        // 用户可以通过注解明确禁止迁移
        if pod.Annotations["scheduling.koordinator.sh/defrag-allowed"] == "false" {
            return false
        }
    }

    // 4. 检查 Pod 状态
    if pod.Status.Phase != corev1.PodRunning {
        return false
    }

    // 5. 检查 PodDisruptionBudget
    // TODO: 实现 PDB 检查

    return true
}

// calculateMigrationPriority 计算迁移优先级
func (c *DefragmentationController) calculateMigrationPriority(
    pod *corev1.Pod,
    fragmentationReduction float64,
) int {
    priority := 0

    // 1. 基于碎片化改善程度
    priority += int(fragmentationReduction * 100)

    // 2. 低优先级 Pod 优先迁移
    if pod.Spec.Priority != nil {
        priority += (100 - int(*pod.Spec.Priority))
    } else {
        priority += 50 // 默认中等优先级
    }

    // 3. QoS 等级
    switch pod.Status.QOSClass {
    case corev1.PodQOSBestEffort:
        priority += 30
    case corev1.PodQOSBurstable:
        priority += 20
    case corev1.PodQOSGuaranteed:
        priority += 10
    }

    return priority
}
```

#### 4. 执行迁移

```go
// executeMigrations 执行迁移计划
func (c *DefragmentationController) executeMigrations(
    ctx context.Context,
    plans []*MigrationPlan,
) error {
    successCount := 0
    failCount := 0

    for _, plan := range plans {
        klog.InfoS("Executing migration",
            "pod", klog.KObj(plan.Pod),
            "sourceNode", plan.SourceNode,
            "targetNode", plan.TargetNode,
            "priority", plan.Priority)

        // 创建 PodMigrationJob
        migrationJob := &schedulingv1alpha1.PodMigrationJob{
            ObjectMeta: metav1.ObjectMeta{
                GenerateName: "defrag-migration-",
                Namespace:    plan.Pod.Namespace,
                Labels: map[string]string{
                    "app":                              "koordinator",
                    "component":                        "defragmentation",
                    "scheduling.koordinator.sh/source": "defragmentation-controller",
                },
            },
            Spec: schedulingv1alpha1.PodMigrationJobSpec{
                PodRef: &corev1.ObjectReference{
                    Namespace: plan.Pod.Namespace,
                    Name:      plan.Pod.Name,
                    UID:       plan.Pod.UID,
                },
                ReservationOptions: &schedulingv1alpha1.PodMigrateReservationOptions{
                    ReservationRef: nil,
                    Template:       nil,
                },
                TargetNodeName: plan.TargetNode,
                Mode:           schedulingv1alpha1.PodMigrationJobModeReservationFirst,
                TTL:            &metav1.Duration{Duration: c.config.MigrationTimeout},
            },
        }

        _, err := c.koordClient.SchedulingV1alpha1().PodMigrationJobs(plan.Pod.Namespace).Create(
            ctx, migrationJob, metav1.CreateOptions{})
        if err != nil {
            klog.ErrorS(err, "Failed to create PodMigrationJob",
                "pod", klog.KObj(plan.Pod))
            failCount++
            continue
        }

        successCount++

        // 等待一段时间，避免过快迁移导致集群不稳定
        time.Sleep(5 * time.Second)
    }

    // 更新指标
    c.metrics.TotalMigrations += int64(len(plans))
    c.metrics.SuccessfulMigrations += int64(successCount)
    c.metrics.FailedMigrations += int64(failCount)

    klog.InfoS("Migration cycle completed",
        "total", len(plans),
        "success", successCount,
        "failed", failCount)

    return nil
}
```

---

## 设计细节

### 1. 环境变量注入

为了支持应用程序感知每个 GPU 的实际可用资源，需要注入相关环境变量。

#### Koordlet 实现

```go
// pkg/koordlet/runtimehooks/hooks/gpu/gpu_asymmetric.go

// injectAsymmetricGPUEnv 为非对称分配的 Pod 注入环境变量
func (p *gpuPlugin) injectAsymmetricGPUEnv(
    proto protocol.HooksProtocol,
) error {
    containerCtx := proto.(*protocol.ContainerContext)
    if containerCtx == nil {
        return fmt.Errorf("container protocol is nil")
    }

    // 1. 获取 Pod 的 GPU 分配信息
    alloc, err := extension.GetDeviceAllocations(containerCtx.Request.PodAnnotations)
    if err != nil {
        return err
    }

    gpuAlloc, ok := alloc[schedulingv1alpha1.GPU]
    if !ok || len(gpuAlloc) == 0 {
        return nil
    }

    // 2. 检查是否是非对称分配
    isAsymmetric := false
    if len(gpuAlloc) > 0 && gpuAlloc[0].Extension != nil {
        isAsymmetric = gpuAlloc[0].Extension.AllocationMode == extension.AsymmetricAllocation
    }

    if !isAsymmetric {
        // 对称分配，使用默认逻辑
        return p.injectSymmetricGPUEnv(proto)
    }

    // 3. 构建 CUDA_VISIBLE_DEVICES
    var deviceIDs []string
    var memoryLimits []string

    for _, gpu := range gpuAlloc {
        deviceIDs = append(deviceIDs, fmt.Sprintf("%d", gpu.Minor))

        if gpu.Extension != nil && gpu.Extension.ResourceLimits != nil {
            memLimit := gpu.Extension.ResourceLimits[extension.ResourceGPUMemory]
            if !memLimit.IsZero() {
                // 转换为 MB
                memLimitMB := memLimit.Value() / (1024 * 1024)
                memoryLimits = append(memoryLimits, fmt.Sprintf("%d", memLimitMB))
            }
        }
    }

    // 4. 注入环境变量
    containerCtx.Response.AddContainerEnvs(map[string]string{
        "CUDA_VISIBLE_DEVICES": strings.Join(deviceIDs, ","),
        // 自定义环境变量，应用程序可以读取每个 GPU 的内存限制
        "KOORDINATOR_GPU_MEMORY_LIMITS": strings.Join(memoryLimits, ","),
        "KOORDINATOR_GPU_ALLOCATION_MODE": "asymmetric",
    })

    klog.V(5).InfoS("Injected asymmetric GPU environment variables",
        "pod", klog.KObj(containerCtx.Request.PodMeta),
        "container", containerCtx.Request.ContainerMeta.Name,
        "devices", strings.Join(deviceIDs, ","),
        "memoryLimits", strings.Join(memoryLimits, ","))

    return nil
}
```

### 2. Scheduler Plugin 修改

#### PreFilter

```go
// PreFilter 预处理阶段
func (p *Plugin) PreFilter(
    ctx context.Context,
    state *framework.CycleState,
    pod *corev1.Pod,
) (*framework.PreFilterResult, *framework.Status) {
    // 现有逻辑...

    // 检查是否使用非对称分配
    hint, err := extension.GetDeviceAllocateHints(pod.Annotations)
    if err != nil {
        return nil, framework.AsStatus(err)
    }

    gpuHint := hint[schedulingv1alpha1.GPU]
    if gpuHint != nil && gpuHint.AllocationMode == extension.AsymmetricAllocation {
        // 非对称分配模式
        preFilterState.allocationMode = extension.AsymmetricAllocation
        preFilterState.packingStrategy = gpuHint.PackingStrategy
        preFilterState.preferFragmented = gpuHint.PreferFragmented
        preFilterState.minResourcePerDevice = gpuHint.MinResourcePerDevice
        preFilterState.maxResourcePerDevice = gpuHint.MaxResourcePerDevice
    } else if gpuHint != nil && gpuHint.AllocationMode == extension.PreferSymmetric {
        preFilterState.allocationMode = extension.PreferSymmetric
        preFilterState.packingStrategy = gpuHint.PackingStrategy
    } else {
        // 默认对称分配
        preFilterState.allocationMode = extension.SymmetricAllocation
    }

    return nil, nil
}
```

#### Filter

```go
// Filter 过滤阶段
func (p *Plugin) Filter(
    ctx context.Context,
    cycleState *framework.CycleState,
    pod *corev1.Pod,
    nodeInfo *framework.NodeInfo,
) *framework.Status {
    // 现有逻辑...

    preFilterState, err := getPreFilterState(cycleState)
    if err != nil {
        return framework.AsStatus(err)
    }

    // 根据分配模式选择过滤逻辑
    switch preFilterState.allocationMode {
    case extension.AsymmetricAllocation:
        return p.filterAsymmetric(preFilterState, nodeInfo, pod)
    case extension.PreferSymmetric:
        // PreferSymmetric 在 Filter 阶段使用宽松的检查
        // 只要总资源足够即可通过
        return p.filterAsymmetricLoose(preFilterState, nodeInfo, pod)
    default:
        return p.filterSymmetric(preFilterState, nodeInfo, pod)
    }
}

// filterAsymmetric 非对称分配的过滤逻辑
func (p *Plugin) filterAsymmetric(
    state *preFilterState,
    nodeInfo *framework.NodeInfo,
    pod *corev1.Pod,
) *framework.Status {
    deviceRes := p.getNodeDevice(nodeInfo.Node().Name)
    if deviceRes == nil {
        return framework.NewStatus(framework.UnschedulableAndUnresolvable, "node device not found")
    }

    gpuCount := int(state.skip[schedulingv1alpha1.GPU])
    totalMemory := state.podRequests[extension.ResourceGPUMemory]

    // 1. 检查 GPU 数量
    availableGPUs := 0
    for _, gpu := range deviceRes.devices {
        if gpu.Health {
            availableGPUs++
        }
    }

    if availableGPUs < gpuCount {
        return framework.NewStatus(framework.Unschedulable,
            fmt.Sprintf("not enough GPUs: need %d, available %d", gpuCount, availableGPUs))
    }

    // 2. 检查总内存
    totalAvailable := resource.NewQuantity(0, resource.BinarySI)
    validGPUCount := 0

    for _, gpu := range deviceRes.devices {
        if !gpu.Health {
            continue
        }

        mem := gpu.Resources[extension.ResourceGPUMemory]

        // 应用最小资源限制
        if state.minResourcePerDevice != nil {
            minMem := state.minResourcePerDevice[extension.ResourceGPUMemory]
            if mem.Cmp(minMem) < 0 {
                continue
            }
        }

        totalAvailable.Add(mem)
        validGPUCount++

        if validGPUCount >= gpuCount {
            break
        }
    }

    if validGPUCount < gpuCount {
        return framework.NewStatus(framework.Unschedulable,
            "not enough GPUs meeting minimum resource requirement")
    }

    if totalAvailable.Cmp(totalMemory) < 0 {
        return framework.NewStatus(framework.Unschedulable,
            fmt.Sprintf("insufficient GPU memory: need %s, available %s",
                totalMemory.String(), totalAvailable.String()))
    }

    return nil
}
```

#### Reserve

```go
// Reserve 预留阶段
func (p *Plugin) Reserve(
    ctx context.Context,
    cycleState *framework.CycleState,
    pod *corev1.Pod,
    nodeName string,
) *framework.Status {
    preFilterState, err := getPreFilterState(cycleState)
    if err != nil {
        return framework.AsStatus(err)
    }

    nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
    if err != nil {
        return framework.AsStatus(err)
    }

    deviceRes := p.getNodeDevice(nodeName)
    if deviceRes == nil {
        return framework.NewStatus(framework.Error, "node device not found")
    }

    // 获取 hint
    hint, err := extension.GetDeviceAllocateHints(pod.Annotations)
    if err != nil {
        return framework.AsStatus(err)
    }

    gpuHint := hint[schedulingv1alpha1.GPU]

    // 根据分配模式执行分配
    var allocations extension.DeviceAllocations
    var status *framework.Status

    switch preFilterState.allocationMode {
    case extension.AsymmetricAllocation:
        allocations, status = p.allocateAsymmetric(preFilterState, deviceRes, gpuHint, pod, nodeInfo)
    case extension.PreferSymmetric:
        allocations, status = p.allocatePreferSymmetric(preFilterState, deviceRes, gpuHint, pod, nodeInfo)
    default:
        allocations, status = p.allocateSymmetric(preFilterState, deviceRes, gpuHint, pod, nodeInfo)
    }

    if !status.IsSuccess() {
        return status
    }

    // 保存分配结果到 cycleState
    cycleState.Write(allocStateKey, &allocState{allocations: allocations})

    // 更新节点设备缓存
    p.updateNodeDeviceCache(nodeName, allocations)

    return nil
}
```

### 3. 碎片化指标收集

#### MetricsAdvisor 实现

```go
// pkg/koordlet/metricsadvisor/device_fragmentation_collector.go

type DeviceFragmentationCollector struct {
    deviceManager device.DeviceManager
    metricCache   metriccache.MetricCache
}

// collectDeviceFragmentation 收集设备碎片化指标
func (c *DeviceFragmentationCollector) collectDeviceFragmentation() error {
    // 1. 获取所有 GPU 设备
    devices := c.deviceManager.GetGPUDevices()
    if len(devices) == 0 {
        return nil
    }

    // 2. 计算碎片化得分
    var totalScore int32
    var fragmentedCount int
    var totalDevices int
    var totalMemory, usedMemory, maxContiguous resource.Quantity

    for _, dev := range devices {
        totalDevices++

        total := dev.Total[extension.ResourceGPUMemory]
        used := dev.Used[extension.ResourceGPUMemory]
        available := dev.Available[extension.ResourceGPUMemory]

        totalMemory.Add(total)
        usedMemory.Add(used)

        // 更新最大连续可用内存
        if available.Cmp(maxContiguous) > 0 {
            maxContiguous = available.DeepCopy()
        }

        // 计算碎片化得分
        score := c.calculateFragmentationScore(total, used, available)
        totalScore += score

        if score > 30 { // 碎片化得分超过 30 认为是碎片化
            fragmentedCount++
        }
    }

    // 3. 计算平均利用率
    avgUtilization := 0.0
    if !totalMemory.IsZero() {
        avgUtilization = float64(usedMemory.Value()) / float64(totalMemory.Value())
    }

    // 4. 创建碎片化指标
    metric := &slov1alpha1.DeviceFragmentationMetric{
        DeviceType:          string(schedulingv1alpha1.GPU),
        FragmentationScore:  totalScore / int32(totalDevices),
        TotalDevices:        totalDevices,
        FragmentedDevices:   fragmentedCount,
        AvgUtilization:      avgUtilization,
        MaxContiguousMemory: maxContiguous,
        UpdateTime:          metav1.Now(),
    }

    // 5. 保存到缓存
    c.metricCache.SetDeviceFragmentation(metric)

    return nil
}

// calculateFragmentationScore 计算碎片化得分
func (c *DeviceFragmentationCollector) calculateFragmentationScore(
    total, used, available resource.Quantity,
) int32 {
    if total.IsZero() {
        return 0
    }

    utilization := float64(used.Value()) / float64(total.Value())
    availabilityRatio := float64(available.Value()) / float64(total.Value())

    // 碎片化得分计算逻辑：
    // - 如果可用率很低（< 20%）或很高（> 80%），碎片化得分较低
    // - 中等可用率（20%-80%）的 GPU 碎片化得分较高
    // - 利用率越高，碎片化得分越高

    var adjustmentFactor float64
    if availabilityRatio < 0.2 || availabilityRatio > 0.8 {
        adjustmentFactor = 0.5
    } else {
        adjustmentFactor = 1.0
    }

    score := int32((1 - availabilityRatio) * 100 * adjustmentFactor * utilization)

    return score
}
```

---

## 实现阶段

建议分阶段实现本提案，以降低风险并便于测试验证。

### 阶段 1: 核心 API 和基础分配算法 (1-2 周)

**目标**: 支持基本的非对称分配功能

**任务**:
1. API 扩展:
   - 添加 `DeviceAllocationMode`、`DevicePackingStrategy` 等类型定义
   - 扩展 `DeviceHint` 和 `DeviceAllocation` 结构
   - 运行 `make generate manifests` 生成代码
2. 实现 FirstFit 算法:
   - 实现 `allocateAsymmetric` 主流程
   - 实现 `firstFitAllocate` 算法
   - 实现分配验证逻辑
3. Scheduler Plugin 修改:
   - 修改 PreFilter 阶段，支持非对称分配模式检测
   - 修改 Filter 阶段，支持非对称分配过滤
   - 修改 Reserve 阶段，支持非对称分配执行
4. 环境变量注入:
   - 实现 Koordlet 中的 `injectAsymmetricGPUEnv`
   - 支持 `KOORDINATOR_GPU_MEMORY_LIMITS` 等环境变量
5. 测试:
   - 单元测试
   - 集成测试（基本场景）

**验收标准**:
- 用户可以通过注解指定 `allocationMode: "Asymmetric"`
- Pod 可以成功调度到具有不均匀 GPU 内存的节点
- 环境变量正确注入到容器中

### 阶段 2: 高级分配算法和打分增强 (1-2 周)

**目标**: 支持多种分配策略，优化调度质量

**任务**:
1. 实现 BestFit 算法
2. 实现 WorstFit 算法（碎片优先）
3. 实现 Balanced 算法
4. 实现 PreferSymmetric 模式
5. 增强 Score 插件:
   - 实现 `scoreNodeForAsymmetricAllocation`
   - 支持碎片优先打分
6. 测试:
   - 各算法的单元测试
   - 对比测试（不同算法的效果对比）

**验收标准**:
- 用户可以通过 `packingStrategy` 选择不同的分配策略
- 不同策略产生不同的调度结果
- 碎片优先策略确实优先选择碎片化节点

### 阶段 3: 碎片化指标收集 (1 周)

**目标**: 收集和报告碎片化指标

**任务**:
1. 扩展 NodeMetric API:
   - 添加 `DeviceFragmentationMetric` 结构
   - 运行 `make generate manifests`
2. 实现 Koordlet 中的碎片化指标收集:
   - 实现 `DeviceFragmentationCollector`
   - 定期收集和上报指标
3. 更新 SLO-Controller:
   - 在 NodeMetric 中记录碎片化指标
4. 测试:
   - 验证指标正确性
   - 验证指标更新频率

**验收标准**:
- NodeMetric CR 中包含碎片化指标
- 指标能够准确反映节点的碎片化程度
- 指标更新及时

### 阶段 4: 碎片整理功能 (2-3 周)

**目标**: 实现可选的碎片整理机制

**任务**:
1. 扩展 ClusterColocationProfile API:
   - 添加 `DeviceDefragmentationSpec` 配置
   - 运行 `make generate manifests`
2. 实现碎片整理控制器:
   - 实现 `DefragmentationController`
   - 实现碎片化节点检测
   - 实现迁移计划生成
   - 实现迁移执行
3. 集成到 Descheduler:
   - 在 Descheduler 中启动碎片整理控制器
4. 测试:
   - Dry-run 模式测试
   - 小规模迁移测试
   - 安全性测试（验证保护机制）

**验收标准**:
- 管理员可以配置碎片整理策略
- 碎片整理可以按计划自动执行
- 迁移过程安全可控
- Dry-run 模式正常工作

### 阶段 5: 文档和示例 (1 周)

**目标**: 提供完整的文档和示例

**任务**:
1. 用户文档:
   - API 参考
   - 使用指南
   - 最佳实践
2. 运维文档:
   - 碎片整理配置指南
   - 故障排查指南
3. 示例:
   - 各种使用场景的 YAML 示例
   - 端到端示例
4. 设计文档:
   - 架构设计
   - 算法详解

---

## 测试计划

### 单元测试

1. **分配算法测试**:
   ```go
   func TestFirstFitAllocate(t *testing.T) {
       // 测试基本场景
       // 测试边界情况
       // 测试约束条件
   }

   func TestBestFitAllocate(t *testing.T) { ... }
   func TestWorstFitAllocate(t *testing.T) { ... }
   func TestBalancedAllocate(t *testing.T) { ... }
   ```

2. **碎片化计算测试**:
   ```go
   func TestCalculateFragmentationScore(t *testing.T) {
       // 测试各种碎片化场景
   }
   ```

3. **API 测试**:
   ```go
   func TestDeviceHintValidation(t *testing.T) {
       // 测试 API 验证逻辑
   }
   ```

### 集成测试

1. **基本调度测试**:
   - 创建一个节点，GPU 内存不均匀
   - 创建一个 Pod，使用非对称分配
   - 验证 Pod 成功调度
   - 验证环境变量正确注入

2. **多算法对比测试**:
   - 使用相同的节点和 Pod 配置
   - 分别测试不同的分配策略
   - 对比调度结果和碎片化影响

3. **碎片整理测试**:
   - 创建碎片化的节点
   - 配置碎片整理策略
   - 验证迁移计划生成
   - 验证迁移执行（dry-run）

### 端到端测试

1. **真实场景模拟**:
   - 搭建测试集群（多节点）
   - 部署多个 GPU 工作负载
   - 模拟碎片化场景
   - 验证非对称分配效果
   - 验证碎片整理效果

2. **压力测试**:
   - 大量 Pod 并发调度
   - 验证调度器性能
   - 验证分配算法稳定性

3. **故障场景测试**:
   - GPU 故障
   - 节点故障
   - 迁移失败
   - 验证错误处理和恢复

### 性能测试

1. **分配算法性能**:
   - 测试不同算法的时间复杂度
   - 测试不同 GPU 数量下的性能
   - 目标：单次分配 < 100ms

2. **调度延迟**:
   - 测试非对称分配对调度延迟的影响
   - 与对称分配对比
   - 目标：延迟增加 < 20%

3. **碎片整理性能**:
   - 测试碎片整理的执行时间
   - 测试对集群的影响
   - 目标：整理周期 < 10 分钟

---

## 风险和缓解措施

### 风险 1: 调度延迟增加

**描述**: 非对称分配算法（特别是 BestFit）需要遍历多个组合，可能增加调度延迟。

**影响**: 高
**可能性**: 中

**缓解措施**:
1. **默认使用 FirstFit**: 大多数场景使用性能最优的 FirstFit 算法
2. **限制组合数量**: 在 BestFit 算法中限制遍历的组合数量
3. **缓存优化**: 缓存候选 GPU 列表，避免重复计算
4. **性能监控**: 添加 metrics 监控分配时间，及时发现性能问题

**验收标准**:
- 单次分配时间 < 100ms (P95)
- 对比对称分配，延迟增加 < 20%

### 风险 2: 应用程序兼容性

**描述**: 某些应用程序可能假设所有 GPU 具有相同的资源量，非对称分配可能导致兼容性问题。

**影响**: 高
**可能性**: 中

**缓解措施**:
1. **默认对称分配**: 保持向后兼容，默认使用对称分配
2. **明确声明**: 要求用户明确声明使用非对称分配（通过注解）
3. **环境变量支持**: 提供 `KOORDINATOR_GPU_MEMORY_LIMITS` 环境变量，让应用感知每个 GPU 的限制
4. **文档说明**: 在文档中明确说明兼容性要求和最佳实践
5. **渐进式推广**: 先在开发/测试环境验证，再推广到生产环境

**验收标准**:
- 现有工作负载不受影响（默认对称分配）
- 文档清晰说明兼容性要求
- 提供应用程序适配指南

### 风险 3: 碎片整理导致服务中断

**描述**: Pod 迁移会导致服务短暂不可用，可能影响业务。

**影响**: 高
**可能性**: 高

**缓解措施**:
1. **Opt-in 机制**: 碎片整理默认关闭，需要管理员明确启用
2. **PodSelector**: 只迁移允许迁移的 Pod（通过标签选择）
3. **排除列表**: 支持明确排除某些关键 Pod
4. **迁移限流**:
   - 限制单次整理周期的迁移数量
   - 限制单个节点的迁移数量
   - 迁移之间添加延迟
5. **PDB 检查**: 尊重 PodDisruptionBudget 限制
6. **Dry-run 模式**: 支持模拟运行，验证迁移计划
7. **优先级控制**: 优先迁移低优先级 Pod
8. **时间窗口**: 只在业务低谷期执行（通过 Cron 配置）

**验收标准**:
- 支持 Dry-run 模式
- 支持 PodSelector 和排除列表
- PDB 限制得到尊重
- 迁移过程可以被监控和控制

### 风险 4: 碎片整理效果不佳

**描述**: 迁移 Pod 后碎片化程度没有明显改善，或者很快又变碎片化。

**影响**: 中
**可能性**: 中

**缓解措施**:
1. **碎片化指标**: 准确计算碎片化得分，只有改善明显时才迁移
2. **效果评估**: 迁移前模拟碎片化改善程度，只执行高价值迁移
3. **最小改善阈值**: 只有碎片化改善超过一定阈值（如 5%）才迁移
4. **目标利用率**: 设置期望的目标利用率，整理后尽量接近该值
5. **持续优化**: 根据实际效果调整算法和策略

**验收标准**:
- 迁移后碎片化得分明显降低
- 碎片化改善程度 > 设定阈值
- 支持效果评估和报告

### 风险 5: 迁移失败

**描述**: Pod 迁移过程中可能失败（资源不足、调度失败、启动失败等）。

**影响**: 中
**可能性**: 中

**缓解措施**:
1. **目标节点验证**: 迁移前验证目标节点是否有足够资源
2. **超时控制**: 设置迁移超时时间，避免长时间阻塞
3. **失败重试**: 支持有限次数的重试
4. **回滚机制**: 迁移失败时保持原 Pod 继续运行
5. **监控告警**: 迁移失败时发送告警
6. **失败统计**: 记录失败原因和统计信息，用于优化策略

**验收标准**:
- 迁移失败不会导致 Pod 丢失
- 支持超时和重试
- 失败原因可追溯

### 风险 6: API 变更导致兼容性问题

**描述**: 新增的 API 字段可能与现有系统不兼容。

**影响**: 中
**可能性**: 低

**缓解措施**:
1. **可选字段**: 所有新字段都是可选的（`+optional`）
2. **默认值**: 不设置时使用现有行为（对称分配）
3. **版本控制**: 使用 API 版本控制，支持渐进式升级
4. **向后兼容测试**: 测试新旧版本混合部署场景

**验收标准**:
- 现有工作负载不受影响
- 新旧版本可以共存
- API 版本控制正确

---

## 替代方案

### 方案 1: 只支持对称分配，不支持非对称

**优点**:
- 简单，不需要修改现有逻辑
- 性能最优
- 兼容性最好

**缺点**:
- 无法解决碎片化问题
- 资源利用率低
- 无法满足用户需求

**结论**: 不采纳，无法解决核心问题。

### 方案 2: 修改 Kubernetes 原生资源模型

**描述**: 修改 Kubernetes 原生资源请求/限制模型，支持指定每个 GPU 的资源量。

**优点**:
- 更标准化
- 更通用

**缺点**:
- 需要修改 Kubernetes 核心代码
- 社区接受度不确定
- 实施周期长
- 侵入性强

**结论**: 不采纳，成本太高且不可控。

### 方案 3: 用户手动指定每个 GPU 的资源量

**描述**: 允许用户通过注解精确指定每个 GPU 应该分配多少资源。

**示例**:
```yaml
annotations:
  scheduling.koordinator.sh/gpu-allocation: |
    {
      "0": {"memory": "10Gi"},
      "1": {"memory": "30Gi"}
    }
```

**优点**:
- 最大灵活性
- 用户完全掌控

**缺点**:
- 用户负担重
- 容易出错
- 需要用户了解节点的 GPU 配置

**结论**: 不采纳作为主要方案，但可以作为高级选项提供。

### 方案 4: 只实现碎片整理，不支持非对称分配

**描述**: 通过重调度来整理碎片，但调度时仍使用对称分配。

**优点**:
- 实现简单
- 兼容性好

**缺点**:
- 无法立即解决调度失败问题
- 碎片整理有风险和成本
- 治标不治本

**结论**: 不采纳，非对称分配是根本解决方案。

---

## 总结

本提案提供了一套完整的 GPU 非对称分配和碎片整理解决方案，包括：

1. **API 设计**:
   - 扩展 `DeviceHint` 支持分配模式和装箱策略
   - 扩展 `DeviceAllocation` 记录实际分配
   - 新增 `DeviceDefragmentationSpec` 配置碎片整理
   - 新增 `DeviceFragmentationMetric` 记录碎片化指标

2. **分配算法**:
   - FirstFit: 快速，性能最优
   - BestFit: 精确匹配，最小化碎片
   - WorstFit: 碎片优先，优先使用碎片节点
   - Balanced: 均衡分配，减少未来碎片化
   - PreferSymmetric: 优先对称，失败回退

3. **碎片整理**:
   - 定期评估碎片化程度
   - 生成迁移计划
   - 安全可控的迁移执行
   - 多重保护机制

4. **向后兼容**:
   - 默认行为不变
   - 所有新功能都是 Opt-in
   - 新旧版本可以共存

5. **分阶段实现**:
   - 阶段 1: 核心功能（2 周）
   - 阶段 2: 高级功能（2 周）
   - 阶段 3: 指标收集（1 周）
   - 阶段 4: 碎片整理（3 周）
   - 阶段 5: 文档示例（1 周）

通过本提案，可以显著提高 GPU 资源利用率，减少调度失败，降低成本。
