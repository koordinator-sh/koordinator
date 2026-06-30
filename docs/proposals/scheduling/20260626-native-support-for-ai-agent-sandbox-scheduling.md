---
title: Native Support for AI Agent Orchestration and Sandbox Scheduling
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

# Native Support for AI Agent Orchestration and Sandbox Scheduling

本文对应 issue [#2879](https://github.com/koordinator-sh/koordinator/issues/2879)。当前阶段以 issue #2879 作为范围的一手事实源；本地调研和外部 sandbox 项目只用于验证集成可行性，不作为扩大范围的依据。

## 目录

<!-- TOC -->

- [摘要](#摘要)
- [术语](#术语)
- [动机](#动机)
  - [目标](#目标)
  - [非目标 / 后续工作](#非目标--后续工作)
- [方案](#方案)
  - [用户故事](#用户故事)
  - [需求](#需求)
  - [分期范围](#分期范围)
  - [架构](#架构)
  - [Sandbox API 感知](#sandbox-api-感知)
  - [Reservation 转换层与接入路径](#reservation-转换层与接入路径)
  - [方向 A：Sandbox Colocation and Hardening](#方向-asandbox-colocation-and-hardening)
  - [方向 B：High-Concurrency Sandbox Scheduling](#方向-bhigh-concurrency-sandbox-scheduling)
  - [方向 C：Sandbox Pipeline and Warm Capacity](#方向-csandbox-pipeline-and-warm-capacity)
  - [方向 D：Automated Diagnostics and Feedback](#方向-dautomated-diagnostics-and-feedback)
  - [非 Pod Sandbox 记账](#非-pod-sandbox-记账)
  - [实现细节](#实现细节)
  - [里程碑](#里程碑)
  - [风险与缓解](#风险与缓解)
- [备选方案](#备选方案)
- [升级策略](#升级策略)
- [测试计划](#测试计划)
- [开放问题](#开放问题)
- [实现历史](#实现历史)
- [参考资料](#参考资料)

<!-- /TOC -->

## 摘要

AI Agent 系统带来了一种新的 Kubernetes 调度形态：在工具执行、多 Agent 协作、SWE-bench 这类评测场景中，系统会频繁创建、复用、销毁大量短生命周期、强隔离的 sandbox 环境。这类 sandbox 往往高度相似、突发性强、启动延迟敏感，而且 runtime overhead 不一定体现在普通容器 request 中，一旦账本不准就容易造成节点超卖。

Koordinator 应该围绕 issue #2879 中提出的四个方向，原生支持这种 workload 形态：

1. **Sandbox colocation and hardening**：把 gVisor、Kata Containers、Wasm 等 sandbox runtime 与 Koordinator QoS class 和资源隔离策略集成起来。
2. **High-concurrency scheduling optimization**：减少近似相同 sandbox 请求的重复调度计算，并缓解高频 Pod 生命周期事件对 APIServer 的压力。
3. **Sandbox Pipeline**：提供 Koordinator 侧的调度与 Reservation 层，用于 sandbox 环境准备、预热、runtime provisioning 和容量预留。
4. **Automated diagnostics and feedback**：返回机器可读的调度反馈，让上层 Agent 编排系统可以自愈、降级、重试或扩容。

本 proposal 的核心判断是：Koordinator 不自研一套新的 Sandbox CRD，而是在现有 Pod、Reservation、QoS 和调度诊断能力之上做一层轻量接入。只要 sandbox API 最终创建 Kubernetes Pod，就以 Pod 作为标准调度对象并进入 sandbox fast path；如果 sandbox 消耗 Koordinator 管理节点上的资源但不创建 Pod，则由 adapter / glue layer 将外部 allocation 映射为 Koordinator Reservation 或 NodeReservation 进行记账。预热、睡眠唤醒、自动诊断等能力应在 proposal 中保留设计位置，但不作为一期实现主线。

## 术语

- **Agent Brain**：Agent 的长生命周期 reasoning loop。通常对延迟敏感，不应该被突发的工具执行任务挤压。
- **Skill / Tool sandbox**：Agent 执行代码、工具或外部动作时使用的短生命周期隔离环境。
- **Sandbox runtime**：提供更强隔离或改变启动 / 运行时开销的 runtime，例如 gVisor、Kata Containers、Wasm。
- **Sandbox Pipeline**：Koordinator 管理的 sandbox 调度流程，覆盖准备、预留、预热、分配和诊断。
- **Reservation 转换层**：把上游 sandbox API 或非 Pod sandbox allocation 转成 Koordinator 可理解的 Pod / Reservation / NodeReservation 元数据的 adapter 层。
- **Warm pool**：预创建或部分准备好的 sandbox 集合，用于比冷启动更快地响应请求。
- **Equivalence class**：一组在调度输入上等价的 sandbox 请求；这些请求可以安全复用部分 filter / score 结果。

## 动机

AI Agent workload 与普通长稳服务和批处理任务不同：

- 会为 tool call、代码执行、评测任务突发创建大量短生命周期 sandbox。
- 很多请求高度相似，例如同一个镜像、同一组资源，只是输入数据不同。
- 启动延迟很关键。冷启动 sandbox 可能主导用户可感知延迟，因此 warm pool 是重要的实际需求。
- 强隔离 runtime 会带来应用容器 request 之外的开销。如果这部分 overhead 没有进调度账本，调度器会高估节点剩余资源。
- Agent 编排器需要可执行的反馈。普通的 unschedulable timeout 不足以让 autonomous agent 判断应该扩 warm pool、降低并发、切换 runtime、还是稍后重试。

Koordinator 已经有一些与该问题相关的基础能力：QoS class、`ClusterColocationProfile`、Reservation、调度诊断、scheduler plugins。本 proposal 的目标是把这些基础能力连接成一个面向 sandbox 的调度流程，而不是让每个 Agent 平台重复实现一套控制面逻辑。

### 目标

一期目标应先收敛到两件事：**接入**和**快速调度**。issue #2879 中的 warm pipeline、自动诊断、深度 QoS hardening 仍属于完整 proposal 的设计范围，但不应压到第一阶段实现里。

- 在 Pod / PodTemplate 边界稳定识别 sandbox workload。
- 通过 Reservation 转换层接入重点 sandbox API，不定义一套平行的完整 sandbox 编排模型。
- Pod 形态 sandbox 通过 label / annotation / `schedulerName` 进入 Koordinator 快速调度路径。
- 非 Pod 形态 sandbox 在消耗 Koordinator 管理节点资源时，可以通过 Reservation 或 NodeReservation 进入调度账本，避免节点资源被重复分配。
- 调度账本不超卖，至少计入 Pod requests、Kubernetes `RuntimeClass` / `pod.spec.overhead`，以及必要时由 Koordinator runtime template 表达的额外 overhead policy。
- 为高并发 sandbox 请求提供快速调度路径，初始 benchmark 目标按 5000 QPS 绑定能力拆解和验证。
- 保留 warm capacity、睡眠唤醒和 automated diagnostics 的设计扩展点，便于后续阶段覆盖 issue #2879 的完整 A-D outcomes。

### 非目标 / 后续工作

- 不实现 sandbox runtime、container runtime、VM runtime、network sandbox 或 filesystem isolation 机制。
- 不替代 `kubernetes-sigs/agent-sandbox` 这类上游 sandbox API；Koordinator 应该适配它们。
- 不自研完整 Sandbox CRD；如果未来引入 Koordinator policy object，也只能是薄调度策略包装。
- 不一次性兼容所有 sandbox API；第一阶段只选择重点 API / runtime 形态接入，并保证 adapter 抽象可扩展。
- 不让所有外部 sandbox 平台都直接受 Koordinator 调度。Koordinator 只能管理自己可见节点上的资源，或由明确 adapter 上报的资源。
- 第一阶段不单独实现调度层 warm pool、预热池或睡眠唤醒控制器；预热能力在 proposal 中保留，依赖上层 workload / sandbox API 先表达。
- 第一阶段不做复杂 automated diagnostics；只保留 sandbox 异常退出、资源释放、卡死避免所需的基础状态感知。
- 第一阶段不深改 QoS / cgroup 隔离 / 混部优化；先保证调度账本准确、不超卖。
- 第一阶段不做全局 cost budget、wallet policy、per-agent billing 或 plugin circuit breaker。
- 不硬编码通用 runtime overhead 值。可以提供默认值，但生产值必须可配置、可验证。
- 如果 label、`ClusterColocationProfile`、上游 Sandbox API 和 Reservation 已能表达第一阶段需求，则不强制新增 Koordinator CRD。

## 方案

### 用户故事

**Story 1：SWE-bench 风格评测**

评测平台一次启动数百个短生命周期 sandbox Pod。这些 Pod 使用相同镜像和相似资源规格。调度器应该避免为每个 sandbox 重复执行相同 filter / score 计算，以有界延迟完成绑定，并在 batch 放不下时返回可执行反馈。

**Story 2：Agent Brain 与 Skill 隔离**

Agent 平台在同一个集群里运行长生命周期 Brain Pod 和突发 Skill sandbox Pod。Brain Pod 需要更高 QoS 和稳定隔离；Skill sandbox 需要高效打包，同时准确计入 runtime overhead，避免资源竞争。

**Story 3：Warm sandbox pool（后续阶段）**

交互式 Agent 平台维护 warm sandbox 来降低冷启动延迟。Koordinator 应该为每个 warm slot 背后的容量做预留，让空闲 warm sandbox 对调度器可见，避免被重复分配。

**Story 4：自愈型 Agent 编排器（后续阶段）**

某个 sandbox Pod 因 warm pool 耗尽或 runtime-specific 资源不足而无法调度。Koordinator 应该暴露结构化 reason 和 next-step hint，让编排器可以扩 pool、退回 cold scheduling、切换 runtime、降低并发或触发扩容。

### 需求

以下 requirement 来自 issue #2879。这里刻意写成可检查结果，便于后续 PR 独立 review。

Issue 到 proposal 的映射：

| Issue outcome | Proposal section | Requirements | Phase |
|---|---|---|---|
| A. Agent Sandbox Colocation & Hardening | [Outcome A](#outcome-a-sandbox-colocation-and-hardening) | FR-2, FR-3, FR-4 | P1 for accounting, P4 for deeper isolation |
| B. Scheduling Optimization for High-Concurrency Tasks | [Outcome B](#outcome-b-high-concurrency-sandbox-scheduling) | FR-5, FR-6 | P2 |
| C. Customized Sandbox Pipeline | [Outcome C](#outcome-c-sandbox-pipeline-and-warm-capacity) | FR-7, FR-8, FR-9 | P3, with design reserved in P0 |
| D. Automated Diagnostics & Feedback | [Outcome D](#outcome-d-automated-diagnostics-and-feedback) | FR-10 | P4, with basic lifecycle status in P1 |
| issue 讨论中提出的 Sandbox API adaptations/awareness | [Sandbox API Awareness](#sandbox-api-awareness) | FR-1 | P1 |

| ID | Requirement | Issue source | Design surface | Verification |
|---|---|---|---|---|
| FR-1 | Koordinator 能在不拥有上游 sandbox API 的前提下识别 sandbox Pod / PodTemplate，并把重点 API 或非 Pod allocation 转到 Pod / Reservation / NodeReservation 调度面。 | issue 讨论中要求补充 Sandbox API awareness；阶段性设计讨论收敛为 Reservation 转换层。 | sandbox labels / annotations、adapter、PodTemplate 透传、Reservation templates。 | 通过选定上游 API 创建的 Pod 携带稳定的 Koordinator 调度元数据；实验性非 Pod adapter 能创建可扣账的 Reservation 或 NodeReservation。 |
| FR-2 | sandbox Pod 可以映射到 Koordinator QoS class。 | Outcome A: Best Practice Support。 | `ClusterColocationProfile`、admission、adapter 注入 label。 | 被选中的 sandbox Pod 获得预期 `koordinator.sh/qosClass`，同时保留用户显式 override。 |
| FR-3 | runtime overhead 与 workload 容器 request 分开计入账本。 | Outcome A: Resource Hardening。 | `pod.spec.overhead`、runtime overhead policy、scheduler / quota accounting。 | 如果只有忽略 runtime overhead 才能放下 Pod 或 Reservation，则调度应该拒绝。 |
| FR-4 | Agent Brain 与 Skill sandbox workload 可以使用不同 QoS 和 priority policy。 | Outcome A 和 issue use cases。 | Namespace policy、labels、`ClusterColocationProfile`、PriorityClass。 | Brain 和 Skill Pod 被不同 policy 选中，并获得不同 QoS / priority 元数据。 |
| FR-5 | 近似相同 sandbox 请求可以在安全边界内减少重复调度计算。 | Outcome B: Equivalence Class Scheduling。 | sandbox fast path、scheduler equivalence key、plugin opt-in cache。 | 一批等价 sandbox Pod 的重复 filter / score 次数下降，同时调度结果保持正确。 |
| FR-6 | 高频 sandbox 生命周期处理应在 Kubernetes API 语义允许的范围内降低 APIServer 压力，并以 5000 QPS 绑定能力作为初始 benchmark 目标。 | Outcome B: APIServer Pressure Relief；阶段性设计讨论中的性能目标。 | 减少 patch churn、scheduler 内部 batching、Reservation-based bind optimization、bind concurrency tuning。 | benchmark 显示 create-to-bound latency、bind latency、APIServer request rate 或 error rate 达到阶段目标，同时不改变 Pod binding 语义。 |
| FR-7 | Koordinator 保留从准备到分配和回收的 Sandbox Pipeline 设计面，但一期不单独实现调度层 warm pool。 | Outcome C: Pipeline Orchestration。 | adapter、labels / annotations、可选 `SandboxPipeline` policy、Reservation templates。 | proposal 能说明 prepare、reserve、warm、assign、recycle、diagnose 的归属；一期实现只落接入和 fast path。 |
| FR-8 | concrete sandbox Pod 分配前，warm capacity 在后续阶段应对调度器可见。 | Outcome C: Pre-warming Mechanism and Capacity reservations。 | 每个 warm slot 对应 Koordinator Reservation 或等价 reservation 模型。 | warm slot 会扣减候选节点资源，且不会被重复分配。 |
| FR-9 | sandbox environment provisioning hints 可以被传递，但 Koordinator 不负责实现 runtime provisioning 本身。 | Outcome C: Automated Environment Provisioning。 | pipeline annotations / status、上游 API adapter、PodTemplate metadata。 | hints 被传递给负责 provisioning 的 sandbox controller，Koordinator 仍只负责调度 / 账本层。 |
| FR-10 | 无法调度或异常退出的 sandbox 请求最终应获得可执行的机器可读反馈；一期至少保证异常退出可被感知并释放资源。 | Outcome D: Automated Diagnostics & Feedback；阶段性设计讨论短期收敛为基础状态感知。 | `ScheduleExplanation`、`NodeFailedDetails`、`ScheduleSuggestion`、sandbox scheduling hint、Pod status / owner status。 | 后续阶段中 Agent 编排器可以区分资源不足、warm pool 耗尽、affinity mismatch、可重试等待等情况；一期避免 sandbox 异常后资源长期占用。 |

非功能要求：

- **Opt-in**：sandbox-specific 调度变更只作用于被明确选择的 sandbox workload。
- **Correctness first**：fast path 在 equivalence safety 不确定时必须回退普通调度路径。
- **API compatibility**：第一版必须适配上游 sandbox API，不强迫用户迁移到 Koordinator 自有 sandbox API。
- **Operability**：性能与诊断能力必须暴露可用于调试 rollout 的 metrics 或 status。
- **Security boundary clarity**：Koordinator 调度策略本身不承诺提供 runtime、filesystem 或 network isolation。

### 分期范围

阶段性设计讨论把一期范围收敛为“接入”和“快速调度”。这与 issue #2879 的 A-D outcomes 不冲突：issue 描述的是完整问题空间，一期实现需要先把可验证闭环做小。

| Phase | Scope | Why now | Explicitly deferred |
|---|---|---|---|
| P0 / Proposal | 明确 sandbox API 接入边界、Reservation 转换层、5000 QPS benchmark 口径、warm capacity 后续位置。 | 先让社区 review 目标和边界，避免自研 CRD 或把预热池做成调度层大抽象。 | 具体 API schema 和 controller 实现。 |
| P1 / 接入闭环 | Pod 形态 sandbox 进入 Koordinator 调度；重点上游 API 通过 adapter 透传 label / annotation / schedulerName；非 Pod sandbox 的 Reservation / NodeReservation 记账语义定稿。 | 验收线是“能调度、账本不超卖”。 | 调度层 warm pool、自动诊断、深度 QoS。 |
| P2 / 快速调度 | 为特定 sandbox label 启用轻量 fast path；拆解调度 pipeline、bind、APIServer / etcd 写入瓶颈；以 5000 QPS 绑定能力作为初始 benchmark 目标。 | 高并发 sandbox 是 issue #2879 的主战场，也是一阶段最容易形成 Koordinator 差异化价值的部分。 | 完整 equivalence-class 跨插件复用、全局调度框架重构。 |
| P3 / Warm capacity | 基于上游 warm pool 或 workload 语义创建 Reservation，支持 warm slot 预留、分配、回收。 | issue #2879 和社区评论都说明预热对延迟重要，但会议结论是不作为一期实现主线。 | Koordinator 自研 warm pool API。 |
| P4 / QoS hardening and diagnostics | 增强 cgroup / runtime hooks、结构化 scheduling hints、自动化反馈闭环。 | 在接入和快调度稳定后再做，避免第一阶段范围失控。 | cost budget、wallet、plugin circuit breaker。 |

### 架构

Koordinator 应把 sandbox support 设计为围绕现有 Pod、Reservation、NodeReservation、QoS、调度诊断机制的一组集成。

```text
Agent orchestrator / sandbox API
        |
        | Sandbox, SandboxClaim, SandboxWarmPool, PodTemplate, or external allocation
        v
Sandbox API adapter / Reservation conversion layer
        |
        | labels, annotations, schedulerName, QoS policy, Reservation templates
        v
Koordinator scheduling surface
        |
        +-- Pod fast path for Pod-shaped sandboxes
        +-- Reservation-backed accounting for non-Pod or warm capacity
        +-- NodeReservation for coarse node-level reservation
        +-- Runtime-aware QoS and overhead accounting
        +-- Scheduling diagnosis and sandbox hints (later phase)
        v
koord-scheduler / koordlet / existing Kubernetes runtime path
```

标准调度面：

- **Pod**：适用于通过 Kubernetes API 创建的 sandbox，以及 gVisor、Kata Containers、Kata with Firecracker hypervisor、常见 Wasm RuntimeClass 等以 Pod 形态进入 K8s 的 runtime。一期主体走这条路径。
- **Reservation**：适用于两类场景：一是 concrete sandbox Pod 分配前需要被调度器看见的 warm slot；二是高 churn 非 Pod sandbox 的对象级记账。
- **NodeReservation**：适用于粗粒度、相对静态的节点级资源扣减，不适合作为大量短生命周期 sandbox 的唯一模型。

### Sandbox API 感知

Koordinator 应该感知 sandbox API，但不应该拥有完整 sandbox orchestration API。

第一阶段目标建议优先选择少量重点 API，例如 `kubernetes-sigs/agent-sandbox`。issue 讨论中明确提出需要补充 Sandbox API awareness，而该上游 API 已经提供 Sandbox、SandboxTemplate、SandboxClaim、SandboxWarmPool 等关键概念。其 Sandbox 路径最终会从 PodTemplate 创建 Kubernetes Pod，因此 Pod / PodTemplate 是 Koordinator 最稳妥的集成边界。

API awareness 层应该：

- 识别最终会创建 Pod 的 sandbox 对象。
- 将 Koordinator 调度元数据注入或透传到最终 Pod / PodTemplate。
- 当上游 API 表达 warm pool 时，在后续阶段为 warm capacity 创建 Reservation。
- 对非 Pod sandbox，只在其消耗 Koordinator 管理节点资源且存在明确 adapter 时，映射为 Reservation 或 NodeReservation。
- 在 adapter 拥有权限时，把 scheduler feedback 回写给 Agent 编排器实际 watch 的对象；第一阶段只保留基础状态感知。

初始调度元数据保持尽量小：

| Key | Type | Purpose |
|---|---|---|
| `scheduling.koordinator.sh/sandbox` | label | 标识 Pod 或 PodTemplate 是 sandbox workload。 |
| `scheduling.koordinator.sh/sandbox-runtime` | label | 标识 runtime family，例如 `gvisor`、`kata`、`wasm`、`runc`。 |
| `scheduling.koordinator.sh/sandbox-template-hash` | label | 标识可用于 cache reuse 的等价 sandbox template。 |
| `scheduling.koordinator.sh/sandbox-pipeline` | annotation | 将 Pod 或 claim 关联到 Koordinator sandbox pipeline policy。 |
| `scheduling.koordinator.sh/sandbox-warm-pool` | annotation | 将 Pod 或 claim 关联到 warm pool 及其 Reservations。 |
| `scheduling.koordinator.sh/sandbox-scheduling-hint` | annotation or status payload | 携带机器可读的调度反馈。 |

以上 key 仍是 provisional，最终命名需要 API review。

### Reservation 转换层与接入路径

转换层的职责不是定义一个新的 sandbox API，而是把不同来源的 sandbox 意图转成 Koordinator 已有调度面能处理的对象。

接入决策先看 sandbox 消耗的资源是否在 Koordinator 管理的 K8s node 上：

| Topology | Example | Koordinator can manage? | Path |
|---|---|---|---|
| 不在 Koordinator 管理节点上运行 | 外部托管 sandbox、独立 Nomad / VM 平台 | 否 | 不接入；Koordinator 没有可扣减的节点账本。 |
| 在 Koordinator 管理节点上，且最终是 Pod | agent-sandbox Sandbox、Kata / gVisor / runc Pod、PodTemplate-based sandbox API | 是 | Pod label + webhook / adapter 注入，进入 koord-scheduler fast path。 |
| 在 Koordinator 管理节点上，但不是 Pod | node-local Firecracker VM、host process sandbox、自研 runtime allocation | 是，但需要 adapter | Reservation 或 NodeReservation 记账；是否由 Koordinator 选 node 需要单独协议。 |

按是否预热，可以得到四种路径：

| Runtime shape | No preheat | Preheat / warm capacity |
|---|---|---|
| Pod sandbox | 一期主路径。Pod 或 PodTemplate 打稳定 label / annotation，必要时由 webhook 注入 `schedulerName: koord-scheduler` 和 QoS 元数据，直接走 sandbox fast path。 | 后续阶段。由上游 warm pool / workload 创建或维护 warm Pod，并用 Reservation 表达 warm slot capacity；claim 到来时优先匹配 Reservation。 |
| Non-Pod sandbox | 二期或实验路径。adapter 创建不被普通 Pod 认领的 Reservation，或使用 NodeReservation 做节点级扣减；目标是账本不超卖。 | 后续待设计。需要定义 sandbox 生命周期、Reservation 状态机、唤醒 / 回收语义。 |

非 Pod sandbox 的 Reservation 语义应与 warm capacity 区分：

- **Warm capacity Reservation**：允许匹配的 sandbox Pod / claim 认领；用于预热容量。
- **Accounting-only Reservation**：只做外部 allocation 记账，不允许普通 Pod 认领。可通过 `unschedulable: true`、不可匹配 owner、固定 `nodeName` 等方式表达，但最终机制需要 API review。

对于 accounting-only Reservation，推荐的安全协议是 Reservation-first：adapter 先创建 Reservation，等 Reservation `Available` 且 `status.nodeName` 明确后，再在该节点启动非 Pod sandbox；释放时先停止外部 sandbox，再删除 Reservation。这样可以避免“先跑后补账”或“先删账后停 runtime”的超卖窗口。若外部系统必须先选 node，则 Reservation template 可以带 `nodeName`，Koordinator 只做该节点准入校验。

### 方向 A：Sandbox Colocation and Hardening

Sandbox hardening 分两层：

1. **Scheduling-time accounting**：让调度器看到 sandbox 的真实资源成本。
2. **Node-time isolation policy**：在 Koordinator 已有能力范围内，让 koordlet 和 runtime hooks 应用对应 QoS 和 cgroup-level 设置。

#### QoS 分类

Koordinator 应该提供 policy，把 sandbox workload class 映射到 Koordinator QoS：

- Agent Brain Pod 通常长生命周期、延迟敏感，一般应使用 `LS` 或 `LSR`，具体取决于是否需要 exclusive CPU 语义。
- Skill sandbox Pod 通常短生命周期、突发性强。交互式工具执行可用 `LS`，后台 / 批处理工具可用 `BE`。
- Runtime 本身不应该单独决定 QoS。应结合 runtime class、workload intent、priority 和 namespace policy。

实现上优先复用现有 Koordinator 机制：

- `ClusterColocationProfile` 可以基于 namespace 和 Pod selector 注入 `koordinator.sh/qosClass`、labels、annotations 和 Pod patches。
- sandbox API adapter 可以增加稳定 label，使 `ClusterColocationProfile` 能匹配 sandbox Pod。
- `spec.schedulerName` 应来自上游 PodTemplate、用户配置或专门的 admission 路径；不能假设当前 colocation profile controller 会修改 schedulerName。

#### Runtime Overhead 记账

对于 Kubernetes RuntimeClass-based sandbox，Kubernetes 可能会填充 `pod.spec.overhead`。除非用户明确 opt out，Koordinator 的调度和 quota 路径都必须计入这部分 overhead。实现时需要补充以下测试：

- scheduler resource fitting 计入 `pod.spec.overhead`；
- sandbox Pod 携带 overhead 时的 ElasticQuota 行为；
- Reservation template 与其预留的 sandbox Pod 使用一致的 resource shape。

部分 runtime overhead 与部署环境有关。例如 gVisor `runsc` 进程或 Kata shim 在某些环境中的资源消耗不一定完全被应用容器 request 覆盖。Koordinator 应支持以 runtime class 和 workload profile 为 key 的可配置 runtime overhead template。这类 template 应该叠加到 Kubernetes `pod.spec.overhead`，不能静默替换它。

#### Hardening Policy

第一阶段聚焦资源账本和 Koordinator QoS policy。更深层的隔离执行，例如 runtime-specific cgroup hierarchy tuning、network QoS、filesystem isolation，应在调度模型稳定后，通过 koordlet 和 runtime hooks 增量加入。

### 方向 B：High-Concurrency Sandbox Scheduling

高并发调度是一阶段主战场。issue #2879 提到 equivalence-class scheduling 和 APIServer pressure relief；阶段性设计讨论进一步给出初始性能目标：以 5000 QPS 绑定能力作为阶段性 benchmark 目标。这个数值不应写成 API 语义保证，而应作为性能设计和压测拆解的目标。

高并发问题分三部分：

- 对近似相同 sandbox Pod 重复执行 filter / score；
- 高频 sandbox 生命周期事件造成写入压力、队列压力和 bind 并发压力；
- 当前通用调度 pipeline 对短生命周期、同质化 sandbox 请求偏重，需要为明确 opt-in 的 sandbox workload 提供更轻量路径。

一期优化应优先发生在调度 pipeline 层，而不是只在某个插件里做局部优化。

#### Fast Path Scope

sandbox fast path 只对带明确 label / annotation 的 sandbox Pod 或 Reservation 生效。初始设计可以分三步推进：

1. **极简调度路径**：为 sandbox workload 配置专门 scheduler profile 或内部 fast path，只保留必需的 queue、cache、Filter、Score、Reserve、Bind 逻辑；禁用与 sandbox 场景无关或未证明必要的扩展点。
2. **Reservation-based bind optimization**：对 Reservation 创建、调度和绑定链路做批处理 / 并发优化评估。直接对 Pod 改成批量 bind 会碰到 Kubernetes 一 Pod 一 Binding 的 API 语义；Reservation 层更适合作为内部批处理和容量预留的优化点，但最终写入语义仍需保持可审计、可回滚。
3. **深度调度性能优化**：在 benchmark 定位瓶颈后再引入同质化调度、并发调度、节点分池、调度冲突仲裁等机制。Volcano agent-scheduler 这类实现可以作为参考，但不能直接假设其模型适合 Koordinator。

性能拆解至少应覆盖：

| Stage | What to measure | Possible optimization |
|---|---|---|
| Queue | sandbox burst 入队、排序、退避耗时 | sandbox 专用 queue hint、减少无效重试、同质请求聚合。 |
| Filter / Score | 每个 Pod 重复计算成本 | equivalence-class cache、节点分池、跳过无关插件。 |
| Reserve | Reservation / Pod 占账和冲突处理成本 | 简化 Reservation state transition、冲突仲裁。 |
| Bind | bind 并发、单次 bind latency、失败重试 | bind worker tuning、内部批处理、减少无意义 patch。 |
| API write | APIServer / etcd request rate、p99、error rate | 限速、去重、后续评估 etcd / apiserver 批处理旋钮。 |

#### Equivalence-Class Scheduling

Koordinator 应为 sandbox Pod 引入 equivalence-class fast path。两个 sandbox Pod 在已启用插件所消费的输入上等价时，调度器可以复用部分调度结果。

equivalence class key 至少应包括：

- resource requests、会影响插件判断的 limits、overhead；
- runtime class 和 Koordinator QoS class；
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

第一阶段应评估：

- 减少重复 status / annotation patch；
- 优化 sandbox Pod 的 scheduler bind concurrency 和 backoff；
- 对 Reservation 层做内部 batching，降低重复计算和对象状态抖动；
- 在 Kubernetes API 语义仍要求一 Pod 一 bind 的前提下，做 scheduler 内部工作批处理；
- 在后续 warm capacity 阶段，通过分配预热 sandbox 减少每次 tool call 都创建 cold Pod 的 churn。

性能目标应基于端到端 benchmark，而不只是 raw binding throughput。本地初步 bind microbenchmark 可以证明单段写入能力是否接近 5000 QPS，但 proposal 的验收不应停在 raw bind：必须衡量 create-to-bound latency、queue latency、filter / score latency、reserve latency、bind latency、APIServer request rate、error rate 和 burst load 下的 p99 latency。

### 方向 C：Sandbox Pipeline and Warm Capacity

Sandbox Pipeline 应把上层 Agent 意图连接到 Koordinator 调度原语。

本节保留 issue #2879 中 pre-warming 和 customized pipeline 的完整设计位置，但不作为一期实现目标。一期只需要确保接入层不会阻断后续 warm capacity：Pod / Reservation 模板、label / annotation、runtime overhead 账本都要能复用。

Pipeline 覆盖：

1. **Prepare**：识别 sandbox template、runtime、QoS policy、resources 和 provisioning hints。
2. **Reserve**：为 warm capacity 创建或复用 Reservation。
3. **Warm**：通过上游 sandbox controller 预创建或部分准备 sandbox 环境。
4. **Assign**：有 warm slot 时将 SandboxClaim 或 Pod 匹配到 warm slot；没有时回退 cold scheduling。
5. **Recycle**：将可复用 sandbox 放回 warm pool，或释放 Reservation。
6. **Diagnose**：任一阶段失败时发布结构化调度反馈。

第一版应避免重复定义上游 sandbox API。如果 labels、annotations、`ClusterColocationProfile`、Reservation templates 表达能力不够，可以引入名为 `SandboxPipeline` 的 Koordinator policy object。即使引入，它也应该是上游 sandbox object 之上的薄调度策略包装，而不是替代上游 API。

示意 policy：

```yaml
apiVersion: scheduling.koordinator.sh/v1alpha1
kind: SandboxPipeline
metadata:
  name: gvisor-skill-pipeline
spec:
  selector:
    matchLabels:
      scheduling.koordinator.sh/sandbox: "true"
  runtimeClassName: gvisor
  qosClass: LS
  warmPool:
    ref:
      apiGroup: agents.x-k8s.io
      kind: SandboxWarmPool
      name: skill-gvisor
    targetSize: 10
  runtimeOverhead:
    cpu: 50m
    memory: 50Mi
  reservationTemplate:
    ttl: 10m
    allocatePolicy: Aligned
```

该 YAML 仅为说明形态。最终 API 必须单独经过 API review。

#### Warm Pool Reservation Flow

warm capacity 应在被分配前对 koord-scheduler 可见：

1. adapter 观察 warm pool 或 pipeline policy。
2. 对每个 warm slot，adapter 创建一个 Koordinator Reservation；其 PodTemplate 与未来 sandbox Pod 的调度形态一致。
3. 现有 Reservation 调度路径调度该 Reservation，并在 scheduler cache 中计入其资源。
4. SandboxClaim 或 sandbox Pod 到来时，Reservation affinity 优先匹配对应 warm slot。
5. 没有 warm slot 时，Pod 回退普通 cold scheduling。
6. warm slot 回收或删除时，对应 Reservation 被更新或删除。

Reservation template 必须包含与目标 sandbox 相同的资源账本模型，包括 runtime overhead。否则 warm pool 仍可能导致超卖。

对于 Pod 形态 sandbox，warm capacity 的上层语义应由 agent-sandbox、OpenKruise agents、OpenSandbox 或用户 workload 表达，Koordinator 只负责把可见容量转成 Reservation 并防止重复分配。对于非 Pod sandbox，预热与睡眠唤醒涉及外部 runtime 生命周期，必须在 accounting-only Reservation 状态机稳定后再设计。

### 方向 D：Automated Diagnostics and Feedback

自动诊断应是机器可读的，并且对 Agent 编排器直接有用。调度器不应只返回 “unschedulable”，还应该解释什么动作可能解除阻塞。

Koordinator 已有 `ScheduleExplanation`、`NodeFailedDetails`、`ScheduleSuggestion` 等调度诊断结构。sandbox 支持应扩展或适配这些机制，而不是新增一套独立诊断栈。

sandbox feedback payload 应包含：

| Field | Purpose |
|---|---|
| `reason` | 稳定的机器可读原因，例如 `warmPoolExhausted`、`insufficientRuntimeOverhead`、`insufficientResource`、`reservationUnavailable`、`affinityMismatch`。 |
| `message` | 给人看的调试信息。 |
| `nextStep` | 建议动作，例如 `scaleWarmPool`、`fallbackToColdStart`、`reduceParallelism`、`relaxConstraints`、`retryLater`、`requestCapacity`。 |
| `runtime` | 如相关，填 runtime class family。 |
| `resources` | 失败涉及的资源名。 |
| `preemptMightHelp` | preemption 是否可能让该 sandbox 可调度。 |

反馈可以通过两种方式暴露：

- 写到 sandbox Pod annotation 或 condition；
- 由 adapter 在拥有权限时回写到上游 sandbox object。

实现应 rate-limit feedback 更新，避免在高并发失败时制造更多 APIServer 压力。

### 非 Pod Sandbox 记账

大多数 Kubernetes sandbox runtime 仍以 Pod 形态调度。gVisor、Kata Containers、常见 Wasm 集成应走 Pod 路径。

部分部署可能运行 node-local sandbox：它们消耗 Koordinator 管理节点上的资源，但不创建 Pod。这不是一期主路径，但账本模型不应阻塞后续支持。adapter 可以通过 Reservation 或 NodeReservation 上报这类资源：

- 外部 allocation 是 per-sandbox、高 churn、需要对象级生命周期时，使用 Reservation。
- 只有粗粒度、相对静态的节点级扣减时，使用 NodeReservation。

对于没有真实 Pod owner 的纯记账 Reservation，adapter 必须阻止真实 Pod 认领它，并且必须在外部 sandbox 停止后再删除它。典型表达包括 `unschedulable: true`、不可匹配的占位 owner、固定 `nodeName`，但这些字段组合需要单独 API review。除非社区接受具体集成方案，否则非 Pod sandbox 的调度决策介入保持 future work；一期最多定义记账协议和 adapter 边界。

### 实现细节

本节描述实现边界，不是最终 API contract；API 变更仍需单独 review。

#### API 与 Extension Constants

在 `apis/extension` 下增加一小组 sandbox extension constants：

- sandbox workload label；
- sandbox runtime label；
- sandbox template / equivalence label；
- sandbox pipeline annotation；
- sandbox warm pool annotation；
- sandbox scheduling hint annotation 或 payload type。

这些 constants 应足够稳定，供 controller、scheduler plugin 和文档共享。runtime name 应做规范化，但不应限定为封闭 enum；未知 runtime 应允许存在，并按保守路径处理。

#### Admission 与分类

sandbox classification 可以通过一个或多个 opt-in 路径实现：

- 上游 PodTemplate 已携带 Koordinator labels 和 `schedulerName`；
- sandbox adapter 将上游 Sandbox、SandboxTemplate、SandboxClaim、SandboxWarmPool 对象上的 label 复制到 PodTemplate；
- `ClusterColocationProfile` 选择 sandbox Pod，并注入 QoS labels、annotations、patches；
- 专门 admission hook 在用户 opt into Koordinator scheduling 时设置 `schedulerName`。

实现必须保留用户显式意图。如果用户设置了 QoS class、runtime class、priority、node selector 或 scheduler name，Koordinator 不应静默覆盖，除非被选中的 policy 明确要求覆盖。

#### Reservation 转换 Adapter

Reservation 转换层应按 runtime shape 分流：

- **Pod-shaped sandbox**：adapter 不创建 Reservation；只负责识别上游 Sandbox / PodTemplate，将 sandbox label、runtime label、template hash、QoS policy 和 `schedulerName` 透传到最终 Pod。普通 Pod 直走 fast path，避免把 Pod 路径强行绕进 Reservation 模型。
- **Warm capacity**：后续阶段由 adapter 观察上游 warm pool / workload，根据每个 warm slot 生成 Reservation template，并设置 owner / affinity，使匹配的 sandbox claim 或 Pod 可以认领。
- **Non-Pod accounting**：adapter 根据外部 allocation 创建 accounting-only Reservation 或 NodeReservation。Reservation 对象应带上外部 sandbox id、runtime、resource shape、nodeName、生命周期状态等 label / annotation，便于审计和清理。

转换层不需要兼容所有社区 API。第一阶段只选择重点 API，抽象出可扩展的 adapter interface，后续按项目补充实现。调度上的特殊能力优先通过 Reservation 或 Pod 上的 label / annotation 表达，避免为了单个 sandbox API 修改 scheduler core。

#### Runtime Overhead Policy

runtime overhead accounting 应遵循明确优先级：

1. Kubernetes `pod.spec.overhead` 来自 RuntimeClass admission，存在时是权威 runtime overhead。
2. 当部署环境需要计入 `pod.spec.overhead` 未覆盖的 host-side runtime process 时，Koordinator sandbox runtime overhead policy 可以追加资源。
3. Pod container requests 仍表示应用 workload request。

这些资源之和构成 scheduling 和 reservation accounting shape。任何修改 Pod requests 或 Reservation template 的实现都必须幂等，并暴露最终 accounted shape 方便调试。

#### Equivalence-Class Fast Path

equivalence-class scheduling 应是 scheduler optimization，而不是新的调度语义。

实现要求：

- admission 之后、filter / score 之前计算 equivalence key；
- key 包含所有 opt-in plugin 消费的字段；
- cached results 关联 scheduler snapshot 或等价 invalidation generation；
- 每个 plugin 声明自己的 filter / score 结果是否可基于该 key 复用；
- 对依赖 per-Pod identity、时间、随机选择、可变外部状态或未建模 annotation 的 plugin 禁止复用；
- 记录 cache hit、miss、fallback、invalidation metrics。

第一版可以只让少量插件 opt in。保守的部分缓存可以接受；不安全的全局复用不可接受。

#### Warm Pool Reservation Controller

warm pool integration 应由 controller 或 adapter 实现：从上游 warm capacity 创建并 reconcile Koordinator Reservations。

controller 职责：

- watch 被选择的上游 warm pool 对象；
- 为每个 warm slot 或选定 allocation unit 渲染 Reservation template；
- 设置 Reservation owners，确保只有匹配的 sandbox claim / Pod 能使用 warm capacity；
- 设置 schedulerName 和 resource accounting，使其与未来 sandbox Pod 一致；
- warm slot 过期、回收或 warm pool 缩容时清理 Reservations；
- 通过 template 去重和 rate-limit reconciliation 避免 update storm。

初始设计应优先复用现有 Reservation scheduling path。只有当现有 Reservation affinity 无法表达所需匹配语义时，才需要修改 scheduler plugin。

#### Sandbox Pipeline Policy

`SandboxPipeline` 在第一版中是可选项。只有当上游 sandbox objects、labels、annotations、`ClusterColocationProfile` 和 Reservation templates 的组合无法清晰表达 policy 时，才应引入。

如果引入 `SandboxPipeline`，它应该：

- 默认放在 Koordinator scheduling-oriented API group；除非 API review 决定使用独立 sandbox group；
- 引用上游 sandbox 对象，而不是嵌入或替代它们的完整 schema；
- 保存 Koordinator-specific policy：runtime overhead、QoS class、warm pool reservation policy、scheduling hints、fallback behavior；
- 如果控制 namespaced sandbox API，应设计为 namespaced；只有集群级 policy 才使用 cluster-scoped。

#### 诊断路径

sandbox diagnostics 应复用现有调度诊断机制：

- 从 failed plugin、reason、failed resources、`preemptMightHelp` 等调度失败信息推导结构化 hint；
- 根据选定 API surface，将 hint 写入 Pod annotation / status 或 `ScheduleExplanation`；
- 允许 adapter 把 hint 复制到上游 SandboxClaim 或等价 status；
- rate-limit 写入，并避免重复写入相同 hint。

诊断路径必须足够确定，才能给自动化系统使用。Human-readable message 有调试价值，但自动化应依赖稳定的 reason 和 nextStep。

#### 可观测性

该能力应暴露以下 metrics：

- recognized sandbox Pod 数量；
- sandbox scheduling latency by stage；
- equivalence cache hit / miss / fallback 次数；
- Reservation warm slot 创建、认领、回收、失败次数；
- scheduling hint reason 计数；
- 按 runtime family 统计的 runtime overhead 账本量。

这些 metrics 是性能验证和 opt-in rollout 调试所必需的。

### 里程碑

| Milestone | Scope | Checkable outcome |
|---|---|---|
| M1: Reservation conversion and Pod classification | 定义 sandbox labels / annotations、重点上游 API adapter 行为、非 Pod accounting-only Reservation / NodeReservation 语义。 | PodTemplate-based sandbox 能被 Koordinator 识别并路由到 `koord-scheduler`；非 Pod 接入边界和记账协议被文档化。 |
| M2: Accounting and minimal QoS integration | 将 sandbox labels 与 `ClusterColocationProfile` / admission 集成；验证 `pod.spec.overhead` 和可配置 runtime overhead 在 scheduling / quota / Reservation template 中生效。 | sandbox Pod 或 Reservation 不会因为漏计 runtime overhead 而造成节点超卖。 |
| M3: High-concurrency fast path | 建立 5000 QPS 绑定能力 benchmark；实现 sandbox opt-in fast path、bind concurrency tuning、保守 equivalence-class cache 或等价优化。 | 近似相同 sandbox Pod 的调度 CPU 成本、p99 create-to-bound latency 或 API pressure 下降，且调度正确性不变。 |
| M4: Warm capacity with Reservation | 将上游 warm pool / workload 的 warm slots 转成 Koordinator Reservations，并将 sandbox claims / Pods 匹配到这些 Reservations。 | warm pool capacity 对 scheduler cache 可见，且不会被重复分配。 |
| M5: Sandbox diagnostics and lifecycle feedback | 通过现有诊断路径和 adapter surface 发出结构化 sandbox scheduling hints；补充异常退出状态感知和资源释放。 | Agent 编排器可以区分 warm-pool exhaustion、resource shortage、affinity mismatch、retryable capacity wait；sandbox 异常后不会长期占用账本。 |

这些 milestone 可以放在 feature gate 后分阶段实现，不需要所有 outcome 同时发布。

### 风险与缓解

| Risk | Description | Mitigation |
|---|---|---|
| 不安全的 equivalence reuse | 对并不真正等价的 Pod 复用 filter / score 结果，会产生错误 placement。 | 要求 plugin 显式 opt in；cache invalidation 包含 plugin config 和 scheduler snapshot generation；不确定时回退普通调度。 |
| runtime overhead 漏计 | runtime overhead 会随 runtime 版本、节点 OS、kernel、配置变化。 | overhead template 可配置；计入 Kubernetes `pod.spec.overhead`；增加 e2e test 和 actual usage vs accounted usage 观测。 |
| API 重复建设 | Koordinator-specific Sandbox API 可能重复上游 sandbox API，造成生态割裂。 | 以 Pod / PodTemplate 作为标准边界；优先 adapter 和薄 scheduling policy；新增 CRD 必须先 API review。 |
| warm pool 重复分配 | idle warm capacity 如果没有正确表达，调度器看不到，会被重复分配。 | 用 Reservation 表示 warm slot，并保证 resource template 一致；明确 claim / recycle transition 并测试。 |
| diagnostics 带来 APIServer 压力 | 高并发失败可能导致大量 status / annotation patch。 | 对 feedback 更新限速，去重相同 hints，优先在可用时更新 higher-level object status。 |
| QoS 分类错误 | 仅凭 runtime 映射 QoS 可能过度保护短任务，或低估交互式任务。 | 综合 runtime、workload intent、namespace policy 和显式 user override；避免 runtime-only 硬编码 QoS 决策。 |
| 安全边界误导 | 调度策略本身不能保证 filesystem、network、runtime isolation。 | 明确 runtime 和集群前置条件；本 proposal 只覆盖 Koordinator 调度、账本和 QoS 集成。 |

## 备选方案

**只使用 Pod label 和 fast path**

实现简单，但无法解决 warm pool capacity、runtime overhead policy，也无法把诊断反馈关联到上游 sandbox API。

**定义完整 Koordinator Sandbox CRD**

Koordinator 可以获得完整 API surface，但这会重复上游 sandbox 工作，也与 issue 中“支持 Agent sandbox orchestration，而非替代它”的方向冲突。

**只优化 binding**

binding optimization 只覆盖链路中的一段。issue 明确包含重复调度计算、APIServer pressure、warm pipeline orchestration 和 diagnostics，因此仅优化 bind 不够。

**所有 warm capacity 都用 NodeReservation**

NodeReservation 适合静态节点级扣减，但 warm sandbox slot 是 per-sandbox、高 churn。Reservation 更适合对象级 warm capacity 和后续 claim matching。

**把 diagnostics 留给 Agent 平台**

Agent 平台可以推断部分失败原因，但 Koordinator 拥有解释资源约束、preemption potential、plugin-specific failure 所需的 scheduler context。导出结构化 hint 可以避免每个平台重复实现 scheduler reasoning。

## 升级策略

所有新行为都应 opt-in：

- sandbox classification 需要显式 labels、selectors、上游 adapter 配置或 pipeline policy。
- QoS injection 尽量使用现有 profile 和 admission 机制。
- equivalence-class scheduling 应由 scheduler feature gate 控制；在正确性和 benchmark 被接受前默认关闭。
- warm pool Reservation integration 是后续阶段能力，只影响被 pipeline 或 adapter 选中的 sandbox。
- diagnostics 是 additive，不应改变调度决策；第一阶段只保留必要生命周期状态感知。

现有非 sandbox Pod 应继续使用当前调度路径。

建议 rollout controls：

| Capability | Default | Rollout control |
|---|---|---|
| Sandbox labels and constants | Available | 没有 selector 或 adapter 时不产生行为变化。 |
| QoS classification for sandbox Pods | 未配置 policy 时关闭 | `ClusterColocationProfile` selectors 或 admission 配置。 |
| Runtime overhead templates | 未配置 policy 时关闭 | Runtime class policy 和 namespace / workload selectors。 |
| Non-Pod Reservation accounting adapter | 关闭 | adapter feature gate、外部 runtime allowlist、Reservation owner / nodeName policy。 |
| Equivalence-class scheduling | 关闭 | scheduler feature gate 和 plugin opt-in list。 |
| Warm pool Reservation sync | 关闭 | controller feature gate 和被选择的上游 warm pool references。 |
| Sandbox scheduling hints | alpha 阶段关闭或只写 annotation | scheduler feature gate 和 rate-limit 配置。 |

升级时可以按以下顺序启用：classification、QoS / overhead accounting、非 Pod 记账 adapter（如需要）、sandbox fast path / equivalence-class scheduling、warm pool Reservation sync、sandbox diagnostics。

## 测试计划

Unit tests：

- sandbox label / annotation parsing；
- Reservation conversion adapter 的 Pod / warm / non-Pod 分流；
- equivalence-class key generation and invalidation；
- plugin opt-in behavior for cached filter / score reuse；
- runtime overhead template 与 `pod.spec.overhead` 合并；
- accounting-only Reservation template 生成和不可认领语义；
- 后续阶段：warm pool slot 的 Reservation template 生成；
- 后续阶段：sandbox diagnosis reason 与 nextStep 映射。

Integration tests：

- PodTemplate-based sandbox API 创建带 Koordinator 调度元数据的 Pod；
- `ClusterColocationProfile` 对选中的 sandbox Pod 应用 QoS label；
- 非 Pod adapter 创建 accounting-only Reservation 后，普通 Pod 不能认领该 Reservation；
- 非 sandbox Pod 不受 sandbox feature gate 影响。
- 后续阶段：warm pool slot 创建 Reservation，sandbox Pod 能 claim 匹配的 Reservation；
- 后续阶段：sandbox 调度失败时生成稳定 scheduling hint。

Performance tests：

- 5000 QPS 绑定能力 microbenchmark，并拆分 bind latency、APIServer request rate、error rate；
- 近似相同 sandbox Pod 的 burst scheduling；
- Brain Pod、Skill sandbox Pod、普通 service Pod 混合调度；
- sandbox churn 下的 APIServer request rate 和 p99 latency。
- 后续阶段：warm-pool assignment latency 对比 cold scheduling latency。

End-to-end tests：

- 携带 `pod.spec.overhead` 的 RuntimeClass-based sandbox Pod；
- 测试环境支持时验证 gVisor / Kata / Wasm 路径；否则使用 simulated runtime；
- 后续阶段：warm slot create、claim、recycle、delete 全链路 Reservation lifecycle。

## 开放问题

- `SandboxPipeline` 是否需要作为新的 Koordinator CRD，还是第一版可以通过 `ClusterColocationProfile`、annotations 和 Reservation templates 表达？当前倾向第一版不引入。
- 除 `kubernetes-sigs/agent-sandbox` 外，第一阶段还应选择哪些重点 API？是否需要覆盖 OpenKruise agents、OpenSandbox、scitix 这类 PodTemplate-based API？
- 5000 QPS 绑定目标应拆成哪些 benchmark：raw bind、create-to-bound、调度 throughput、p99 latency、APIServer / etcd request pressure？
- sandbox fast path 第一版到底砍掉哪些非必要 scheduler 模块？
- Reservation-based batch bind 的语义边界是什么？哪些优化可以在 Reservation 层做，哪些会违反 Kubernetes Pod Binding 语义？
- 第一版哪些 scheduler plugins 可以安全 opt in 到 equivalence-class reuse？
- runtime overhead 默认值应该放在 Koordinator、RuntimeClass 配置、sandbox pipeline policy，还是三者都有并定义优先级？
- sandbox scheduling hints 的最终 API surface 是 Pod annotation、Pod condition、ScheduleExplanation、上游 SandboxClaim status，还是组合？
- accounting-only Reservation 应使用 `unschedulable: true`、占位 owner、固定 `nodeName` 的哪种字段组合？
- warm pool Reservations 应如何与 `allocateOnce`、`preAllocationPolicy`、同一 pool 的多个 sandbox claims 交互？

## 实现历史

- [x] 2026-04-28：issue [#2879](https://github.com/koordinator-sh/koordinator/issues/2879) 创建。
- [x] 2026-05-12：maintainer 在 issue 讨论中要求补充 Sandbox API adaptations/awareness 章节。
- [x] 2026-06-26：创建 proposal 初稿。
- [x] 2026-06-30：围绕 issue-backed outcomes 重写 proposal。
- [x] 2026-06-30：将草稿改为中文，便于继续迭代设计。

## 参考资料

- [Issue #2879: Native support for AI Agent orchestration and high-concurrency Sandbox scheduling](https://github.com/koordinator-sh/koordinator/issues/2879)
- [Maintainer comment requesting Sandbox API adaptations/awareness](https://github.com/koordinator-sh/koordinator/issues/2879#issuecomment-4430422335)
- [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox)
- [Koordinator Reservation proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20220609-resource-reservation.md)
- [Koordinator NodeReservation proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/scheduling/20221227-node-resource-reservation.md)
- [Koordinator QoS Manager proposal](https://github.com/koordinator-sh/koordinator/blob/main/docs/proposals/koordlet/20220615-qos-manager.md)
