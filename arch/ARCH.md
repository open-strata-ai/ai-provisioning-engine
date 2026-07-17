# ai-provisioning-engine · 架构文档

> 对应设计文档 §1（定位与边界）、§2（职责清单）、§3（核心抽象与接口）、§6（外部适配器）
> 平台版本 v1.4.0 | 领域：assembly（装配域 · 供给/部署引擎）| 必选性：core

---

## §1 定位

### 1.1 一句话定位

`ai-provisioning-engine` 是 OpenStrata 装配中枢的**后半程**——部署/供给引擎。它消费 `ai-dependency-resolver` 产出的 **AssemblyPlan**，把计划落地为 **Helm Values / Compose / Operator 配置**，并通过 **ArgoCD 或直连 Helm** 应用到运行环境，实现「增量部署、滚动更新、零停机」。

### 1.2 解决的唯一问题

把"一份确定的装配计划"变成"对环境的安全、可观测、可回滚的实际变更"——只动差异部分，已运行服务不重启。

### 1.3 系统位置

```
ai-dependency-resolver
  │ AssemblyPlan (+ Checksum)
  ▼
ai-provisioning-engine  ← 本仓（只执行、不算依赖）
  │ Render + Apply
  ▼
K8s Cluster / Compose（ArgoCD/Helm/Docker）
```

### 1.4 边界声明

- **入**：`AssemblyPlan`（Added/Reused/Removed + Checksum，来自 resolver）
- **出**：`ApplyResult[]`（各组件部署状态）+ 组件 Ready 信号 → 门户
- **不负责**：依赖解析与 Plan 生成（ai-dependency-resolver）；组件运行时管理（各自运行时服务自身）
- **核心原则**：增量部署、滚动更新、零停机（§13.3 三原则）

### 1.5 与其他 Go 组件的关系

| 组件 | 关系 | 说明 |
|------|------|------|
| ai-dependency-resolver | 上游生产者 | 消费其 AssemblyPlan |
| ai-cli | 上游调用方 | `aictl up/apply/rollback` 透传调用 |
| ai-guide-portal | 上游调用方 | 门户触发执行编排，本仓为执行内核 |
| 被部署组件 | 被装配者 | gateway/sandbox 等为其装配目标，不直接调用运行时 |

### 1.6 必选性

**core**（§13.3 装配中枢）。阶段一~三由 `aictl up` 驱动 Compose；full 档由引导门户 + ArgoCD GitOps 驱动。

---

## §2 职责

| # | 职责 | 必选 | 说明 |
|---|------|------|------|
| R1 | Plan 消费与校验 | core | 读取 AssemblyPlan，预检资源/依赖/冲突 |
| R2 | 渲染配置 | core | Plan → Helm Values / Compose / Operator（按 profile） |
| R3 | 增量部署 | core | 仅部署/变更差异，不停已有组件 |
| R4 | 滚动更新 | core | 多副本滚动 + 探针保活 |
| R5 | 灰度升级 | core | 影响面大者双写校验后切 |
| R6 | 回滚 | core | 声明式回滚（enabled=false 重放） |
| R7 | 状态回传 | core | 组件 Ready 状态回传门户 |

### 职责交互矩阵

```
  AssemblyPlan
       │
  ┌────▼─────┐
  │ R1 预检   │──失败→ 阻断
  └────┬─────┘
       │ 通过
  ┌────▼─────┐    ┌──────────┐
  │ R2 渲染   │◄───│ profile  │
  └────┬─────┘    └──────────┘
       │ Helm Values/Compose/Manifest
  ┌────┴─────────────────────┐
  │       R3 增量部署         │
  │  Added → deploy          │
  │  Removed → decommission  │
  │  Reused → skip           │
  └────┬─────────────────────┘
       │
  ┌────┴──────┬──────────┐
  ▼           ▼          ▼
R4 滚动    R5 灰度    R6 回滚
  │           │          │
  └─────┬─────┴──────────┘
        ▼
  R7 状态回传 → 门户
```

---

## §3 核心接口

### 3.1 领域层类型定义（`domain/`）

```go
package domain

import "context"

// ============================================================
// 输入（消费方模型）
// ============================================================

// AssemblyPlan 来自 ai-dependency-resolver 的装配计划
type AssemblyPlan struct {
    Added    []PlannedComponent
    Reused   []PlannedComponent
    Removed  []PlannedComponent
    Checksum string
}

// PlannedComponent 计划中的单个组件
type PlannedComponent struct {
    RepoName  string   // 自研 App 名或 OSS 实例名
    Kind      string   // app | oss
    Version   string   // 钉死版本
    Capability string
    DependsOn []string
}

// ============================================================
// 产出模型
// ============================================================

// RenderOutput 渲染产物
type RenderOutput struct {
    Kind      string            // helm-values | compose | k8s-manifest
    Artifacts map[string][]byte // 文件名 → YAML 内容
}

// ApplyResult 单个组件的执行结果
type ApplyResult struct {
    Component string // 组件名
    Action    string // add | reuse | remove | rolling-update | gray-cutover
    Status    string // success | failed | in-progress
    Revision  string // 部署 revision（用于回滚）
    Message   string // 人类可读描述
}

// ComponentStatus 组件运行时状态
type ComponentStatus struct {
    Name     string
    Ready    bool
    Version  string
    Replicas int
    Message  string
}
```

### 3.2 领域 Port（解耦点）

```go
// Deployer 是部署执行的领域 Port
// 实现位于 infrastructure/adapter/，支持多 SPI 实现并存
type Deployer interface {
    // Render 将 Plan 按 profile 渲染为目标部署配置
    Render(ctx context.Context, plan AssemblyPlan, profile string) (RenderOutput, error)

    // Apply 将渲染产物应用到目标环境
    Apply(ctx context.Context, out RenderOutput) ([]ApplyResult, error)

    // Rollback 将组件回滚到指定版本
    Rollback(ctx context.Context, component string, toRevision string) error

    // Status 查询组件当前运行状态
    Status(ctx context.Context, component string) (ComponentStatus, error)
}

// CICDPort 是部署工具的 SPI 端口（interface_versions.CICD = 1.0.0）
// ArgoCD / Istio 等实现此接口
type CICDPort interface {
    Sync(ctx context.Context, manifest []byte) error
    RollbackTo(ctx context.Context, revision string) error
}
```

### 3.3 包结构（DDD 四层）

```
cmd/provisioner/              # 入口：Gin HTTP server + gRPC
├── domain/
│   ├── model.go              # AssemblyPlan, RenderOutput, ApplyResult
│   ├── port.go               # Deployer, CICDPort interfaces
│   ├── service.go            # provisionerService（编排实现）
│   └── service_test.go
├── application/
│   └── usecase/
│       ├── apply.go          # 编辑：预检→渲染→应用→回传
│       └── rollback.go       # 回滚编排
├── infrastructure/
│   ├── adapter/
│   │   ├── helm_adapter.go   # HelmAdapter：Helm install/upgrade/rollback
│   │   ├── compose_adapter.go# ComposeAdapter：docker-compose up/down
│   │   └── argocd_adapter.go # ArgoCDAdapter：GitOps sync/rollback
│   ├── config/
│   │   └── config.yaml       # provisioner.mode, rollout, grayCutover
│   └── persistence/
│       ├── pg_record.go      # provisioning_record 表操作
│       └── redis_lock.go     # 分布式执行锁
└── api/
    └── handler/
        ├── apply.go          # POST /v1/apply
        ├── rollback.go       # POST /v1/rollback
        ├── status.go         # GET  /v1/status/{component}
        └── result.go         # GET  /v1/plan/{checksum}/apply-result
```

### 3.4 接口契约：请求/响应

```go
// ApplyRequest
type ApplyRequest struct {
    Plan     AssemblyPlan `json:"plan"`
    Profile  string       `json:"profile"`
    TenantID string       `json:"tenant_id"`
}

// ApplyResponse
type ApplyResponse struct {
    Results  []ApplyResult `json:"results"`
    PlanRef  string        `json:"plan_checksum"`
    Summary  string        `json:"summary"`
}

// RollbackRequest
type RollbackRequest struct {
    Component  string `json:"component"`
    ToRevision string `json:"to_revision"` // 空=回滚到上一版本
}
```

---

## §6 适配器

### 6.1 SPI 适配器矩阵

| SPI 端口 | 角色 | 外部组件 | 默认/备选 | Adapter |
|----------|------|----------|-----------|---------|
| CICD (1.0.0) | 消费方 | ArgoCD（optional）+ Istio（optional，full） | 备选/备选 | `ArgoCDAdapter` |
| Cache (1.0.0) | 消费方 | Redis（core） | ✅ 唯一 | 分布式锁 + 状态缓存 |
| Tracing (1.0.0) | 消费方 | OTel（core） | ✅ 唯一 | 部署链路 trace |
| 部署目标 | 直接驱动 | Kubernetes / Docker Compose | ✅ 唯一 | HelmAdapter / ComposeAdapter |

### 6.2 CICD 默认关逻辑

ArgoCD/Istio 为 **optional**（仅 full 档），因此本仓在 starter/standard/advanced 走**直连 Helm/Compose**，full 档走 ArgoCD。

```go
// 渲染器选择逻辑（infrastructure/adapter/ 工厂）
func SelectDeployer(mode string, profile string) Deployer {
    switch {
    case mode == "argocd" || profile == "full":
        return NewArgoCDAdapter(...)
    case mode == "helm" || profile == "advanced" || profile == "standard":
        return NewHelmAdapter(...)
    case mode == "compose" || profile == "starter":
        return NewComposeAdapter(...)
    }
}
```

### 6.3 防腐层设计

每个 Adapter 实现 `Deployer` 接口，切换部署目标零业务改动：

```go
type HelmAdapter struct {
    namespace string
    kubeconfig string
    maxSurge   int
    maxUnavailable int
}

func (a *HelmAdapter) Render(ctx context.Context, plan AssemblyPlan, profile string) (RenderOutput, error)
func (a *HelmAdapter) Apply(ctx context.Context, out RenderOutput) ([]ApplyResult, error)
func (a *HelmAdapter) Rollback(ctx context.Context, component string, toRevision string) error
func (a *HelmAdapter) Status(ctx context.Context, component string) (ComponentStatus, error)
```

```go
type ComposeAdapter struct {
    composeFiles []string
    projectName  string
}

// 实现同上 Deployer 接口
```

### 6.4 渲染流程

```
Plan (Added + Removed)
  │
  ▼
按 profile 选择渲染器 → 读取元仓 dependencies/config/ + 本仓 infrastructure/config/
  │
  ▼
合并 Values（优先级）：用户覆盖 > profile > bom 默认
  │
  ▼
RenderOutput（Helm Values YAML / compose.yaml / K8s manifest）
```

### 6.5 适配器测试策略

| 测试类型 | 覆盖 | 环境 |
|----------|------|------|
| 单测 | Render 字段正确性、滚动策略逻辑 | Go test |
| 契约测试 | Helm/Compose/ArgoCD 三种适配器同一契约 | SPI 多实现一致性 |
| 集成测试 | kind 集群 + mock ArgoCD | 增量子集不重启、滚动探针 |
| 混沌测试 | 部署中途 kill Pod | 重试/超时→回滚 |

---

> 详细处理流水线参见 [design/DESIGN.md §4](../design/DESIGN.md#4-处理流水线--请求路径输入依赖展开计划生成执行)
> 并发/安全规则参见 [skills/SKILLS.md](../skills/SKILLS.md)
> API 端点与部署详情参见 [specs/SPECS.md](../specs/SPECS.md)
