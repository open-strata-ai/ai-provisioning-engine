# ai-provisioning-engine · 接口/数据/部署规格

> 对应设计文档 §7（API/CLI）、§8（数据模型与存储）、§11（配置与部署）

---

## §7 API / CLI

### 7.1 HTTP API（Gin）

| 方法 | 路径 | 说明 | 请求体/参数 | 响应 |
|------|------|------|-------------|------|
| POST | `/v1/apply` | 提交 AssemblyPlan，执行部署 | `ApplyRequest`（JSON） | `ApplyResponse` |
| POST | `/v1/rollback` | 回滚组件到指定 revision | `RollbackRequest`（JSON） | `RollbackResponse` |
| GET | `/v1/status/{component}` | 查询组件运行状态 | path: component name | `ComponentStatus` |
| GET | `/v1/plan/{checksum}/apply-result` | 查询 Plan 执行结果 | path: checksum | `ApplyResult[]` |
| GET | `/healthz` | 存活/就绪探针 | — | `{"status":"ok"}` |
| GET | `/metrics` | Prometheus 指标 | — | text/plain |

### 7.2 请求/响应 Schema

**ApplyRequest**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| plan | AssemblyPlan | 是 | 来自 resolver 的装配计划 |
| profile | string | 是 | starter/standard/advanced/full |
| tenant_id | string | 是 | 租户标识 |

**ApplyResponse**:

| 字段 | 类型 | 说明 |
|------|------|------|
| results | []ApplyResult | 每个组件的执行结果 |
| plan_checksum | string | 对应 Plan |
| summary | string | 汇总（N added, M reused, K removed） |

**ApplyResult**:

| 字段 | 类型 | 说明 |
|------|------|------|
| component | string | 组件名 |
| action | string | add / reuse / remove / rolling-update / gray-cutover |
| status | string | success / failed / in-progress |
| revision | string | 部署 revision（回滚用） |
| message | string | 错误原因或成功描述 |

**RollbackRequest**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| component | string | 是 | 组件名 |
| to_revision | string | 否 | 目标 revision，空=回滚到上一版 |

### 7.3 CLI 命令（经 `ai-cli` 透传）

| 命令 | 说明 | 参数 |
|------|------|------|
| `aictl up --profile starter` | 渲染 Compose + 拉起核心组件 | `--profile <p>` `--detach` |
| `aictl apply --plan <checksum>` | 应用某 Plan | `--plan <checksum>` |
| `aictl rollback --component <name>` | 回滚组件 | `--component <name>` |

---

## §8 数据模型

### 8.1 持久化表

**provisioning_record**（PostgreSQL，core）：

| 列 | 类型 | 说明 |
|----|------|------|
| id | BIGSERIAL PRIMARY KEY | 自增主键 |
| plan_checksum | TEXT NOT NULL | 对应 AssemblyPlan |
| tenant_id | TEXT NOT NULL | 租户标识 |
| component | TEXT NOT NULL | 组件名 |
| action | TEXT NOT NULL | add/reuse/remove/rolling-update/gray-cutover |
| revision | TEXT | 部署 revision（Helm release rev 或 ArgoCD commit） |
| status | TEXT NOT NULL | success/failed/in-progress |
| error_detail | TEXT | 失败原因 |
| created_at | TIMESTAMPTZ DEFAULT now() | 时间戳 |

**索引**：

```sql
CREATE INDEX idx_provisioning_record_checksum ON provisioning_record(plan_checksum);
CREATE INDEX idx_provisioning_record_component ON provisioning_record(component, created_at DESC);
CREATE INDEX idx_provisioning_record_tenant ON provisioning_record(tenant_id, created_at DESC);
```

### 8.2 Redis 键

| 键模式 | 数据类型 | TTL | 说明 |
|--------|----------|-----|------|
| `provisioner:lock:{component}` | string（SET NX） | 60s | 分布式执行锁 |
| `provisioner:status:{component}` | JSON | 5min | 组件运行状态缓存 |
| `provisioner:apply:{checksum}` | JSON | 1h | Plan 执行结果缓存 |
| `provisioner:revision:{component}` | list | 无 | 组件 revision 历史（最多 10 条） |

### 8.3 配置合并来源

| 来源 | 路径 | 优先级 | 说明 |
|------|------|--------|------|
| App 局部配置 | `infrastructure/config/<component>.yaml` | 1（最高） | 组件仓自带 |
| Profile overlays | `openstrata-meta/profiles/<p>/overlays/` | 2 | 各档覆盖 |
| BOM 默认 | `openstrata-meta/bom.yaml` | 3（最低） | 全局默认 |

### 8.4 待下线组件追踪

```sql
CREATE TABLE decommission_log (
  id          BIGSERIAL PRIMARY KEY,
  component   TEXT NOT NULL,
  reason      TEXT,
  deactivated_at TIMESTAMPTZ,
  verified    BOOLEAN DEFAULT false,  -- 确认无流量后置 true
  verified_at TIMESTAMPTZ
);
```

---

## §11 部署

### 11.1 部署形态

| 属性 | 值 |
|------|-----|
| 命名空间 | `ai-system`（§9.2） |
| 副本数 | 2（多副本） |
| CICD | starter/standard/advanced → 直连 Helm；full → ArgoCD GitOps 自举 |

### 11.2 K8s 资源配置

| 资源类型 | Requests | Limits |
|----------|----------|--------|
| CPU | 200m | 1000m |
| Memory | 256Mi | 1024Mi |

**说明**：渲染 Helm Values 和调用 K8s API 需要一定内存，limits 预留余量。

### 11.3 探针

| 探针 | 路径 | 初始延迟 | 周期 | 失败阈值 |
|------|------|----------|------|----------|
| 存活 liveness | `GET /healthz` | 5s | 10s | 3 |
| 就绪 readiness | `GET /healthz` | 5s | 10s | 3 |

就绪探针额外校验：K8s API Server 可达性（`kubectl version` 或 client-go ping）。

### 11.4 滚动更新

```yaml
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0
minReadySeconds: 10
```

### 11.5 配置键清单

| 配置键 | 默认值 | 说明 |
|--------|--------|------|
| `provisioner.mode` | `helm` | 部署模式：helm / compose / argocd |
| `provisioner.argocd.enabled` | `false` | full 档置 true |
| `provisioner.argocd.namespace` | `ai-system` | ArgoCD 管理命名空间 |
| `provisioner.rollout.maxSurge` | `1` | 滚动更新最大激增数 |
| `provisioner.rollout.maxUnavailable` | `0` | 滚动更新最大不可用数 |
| `provisioner.rollout.probeGraceSeconds` | `30` | 探针等待宽限期 |
| `provisioner.grayCutover.doubleWriteVerify` | `true` | 灰度切换双写校验 |
| `provisioner.metaRepo.profilesPath` | `openstrata-meta/profiles` | Profile 目录 |
| `provisioner.metaRepo.configPath` | `openstrata-meta/dependencies/config` | 组合级配置 |
| `provisioner.maxParallelDeploy` | `8` | 并行部署上限（信号量） |
| `provisioner.applyTimeout` | `300` | Apply 超时秒数 |
| `provisioner.readyTimeout` | `30` | 组件就绪等待秒数 |

### 11.6 部署依赖

| 依赖 | 类型 | 必选 | 说明 |
|------|------|------|------|
| K8s API Server / Docker | 运行时 | 是 | 部署目标 |
| Helm / kubectl / docker CLI | 工具 | 是 | 根据 mode 需要 |
| ArgoCD | CICD | full | full 档 GitOps |
| PostgreSQL | 数据库 | 否 | 审计记录持久化 |
| Redis | 缓存 | 否 | 分布式锁 |
| OTel Collector | 可观测 | 否 | Trace 上报 |

### 11.7 环境变量

| 变量 | 说明 | 示例 |
|------|------|------|
| `CONFIG_PATH` | 配置文件路径 | `/etc/provisioner/config.yaml` |
| `KUBECONFIG` | K8s 配置 | `/etc/provisioner/kubeconfig` |
| `PROVISIONER_MODE` | 部署模式 | `helm` |
| `PG_DSN` | PostgreSQL 连接串 | `postgres://...` |
| `REDIS_ADDR` | Redis 地址 | `redis:6379` |
| `ARGOCD_SERVER` | ArgoCD 服务地址 | `argocd-server.ai-system:443` |
| `OTEL_ENDPOINT` | OTel 导出端点 | `otel-collector:4317` |

---

> 核心接口与包结构参见 [arch/ARCH.md](../arch/ARCH.md)
> 算法/并发/安全规则参见 [skills/SKILLS.md](../skills/SKILLS.md)
> 完整处理流水线参见 [design/DESIGN.md §4](../design/DESIGN.md#4-处理流水线--请求路径输入依赖展开计划生成执行)
