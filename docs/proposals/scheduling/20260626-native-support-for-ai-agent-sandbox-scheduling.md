---
title: AI Agent 编排与 Sandbox 调度原生支持
authors:
  - "@tan90github"
reviewers:
  - "@saintube"
  - "@ZiMengSheng"
creation-date: 2026-06-26
last-updated: 2026-06-30
status: provisional
see-also:
  - "https://github.com/koordinator-sh/koordinator/issues/2879"
  - "docs/proposals/scheduling/20220609-resource-reservation.md"
  - "docs/proposals/scheduling/20221227-node-resource-reservation.md"
  - "docs/proposals/koordlet/20220615-qos-manager.md"
---

# AI Agent 编排与 Sandbox 调度原生支持

本文对应 issue [#2879](https://github.com/koordinator-sh/koordinator/issues/2879)。当前阶段以 issue #2879 作为范围的一手事实源；本地调研和外部 sandbox 项目只用于验证集成可行性，不作为扩大范围的依据。

## 摘要

AI Agent 系统带来了一种新的 Kubernetes 调度形态：在工具执行、多 Agent 协作、SWE-bench 这类评测场景中，系统会频繁创建、复用、销毁大量短生命周期 sandbox 环境。这类 sandbox 往往高度相似、突发性强、启动延迟敏感，而且可能来自不同上游 API 或不同运行形态。

社区讨论里，Koordinator 需要回答三个问题：已有 sandbox API 如何接入 Koordinator；大量短生命周期 sandbox 突发创建时如何优化 Kubernetes 调度框架和 APIServer / etcd 高并发性能；后续 sandbox 隔离、预热和诊断能力如何保留扩展面。本 proposal 第一版先收敛到一期重点：

1. **接入 Koordinator**：Pod sandbox 直接进入 koord-scheduler；非 Pod 类型的 sandbox 通过 Reservation 进入 Koordinator 资源账本。
2. **快速调度**：为显式标记的 sandbox workload 建立轻量调度路径，先拆解 Kubernetes 调度框架、APIServer / etcd 在高并发场景下的性能问题。

核心判断是：Koordinator 不新增 Sandbox CRD，也不替代上游 sandbox API。只要 sandbox API 最终创建 Kubernetes Pod，就以 Pod / PodTemplate 作为接入边界；如果 sandbox 消耗 Koordinator 管理节点上的资源但不创建 Pod，则由 adapter / glue layer 将外部 allocation 映射为 Koordinator Reservation。

## 术语

- **Sandbox**：Agent 执行代码、工具或评测任务时使用的隔离执行环境。在 Kubernetes 中可能表现为 Pod，也可能由外部 runtime / controller 在节点上创建非 Pod 形态的实例。
- **Reservation 转换层**：把上游 sandbox API 或非 Pod 类型的 sandbox allocation 转成 Koordinator 可理解的 Pod 或 Reservation 的 adapter 层。
- **Sandbox fast path**：只对显式标记的 sandbox workload 生效的轻量调度路径，用于减少通用调度 pipeline 中不必要的计算和写入。

## 动机

issue #2879 提议 Koordinator 为 AI Agent 场景提供原生支持，利用其在混部和细粒度调度方面的优势，优化 sandbox 环境中的资源效率。这里的核心问题不是 Koordinator 要重新定义 AI Agent 编排模型，而是 Koordinator 如何把已有调度、资源记账和后续隔离增强能力接入 sandbox 场景。

Koordinator 在这个问题上有几类天然优势：

- **Reservation 能承接非 Pod 类型的资源记账**：很多 sandbox API 最终会落成 Pod，可以直接进入 koord-scheduler；但仍可能存在 node-local VM、host process、Wasm runtime 等非 Pod 形态。只要它们消耗 Koordinator 管理节点上的资源，Reservation 可以作为对象级转换层，避免资源不可见。
- **koord-scheduler 有调度 pipeline 和插件扩展基础**：AI sandbox 请求通常高并发、短生命周期、同质化程度高，普通调度路径容易重复计算。Koordinator 可以先通过 scheduler profile、已有扩展点和 Reservation 插件建立 fast path，再逐步评估更深的并发调度、节点分池、冲突仲裁等优化。
- **QoS 能力可以作为后续承接面**：sandbox 可能使用不同 runtime、不同优先级、不同资源隔离要求。Koordinator 已有 QoS class 和 `ClusterColocationProfile`，可以在接入闭环稳定后继续承接 QoS 分类与隔离增强。
- **Reservation 可以支撑后续预热容量可见性**：预热由上层 sandbox workload 表达时，Koordinator 可以通过 Reservation 表达 warm slot capacity，让容量进入 scheduler cache，避免被重复分配。该能力保留在后续阶段，不作为一期主线。

因此，本 proposal 的动机是把 Koordinator 的 Reservation 和调度 pipeline 连接成一条面向 sandbox 的接入与快速调度路径；一期先打通接入和性能基线。

### 目标

本 proposal 的第一阶段目标是把 sandbox 接入 Koordinator 的完整链路跑通，并给出高并发调度优化的分阶段落地路径。issue #2879 中的 warm pipeline、QoS 增强、自动诊断仍属于完整问题空间，但不是本次 proposal 的设计重点。

- 设计 Pod sandbox 和非 Pod sandbox 接入 Koordinator 的路径。
- 重点设计非 Pod 类型的 sandbox 到 Reservation 的胶水层，使 Koordinator 能感知这类 sandbox 的资源占用。
- 跑通最小闭环：上游 sandbox API / adapter → Koordinator 调度面 → koord-scheduler → sandbox 启动或记账成功 → sandbox 退出后资源释放。
- 分阶段落地 sandbox 调度性能优化：一期实现极简调度路径和性能基线，二期落地 bind / API 写入优化，三期推进调度框架并发化、节点分池与冲突仲裁。

### 非目标 / 后续工作

- 不新增 Sandbox CRD；Koordinator 适配上游 sandbox API，并把它们接入现有 Pod 和 Reservation 调度面。
- 不实现 sandbox runtime、网络隔离、文件系统隔离或 VM 生命周期管理。
- 不在本次 proposal 深入设计 warm capacity、睡眠唤醒、QoS 深度接入或复杂自动诊断。
- 不管理 Koordinator 不可见的外部 sandbox；只有运行在 Koordinator 管理节点上、或由明确 adapter 上报的资源，才纳入调度账本。

## 方案

### 用户故事

**Story 1：Pod 形态 sandbox 接入**

Agent 平台通过 `kubernetes-sigs/agent-sandbox`、PodTemplate 或直接 Pod 创建 sandbox。Koordinator 需要识别这些 Pod，并通过 `schedulerName`、label / annotation、scheduler profile 等机制把它们路由到 sandbox fast path。

**Story 2：非 Pod sandbox 记账**

外部 runtime 在 Koordinator 管理节点上创建 node-local VM 或 host process sandbox，但不创建 Kubernetes Pod。adapter 需要把这类 allocation 映射为对象级 Reservation。目标是保证调度账本不超卖，并在 sandbox 退出后释放账本。

**Story 3：SWE-bench 风格评测**

评测平台一次启动数百个短生命周期 sandbox Pod。这些 Pod 使用相同镜像和相似资源规格。调度器应该避免为每个 sandbox 重复执行相同 filter / score 计算，并用 benchmark 拆解各阶段耗时。

Warm sandbox pool、QoS 分类和复杂诊断是后续阶段故事，本次只保证接入层不会阻断这些扩展。

### 设计原则

本 proposal 用 issue #2879 的四个方向作为问题边界，但不把它们提前展开成完整功能清单。一期设计先遵循以下原则：

- **适配优先**：优先接入上游 sandbox API 和 PodTemplate，不新增 Sandbox CRD。
- **现有调度面优先**：Pod 形态走 Pod fast path；非 Pod 类型的 sandbox 通过 Reservation 表达；warm capacity 作为后续扩展评估。
- **显式启用**：sandbox-specific 调度行为只作用于明确标记的 sandbox workload，不影响普通 Pod。
- **正确性优先**：fast path、batching、equivalence reuse 在不确定时必须回退普通调度路径。
- **可观测**：性能优化需要能用 queue、filter / score、reserve、bind、APIServer request 等指标拆解验证。

### 分期范围

阶段性设计讨论把一期范围收敛为“接入”和“快速调度”。这与 issue #2879 的 A-D outcomes 不冲突：issue 描述的是完整问题空间，一期实现需要先把可验证闭环做小。

| Phase | Scope | Why now | Explicitly deferred |
|---|---|---|---|
| P0 / Proposal | 明确 sandbox 接入边界、Pod / 非 Pod 胶水层、调度性能优化落地路线。 | 先让社区确认 Koordinator 只做调度接入层，不定义新的 sandbox 资源模型。 | 具体 controller / API 字段名。 |
| P1 / 接入闭环 | Pod sandbox 通过 label / schedulerName 接入；非 Pod 类型的 sandbox 通过 Reservation 记账；跑通 sandbox 创建、调度、启动 / 记账、释放。 | 第一阶段先证明 sandbox 可以通过 Koordinator 跑起来。 | 预热、QoS 接入、复杂诊断。 |
| P2 / 极简调度路径 | 为 sandbox workload 落地最小调度路径，先用 scheduler profile、plugin 裁剪、候选节点缩小等方式建立性能基线。 | 先验证“砍掉非必要模块”能带来多少收益。 | 批量 API 语义、调度框架重构。 |
| P3 / Bind 与 API 写入优化 | 在调研 Kubernetes bind、Koordinator Reservation bind、Volcano agent-scheduler binder 等实现后，落地 bind / API 写入优化。 | 高并发 sandbox 调度的瓶颈很可能出现在 binding cycle、对象 patch、APIServer / etcd 写入。 | 全局并发调度。 |
| P4 / 调度框架优化 | 推进并发调度、节点分池、同质化调度、调度冲突仲裁等机制的实现与验证。 | 形成 Koordinator 在 sandbox 场景下的长期性能优势。 | warm capacity、QoS 增强、sandbox 故障感知作为独立后续设计。 |

### 架构

Koordinator 应把 sandbox support 设计为围绕现有 Pod、Reservation 和调度扩展机制的一组集成。

```text
Agent orchestrator / sandbox API
        |
        | Sandbox, SandboxClaim, PodTemplate, or external allocation
        v
Sandbox API adapter / Reservation conversion layer
        |
        | labels, annotations, schedulerName, Reservation templates
        v
Koordinator scheduling surface
        |
        +-- Pod fast path for Pod-shaped sandboxes
        +-- Reservation-backed accounting for non-Pod allocations
        +-- later-phase extensions: warm capacity / QoS / diagnosis
        v
koord-scheduler / koordlet / existing Kubernetes runtime path
```

标准调度面：

- **Pod**：适用于通过 Kubernetes API 创建的 sandbox，以及 gVisor、Kata Containers、Kata with Firecracker hypervisor、Pod-based Wasm RuntimeClass 等以 Pod 形态进入 K8s 的 runtime。一期主体走这条路径。
- **Reservation**：一期重点用于高 churn 非 Pod sandbox 的对象级记账；后续可继续评估用它表达 concrete sandbox Pod 分配前需要被调度器看见的 warm slot。

### 现有代码依据

本 proposal 中关于接入路径和性能优化边界的判断基于以下代码事实：

- Koordinator Reservation 是 cluster-scoped API，`spec.template` 是 `corev1.PodTemplateSpec`，会像 Pod 一样被调度；`spec.template.spec.nodeName` 可以固定节点；`spec.owners` 必填且至少一个；`spec.unschedulable` 用于控制是否允许新 Pod 分配该 Reservation；`status.nodeName`、`status.allocatable`、`status.allocated` 表达调度结果和账本。
- `pkg/util/reservation.NewReservePod` 会把 Reservation 转成 scheduler 内部可见的 fake Pod，并用 annotation 标记 reserve pod。Reservation 插件实现了 PreFilter、Filter、Score、Reserve、PreBind、Bind 以及 Reservation 扩展点，说明 Reservation 已经深度接入 koord-scheduler pipeline。
- Koordinator `FrameworkExtender` 已有 `FindOneNodePlugin` 和 `PreferNodesPlugin`：它们可以在 PreFilter 后把候选节点缩到单节点或优先节点，再走正常 Filter 校验。这可以作为 sandbox fast path 缩小候选集的现有扩展点。
- `DefaultPreBind` 通过 `PatchPodSafe` / `PatchReservationSafe` 对 Pod / Reservation 做 PreBind patch；Reservation bind 通过 `UpdateStatus` 把 Reservation 标为 Available。当前实现没有面向 sandbox 的批量写入语义，因此 bind / API pressure 优化必须作为后续设计，不应写成现有能力。
- Kubernetes scheduler 的正常流程是 scheduling cycle 成功后异步进入 binding cycle；DefaultBinder 仍通过 Pod `binding` 子资源逐个绑定 Pod。Koordinator 当前沿用该语义，所以 Pod-shaped sandbox 第一期不能假设存在批量 Pod bind API。
- 本地 `kubernetes-sigs/agent-sandbox` 代码中，`Sandbox.Spec.PodTemplate.Spec` 是 `corev1.PodSpec`，controller 最终创建原生 Pod。对这类 API，Koordinator 最稳妥的边界是 PodTemplate / Pod label 和 `schedulerName` 透传，而不是再转一层 Reservation。

### Sandbox API 感知

Koordinator 应该感知 sandbox API，但不应该拥有完整 sandbox orchestration API。

第一阶段目标建议优先选择少量重点 API，例如 [`kubernetes-sigs/agent-sandbox`](https://github.com/kubernetes-sigs/agent-sandbox)。issue 讨论中明确提出需要补充 Sandbox API awareness；本地代码确认该项目的 Sandbox 路径最终会从 PodTemplate 创建 Kubernetes Pod，因此 Pod / PodTemplate 是 Koordinator 最稳妥的集成边界。后续可继续评估 [`opensandbox-group/OpenSandbox`](https://github.com/opensandbox-group/OpenSandbox)、[`agenttier/agenttier`](https://github.com/agenttier/agenttier)、[`agentscope-ai/agentscope-runtime`](https://github.com/agentscope-ai/agentscope-runtime) 等项目与 Koordinator 的接入方式。

API awareness 层应该：

- 识别最终会创建 Pod 的 sandbox 对象。
- 将 Koordinator 调度元数据注入或透传到最终 Pod / PodTemplate。
- 对非 Pod 类型的 sandbox，只在其消耗 Koordinator 管理节点资源且存在明确 adapter 时，映射为 Reservation。
- 保留上游对象状态回写的接口位置；第一阶段只要求资源释放状态可观察，不设计复杂 scheduling feedback。
- 当上游 API 表达 warm pool 时，后续阶段再评估是否为 warm capacity 创建 Reservation。

初始调度元数据保持尽量小：

| Key | Type | Purpose |
|---|---|---|
| `scheduling.koordinator.sh/sandbox` | label | 标识 Pod 或 PodTemplate 是 sandbox workload。 |
| `scheduling.koordinator.sh/sandbox-runtime` | label | 标识 runtime family，例如 `gvisor`、`kata`、`wasm`、`runc`。 |
| `scheduling.koordinator.sh/sandbox-template-hash` | label | 标识可用于 cache reuse 的等价 sandbox template。 |

以上 key 仍是 provisional，最终命名需要 API review。

### Reservation 转换层与接入路径

转换层的职责不是定义一个新的 sandbox API，而是把不同来源的 sandbox 意图转成 Koordinator 已有调度面能处理的对象。

接入决策先看 sandbox 消耗的资源是否在 Koordinator 管理的 K8s node 上：

| Topology | Example | Koordinator can manage? | Path |
|---|---|---|---|
| 不在 Koordinator 管理节点上运行 | 外部托管 sandbox、独立 Nomad / VM 平台 | 否 | 不接入；Koordinator 没有可扣减的节点账本。 |
| 在 Koordinator 管理节点上，且最终是 Pod | agent-sandbox Sandbox、Kata / gVisor / runc Pod、PodTemplate-based sandbox API | 是 | Pod label + webhook / adapter 注入，进入 koord-scheduler fast path。 |
| 在 Koordinator 管理节点上，但不是 Pod | node-local Firecracker VM、host process sandbox、外部 runtime allocation | 是，但需要 adapter | Reservation 记账；是否由 Koordinator 选 node 需要单独协议。 |

第一版接入路径只分两类：

| Runtime shape | First-stage path | Notes |
|---|---|---|
| Pod-shaped sandbox | Pod 或 PodTemplate 打稳定 label / annotation，必要时由 webhook / adapter 注入 `schedulerName: koord-scheduler`，直接走 sandbox fast path。 | [`kubernetes-sigs/agent-sandbox`](https://github.com/kubernetes-sigs/agent-sandbox)、gVisor、Kata 等最终落成 Pod 的路径都属于这一类。 |
| Non-Pod sandbox | adapter 创建不被普通 Pod 认领的 accounting-only Reservation。 | 只接入运行在 Koordinator 管理节点上、且资源占用能被 adapter 明确上报的 allocation。 |

预热不作为第一版主线设计，但需要预留位置：当上游 warm pool / workload 已经表达 warm slot 时，后续可以评估用 Reservation 表达 warm capacity；非 Pod 预热还需要单独定义 sandbox 生命周期、Reservation 状态机、唤醒 / 回收语义。

非 Pod sandbox 的 Reservation 语义应与后续 warm capacity 区分：

- **Warm capacity Reservation**：允许匹配的 sandbox Pod / claim 认领；用于预热容量。
- **Accounting-only Reservation**：只做外部 allocation 记账，不允许普通 Pod 认领。可通过 `unschedulable: true`、不可匹配 owner、固定 `nodeName` 等方式表达，但最终机制需要 API review。

对于 accounting-only Reservation，推荐的安全协议是 Reservation-first：adapter 先创建 Reservation，等 Reservation `Available` 且 `status.nodeName` 明确后，再在该节点启动非 Pod sandbox；释放时先停止外部 sandbox，再删除 Reservation。这样可以避免“先跑后补账”或“先删账后停 runtime”的超卖窗口。若外部系统必须先选 node，则 Reservation template 可以带 `nodeName`，Koordinator 只做该节点准入校验。

### 高并发 Sandbox 调度

高并发调度是本 proposal 的重点。接入能力决定 sandbox 能不能通过 Koordinator 跑起来；调度性能决定其他 sandbox 平台是否有动力接入 Koordinator。

阶段性目标是先建立可重复的高并发 benchmark，用它拆解 queue、filter / score、reserve、bind、APIServer 写入等瓶颈。具体吞吐目标应由 benchmark 和社区讨论共同确定，不能写成 API 语义保证。

高并发问题分三部分：

- 对近似相同 sandbox Pod 重复执行 filter / score；
- 高频 sandbox 生命周期事件造成写入压力、队列压力和 bind 并发压力；
- 当前通用调度 pipeline 对短生命周期、同质化 sandbox 请求偏重，需要为显式启用的 sandbox workload 提供更轻量路径。

性能优化应优先发生在调度 pipeline 层，而不是只在某个插件里做局部优化。

#### Phase 1: Minimal Sandbox Scheduling Path

sandbox fast path 只对带明确 label / annotation 的 sandbox Pod 或 Reservation 生效。第一期目标是建立极简调度链路和性能基线。

一期落地内容：

- sandbox workload 使用专门 label / annotation 显式启用。
- 优先通过独立 scheduler profile 裁剪插件集合，只保留 sandbox 必需的 Filter、Score、Reserve、PreBind、Bind 能力。
- 复用 Koordinator 现有 `FindOneNodePlugin` / `PreferNodesPlugin` 这类候选节点缩小扩展点；即使命中单节点，仍运行 Filter 作为正确性校验。
- 对不影响一期 sandbox 调度正确性的插件和扩展点保持关闭或不参与该 profile。
- Pod-shaped sandbox 仍使用 Kubernetes Pod binding 语义。
- 非 Pod 类型的 sandbox 通过 Reservation 完成资源占位，不引入新的 sandbox 调度对象。
- 产出 queue、filter / score、reserve、prebind、bind、APIServer request 的阶段性 benchmark。

一期不要求立刻实现 equivalence-class cache 或批量 bind。先保证 fast path 可控、正确、可观测。

一期 milestone：

| Milestone | Deliverable |
|---|---|
| B1.1 | 实现 sandbox fast path 显式启用机制：label / annotation、`schedulerName`、scheduler profile。 |
| B1.2 | 落地最小插件集合：明确保留哪些 Filter / Score / Reserve / PreBind / Bind 插件，并在 sandbox profile 中关闭非必要插件。 |
| B1.3 | 建立 sandbox burst benchmark：拆分 queue、filter / score、reserve、prebind、bind、APIServer 写入耗时。 |
| B1.4 | 与默认调度路径对比，确认最小链路收益和下一阶段瓶颈。 |

#### Phase 2: Bind and API Pressure Optimization

二期目标是落地针对 binding cycle 和 APIServer / etcd 冲击的优化。调研现有实现是前置工作，最终需要产出可启用的实现，或明确证明某个候选方向不适合 Koordinator。

现有边界：

- Kubernetes DefaultBinder 通过 Pod `binding` 子资源逐个绑定 Pod；即使 binding cycle 是异步执行，最终 API 仍是一 Pod 一 bind。
- Koordinator `DefaultPreBind` 当前对 Pod / Reservation 做安全 patch；Reservation 插件的 Bind 当前通过 `UpdateStatus` 更新单个 Reservation。
- Volcano agent-scheduler 可作为参考：多个 worker 并发调度，调度结果携带多个候选节点进入 `ConflictAwareBinder`，binder 通过 node bind generation 做冲突检查；无可用候选节点时把 Pod 高优先级放回队列。它没有改变 Kubernetes 最终 Pod bind 语义，而是在调度结果和绑定前增加冲突仲裁层。

二期候选落地方向：

| Direction | Design idea | Open question |
|---|---|---|
| API write reduction | 减少不必要的 annotation / status patch；对 sandbox fast path 只保留正确性必需的写入。 | 哪些 PreBind patch 对 sandbox 必不可少？哪些只是诊断或调试信息？ |
| Binder queue / conflict check | 参考 Volcano agent-scheduler，在 Koordinator 内部引入 scheduling result queue，由 binder 做冲突检查、限速和重试。 | 如何与 Kubernetes scheduler cache、Reserve / Unreserve、Permit / PreBind 生命周期一致？crash 后如何恢复？ |
| Candidate-node result | 调度结果保留多个候选节点，bind 前遇到资源冲突时尝试下一个候选。 | Koordinator 现有 `ScheduleResult` 和 framework extension 是否需要扩展？插件状态能否安全跨候选节点复用？ |
| Reservation-side optimization | 对非 Pod accounting 或后续 warm capacity，研究是否能在 Reservation 层减少重复计算和状态抖动。 | 哪些优化只适用于 Reservation，哪些会误伤 Pod-shaped sandbox？ |
| APIServer backpressure | 对 bind / patch 做限速、退避、错误分类和 metrics。 | 吞吐提升是否只是把压力转移到 APIServer / etcd？ |

二期 milestone：

| Milestone | Deliverable |
|---|---|
| B2.1 | 完成 Kubernetes bind 语义、Koordinator bind 路径和 Volcano agent-scheduler bind 路径调研，并确定可落地方向。 |
| B2.2 | 实现 sandbox bind / API pressure 优化，或实现必要的内部 binder queue / framework extension 扩展。 |
| B2.3 | 在 benchmark 中证明 APIServer request rate、bind p99、error rate 或 retry 次数有可观测下降。 |

#### Phase 3: Scheduler Framework Optimization

三期目标是提升大规模 sandbox burst 下的整体吞吐，属于更深的调度框架优化。

候选方向：

- **同质化调度**：对相同 template / image / resource shape 的 sandbox 请求复用部分 filter / score 结果。
- **并发调度**：允许多个 sandbox scheduling cycle 并行运行，但需要处理节点资源和 Reservation 冲突。
- **节点分池**：按 runtime、QoS、资源形态、镜像缓存等维度缩小候选节点集合。
- **调度冲突仲裁**：当并发调度结果争用同一节点资源时，提供 deterministic conflict resolution。Volcano agent-scheduler 的 bind generation 和候选节点重试可以作为参考，但 Koordinator 需要结合自身 scheduler cache 和 Reservation cache 重新设计。
- **缓存失效机制**：当 node resource、Reservation、plugin config 或 sandbox template 变化时，及时失效可复用结果。

三期 milestone：

| Milestone | Deliverable |
|---|---|
| B3.1 | 定义并实现 sandbox equivalence key 和可复用的 plugin 范围。 |
| B3.2 | 实现并验证并发调度和节点资源冲突仲裁机制。 |
| B3.3 | 验证节点分池 / 同质化调度在 sandbox burst 下的吞吐收益。 |

性能拆解至少应覆盖：

| Stage | What to measure | Possible optimization |
|---|---|---|
| Queue | sandbox burst 入队、排序、退避耗时 | sandbox 专用 queue hint、减少无效重试、同质请求聚合。 |
| Filter / Score | 每个 Pod 重复计算成本 | equivalence-class cache、节点分池、跳过无关插件。 |
| Reserve | Reservation / Pod 占账和冲突处理成本 | 简化 Reservation state transition、冲突仲裁。 |
| Bind | binding cycle 并发、单次 bind latency、失败重试 | binder queue、冲突检查、限速、减少无意义 patch。 |
| API write | APIServer / etcd request rate、p99、error rate | 限速、去重、后续评估 etcd / apiserver 批处理旋钮。 |

#### Equivalence-Class Scheduling

Equivalence-class scheduling 是 Phase 3 的候选优化，不是一阶段承诺。两个 sandbox Pod 在已启用插件所消费的输入上等价时，调度器可以复用部分调度结果。

equivalence class key 至少应包括：

- resource requests、会影响插件判断的 limits；
- runtime class；
- schedulerName 和 scheduler profile；
- node selector、node affinity、pod affinity、tolerations、topology spread、相关 annotations；
- 当 ImageLocality 等插件参与 score 时，需要包含 images；
- Reservation / warm-pool affinity；
- 插件声明会影响 cache key 的 labels 或 annotations。

第一版必须保守：

- 只有显式声明 equivalence safe 的插件才复用结果。
- 按 equivalence key 和 scheduler snapshot generation 缓存 feasible nodes 与可复用 scores。
- 当 node resource、Reservation、plugin config、或任何进入 equivalence key 的 Pod 字段变化时失效缓存。
- 当插件未声明安全或无法计算 key 时，回退普通调度路径。

正确性优先于 cache hit rate。不安全的复用会把 Pod 放到不再满足约束的节点上；cache miss 只会损失性能。

#### APIServer Pressure Relief

issue 提到高频 Pod 生命周期事件会造成 APIServer bottleneck。Koordinator 应在尊重 Kubernetes Binding API 的前提下处理这个问题：每个 concrete Pod 仍独立绑定。

二期应评估：

- 减少重复 status / annotation patch；
- 评估 binding cycle 并发、client QPS / burst、APIServer throttling 和 backoff；
- 评估 Reservation 层是否可以做内部 batching，降低重复计算和对象状态抖动；
- 在 Kubernetes API 语义仍要求一 Pod 一 bind 的前提下，做 scheduler 内部工作批处理；
- 在后续 warm capacity 阶段，通过分配预热 sandbox 减少每次 tool call 都创建 cold Pod 的 churn。

性能目标应基于端到端 benchmark，而不只是 raw binding throughput。proposal 的验收不应停在 raw bind：必须衡量 create-to-bound latency、queue latency、filter / score latency、reserve latency、bind latency、APIServer request rate、error rate 和 burst load 下的 p99 latency。

### 非 Pod Sandbox 记账

大多数 Kubernetes sandbox runtime 仍以 Pod 形态调度。gVisor、Kata Containers、Pod-based Wasm 集成应走 Pod 路径。

部分部署可能运行 node-local sandbox：它们消耗 Koordinator 管理节点上的资源，但不创建 Pod。这不是一期主路径，但账本模型不应阻塞后续支持。adapter 可以通过 Reservation 上报对象级 allocation：

- 外部 allocation 是 per-sandbox、高 churn、需要对象级生命周期时，使用 Reservation。

对于没有真实 Pod owner 的纯记账 Reservation，adapter 必须阻止真实 Pod 认领它，并且必须在外部 sandbox 停止后再删除它。典型表达包括 `unschedulable: true`、不可匹配的占位 owner、固定 `nodeName`，但这些字段组合需要单独 API review。除非社区接受具体集成方案，否则非 Pod sandbox 的调度决策介入保持 future work；一期最多定义记账协议和 adapter 边界。

### 实现细节

本节描述实现边界，不是最终 API contract；API 变更仍需单独 review。

#### API 与 Extension Constants

在 `apis/extension` 下增加一小组 sandbox extension constants：

- sandbox workload label；
- sandbox runtime label；
- sandbox template / equivalence label；

这些 constants 应足够稳定，供 controller、scheduler plugin 和文档共享。runtime name 应做规范化，但不应限定为封闭 enum；未知 runtime 应允许存在，并按保守路径处理。

#### Admission 与分类

sandbox classification 可以通过一个或多个显式启用路径实现：

- 上游 PodTemplate 已携带 Koordinator labels 和 `schedulerName`；
- sandbox adapter 将上游 Sandbox、SandboxTemplate、SandboxClaim 等对象上的 label 复制到 PodTemplate；
- 专门 admission hook 在用户显式选择 Koordinator scheduling 时设置 `schedulerName`。

实现必须保留用户显式意图。如果用户设置了 runtime class、priority、node selector 或 scheduler name，Koordinator 不应静默覆盖，除非被选中的 policy 明确要求覆盖。

#### Reservation 转换 Adapter

Reservation 转换层应按 runtime shape 分流：

- **Pod-shaped sandbox**：adapter 不创建 Reservation；只负责识别上游 Sandbox / PodTemplate，将 sandbox label、runtime label、template hash 和 `schedulerName` 透传到最终 Pod。普通 Pod 直走 fast path，避免把 Pod 路径强行绕进 Reservation 模型。
- **Warm capacity**：后续阶段再评估由 adapter 观察上游 warm pool / workload，并用 Reservation 表达 warm slot capacity；本 proposal 不展开 claim、回收和唤醒状态机。
- **Non-Pod accounting**：adapter 根据外部 allocation 创建 accounting-only Reservation。Reservation 对象应带上外部 sandbox id、runtime、resource shape、nodeName、生命周期状态等 label / annotation，便于审计和清理。

转换层不需要兼容所有社区 API。第一阶段只选择重点 API，抽象出可扩展的 adapter interface，后续按项目补充实现。调度上的特殊能力优先通过 Reservation 或 Pod 上的 label / annotation 表达，避免为了单个 sandbox API 修改 scheduler core。

#### Reservation Resource Shape

Koordinator 不负责计算 sandbox runtime 的额外开销。Pod 形态 sandbox 的资源请求、RuntimeClass / PodOverhead 等字段由用户、Kubernetes admission 或上游 admission 写入 Kubernetes 对象；非 Pod sandbox 的资源请求由 adapter 上报。

Reservation 转换层只需要保证三件事：

- Pod-shaped sandbox 不被改写成另一套资源模型；
- 后续涉及 warm capacity 时，Reservation template 与未来要承接的 sandbox Pod 使用同一套资源请求；
- accounting-only Reservation 使用外部 sandbox 实际占用的资源口径。

任何修改 Reservation template 的实现都必须幂等，并暴露最终 resource shape 方便调试。

#### Equivalence-Class Fast Path

equivalence-class scheduling 是 Phase 3 的候选 scheduler optimization，而不是新的调度语义，也不是第一版接入闭环的依赖。

实现要求：

- admission 之后、filter / score 之前计算 equivalence key；
- key 包含所有显式启用 plugin 消费的字段；
- cached results 关联 scheduler snapshot 或等价 invalidation generation；
- 每个 plugin 声明自己的 filter / score 结果是否可基于该 key 复用；
- 对依赖 per-Pod identity、时间、随机选择、可变外部状态或未建模 annotation 的 plugin 禁止复用；
- 记录 cache hit、miss、fallback、invalidation metrics。

Phase 3 初版可以只让少量插件显式启用。保守的部分缓存可以接受；不安全的全局复用不可接受。

#### 可观测性

按已启用能力暴露以下 metrics：

- recognized sandbox Pod 数量；
- sandbox scheduling latency by stage；
- accounting-only Reservation 创建、释放、失败次数；
- 按 runtime family 统计的 Reservation / adapter 记账量。
- 后续启用 equivalence-class scheduling 时，增加 cache hit / miss / fallback 次数。

这些 metrics 是性能验证和显式启用 rollout 调试所必需的。

### 后续工作

以下能力来自 issue #2879 的完整问题空间，但不作为本次 proposal 主线设计：

- **Warm capacity**：当上游 workload 已经表达 warm pool / warm slot 时，评估用 Reservation 表达预热容量，使其对 scheduler cache 可见。Reservation owner、claim、recycle 和唤醒状态机需要单独设计。
- **QoS 接入**：接入闭环稳定后，再评估如何通过现有 QoS class、`ClusterColocationProfile`、priority、koordlet / cgroup 能力承接 sandbox 分类。RuntimeClass / PodOverhead 由用户或 Kubernetes admission 管理，Koordinator 不在本 proposal 中计算 runtime overhead。
- **Sandbox 故障感知和诊断**：短期只要求 sandbox 退出后资源账本能释放，避免卡死。结构化 scheduling hint、自动诊断、上游 SandboxClaim status 回写等能力后续单独设计。
- **Koordinator scheduling policy object**：第一版不引入。如果 labels、annotations、scheduler profile、Reservation template 无法表达后续策略，再讨论薄 policy object；该对象只能保存 Koordinator 调度策略，不替代上游 sandbox API。

### 里程碑

| Milestone | Scope | Checkable outcome |
|---|---|---|
| M1: Reservation conversion and Pod classification | 定义 sandbox labels / annotations、重点上游 API adapter 行为和非 Pod accounting-only Reservation 语义。 | PodTemplate-based sandbox 能被 Koordinator 识别并路由到 `koord-scheduler`；非 Pod 接入边界和记账协议被文档化。 |
| M2: Accounting closure | 验证 Reservation template 或非 Pod adapter 上报的资源口径正确；sandbox 退出后账本释放。 | sandbox Reservation 不会因为记账口径不一致或释放失败造成节点超卖。 |
| M3: Minimal fast path | 建立 sandbox 显式启用的 scheduler profile / fast path，并明确最小插件集合。 | Pod-shaped sandbox 能走 fast path，普通 Pod 不受影响。 |
| M4: High-concurrency benchmark | 建立高并发 sandbox 绑定能力 benchmark，拆分 queue、filter / score、reserve、prebind、bind、APIServer 写入。 | 能定位下一阶段瓶颈，且 benchmark 可重复运行。 |
| M5: Bind / API pressure optimization | 完成 Kubernetes bind、Koordinator bind / patch、Volcano agent-scheduler binder 调研，并落地二期优化。 | 明确并实现内部 binder queue、candidate-node result、Reservation-side optimization 或其他被 benchmark 证明有效的替代方案。 |

这些 milestone 可以放在 feature gate 后分阶段实现，不需要所有 outcome 同时发布。

### 风险与缓解

| Risk | Description | Mitigation |
|---|---|---|
| 不安全的 equivalence reuse | 对并不真正等价的 Pod 复用 filter / score 结果，会产生错误 placement。 | 要求 plugin 显式声明；cache invalidation 包含 plugin config 和 scheduler snapshot generation；不确定时回退普通调度。 |
| 资源账本漏计 | Reservation template 或外部 allocation 口径不一致，会导致节点超卖。 | Reservation template 与目标 sandbox 使用一致资源请求；外部 adapter 上报实际占用；增加 actual usage vs accounted usage 观测。 |
| API 重复建设 | Koordinator-specific sandbox 资源模型可能重复上游 sandbox API，造成生态割裂。 | 不新增 Sandbox CRD；以 Pod / PodTemplate 作为标准边界；优先 adapter、labels / annotations 和 Reservation。 |
| Bind 优化误判 | 只提升调度器内部吞吐，但把压力转移到 APIServer / etcd。 | benchmark 必须包含 APIServer request rate、throttling、error rate、p99 latency。 |
| 非 Pod 账本生命周期不一致 | 外部 sandbox 已启动但 Reservation 未成功，或 sandbox 已退出但 Reservation 未删除。 | 优先 Reservation-first；必须支持幂等 reconcile 和超时清理。 |
| 安全边界误导 | 调度策略本身不能保证 filesystem、network、runtime isolation。 | 明确 runtime 和集群前置条件；本 proposal 只覆盖 Koordinator 调度与账本。 |

## 备选方案

**只使用 Pod label 和 fast path**

实现简单，但无法解决非 Pod 资源记账，也无法覆盖后续 warm capacity。

**定义 Koordinator Sandbox CRD**

Koordinator 可以获得完整 API surface，但这会重复上游 sandbox 工作，也与本 proposal 的接入层定位冲突。

**只优化 binding**

binding optimization 只覆盖链路中的一段。当前瓶颈需要先拆解 queue、filter / score、reserve、prebind、bind、APIServer / etcd 写入；仅优化 bind 不够。

**非 Pod sandbox 全部使用 NodeReservation**

NodeReservation 适合粗粒度、相对静态的节点级扣减。它通过节点 annotation 表达，不需要为每个 sandbox 创建或更新独立对象，写入成本低，也不会直接增加调度器对象处理压力。

它的问题是缺少 per-sandbox 对象生命周期，更新依赖 koordlet / runtimehook reconciler 类似的异步同步链路。在短生命周期、高 churn 的 sandbox 场景下，滞后更新可能带来启动后才补账、退出后未及时释放、多个 sandbox 变更被合并或覆盖等账本窗口。因此第一版不把 NodeReservation 作为主路径，只把它作为粗粒度节点扣减的备选方案。

## 升级策略

所有新行为都应显式启用：

- sandbox classification 需要显式 labels、selectors、上游 adapter 配置或 scheduler profile。
- sandbox fast path 应由 scheduler profile 或 feature gate 控制。
- equivalence-class scheduling、内部 binder queue、并发调度等二三期能力在正确性和 benchmark 被接受前默认关闭。
- warm pool、QoS 深度接入、diagnostics 是后续阶段能力，不随本 proposal 一期默认开启。

现有非 sandbox Pod 应继续使用当前调度路径。

建议 rollout controls：

| Capability | Default | Rollout control |
|---|---|---|
| Sandbox labels and constants | Available | 没有 selector 或 adapter 时不产生行为变化。 |
| Non-Pod Reservation accounting adapter | 关闭 | adapter feature gate、外部 runtime allowlist、Reservation owner / nodeName policy。 |
| Sandbox fast path profile | 关闭 | `schedulerName`、profile 配置、feature gate。 |
| Equivalence-class scheduling | 关闭 | scheduler feature gate 和 plugin 显式启用列表。 |
| Internal binder queue / conflict arbitration | 关闭 | scheduler feature gate、benchmark gate。 |
| Warm pool / QoS / diagnostics | 后续阶段关闭 | 单独 proposal 或补充设计。 |

升级时可以按以下顺序启用：classification、非 Pod 记账 adapter（如需要）、sandbox fast path profile、bind / API pressure 优化、equivalence-class / 并发调度优化。

## 测试计划

Unit tests：

- sandbox label / annotation parsing；
- Reservation conversion adapter 的 Pod / non-Pod 分流；
- accounting-only Reservation template 生成和不可认领语义；
- Reservation template 与目标 sandbox Pod 的资源请求一致；
- 后续阶段：equivalence-class key generation、cache invalidation 和 plugin 显式启用行为；

Integration tests：

- PodTemplate-based sandbox API 创建带 Koordinator 调度元数据的 Pod；
- 非 Pod adapter 创建 accounting-only Reservation 后，普通 Pod 不能认领该 Reservation；
- 非 sandbox Pod 不受 sandbox feature gate 影响。
- Reservation-first 非 Pod sandbox 流程中，Reservation 创建失败时不会启动外部 sandbox，外部 sandbox 退出后 Reservation 会被释放。

Performance tests：

- 高并发绑定能力 microbenchmark，并拆分 bind latency、APIServer request rate、error rate；
- 近似相同 sandbox Pod 的 burst scheduling；
- sandbox fast path 与默认 profile 的 queue、filter / score、reserve、prebind、bind 分段对比；
- sandbox churn 下的 APIServer request rate、throttling、error rate 和 p99 latency。

End-to-end tests：

- 测试环境支持时验证 gVisor / Kata / Wasm 的 Pod-shaped sandbox 路径；否则使用 simulated runtime。

## 开放问题

- 是否需要薄的 Koordinator scheduling policy object，还是通过 scheduler profile、annotations 和 Reservation templates 表达就足够？当前倾向第一版不引入。
- 除 [`kubernetes-sigs/agent-sandbox`](https://github.com/kubernetes-sigs/agent-sandbox) 外，第一阶段还应选择哪些重点 API？是否需要覆盖 [`opensandbox-group/OpenSandbox`](https://github.com/opensandbox-group/OpenSandbox)、[`agenttier/agenttier`](https://github.com/agenttier/agenttier)、[`agentscope-ai/agentscope-runtime`](https://github.com/agentscope-ai/agentscope-runtime) 这类 sandbox runtime / platform 项目？
- 高并发绑定目标应拆成哪些 benchmark：raw bind、create-to-bound、调度 throughput、p99 latency、APIServer / etcd request pressure？
- sandbox fast path 第一版到底砍掉哪些非必要 scheduler 模块？
- 是否需要内部 binder queue / candidate-node result？如果需要，如何与 Kubernetes scheduler cache、Reserve / Unreserve、PreBind / Bind 生命周期一致？
- Reservation-side optimization 的语义边界是什么？哪些优化可以在 Reservation 层做，哪些会违反 Kubernetes Pod Binding 语义？
- 第一版哪些 scheduler plugins 可以安全显式启用 equivalence-class reuse？
- accounting-only Reservation 应使用 `unschedulable: true`、占位 owner、固定 `nodeName` 的哪种字段组合？

## 实现历史

- [x] 2026-04-28：issue [#2879](https://github.com/koordinator-sh/koordinator/issues/2879) 创建。
- [x] 2026-05-12：maintainer 在 issue 讨论中要求补充 Sandbox API adaptations/awareness 章节。
- [x] 2026-06-26：创建 proposal 初稿。
- [x] 2026-06-30：围绕 issue #2879 的范围重写 proposal。
- [x] 2026-06-30：将草稿改为中文，便于继续迭代设计。

## 参考资料

- [Issue #2879: Native support for AI Agent orchestration and high-concurrency Sandbox scheduling](https://github.com/koordinator-sh/koordinator/issues/2879)
- [Maintainer comment requesting Sandbox API adaptations/awareness](https://github.com/koordinator-sh/koordinator/issues/2879#issuecomment-4430422335)
- [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox)
- [opensandbox-group/OpenSandbox](https://github.com/opensandbox-group/OpenSandbox)
- [agenttier/agenttier](https://github.com/agenttier/agenttier)
- [agentscope-ai/agentscope-runtime](https://github.com/agentscope-ai/agentscope-runtime)
- [Koordinator Reservation proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20220609-resource-reservation.md)
- [Koordinator NodeReservation proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20221227-node-resource-reservation.md)
- [Koordinator QoS Manager proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/koordlet/20220615-qos-manager.md)
