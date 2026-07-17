# ai-provisioning-engine · 算法/并发/安全规则

> 对应设计文档 §5（关键算法）、§9（并发与性能）、§12（可观测性/安全）

---

## §5 关键算法

### 5.1 配置渲染（Plan → 部署配置）

**输入**：AssemblyPlan + profile + 元仓局部 config
**输出**：Helm Values / Compose YAML / K8s Manifest

```
function render(plan, profile):
    renderer = selectRenderer(profile)
    // starter → Compose; standard+/advanced → Helm; full → ArgoCD

    values = {}
    for component in (plan.Added ∪ plan.Changed):
        base    = loadFromBom(component.version)     // bom.yaml 钉死版本
        local   = loadFromProfile(profile, component) // profile external 段
        appConf = loadFromAppConfig(component)       // infrastructure/config/ 局部
        merged  = merge(local, appConf, base)        // 优先级: app > profile > bom
        values[component.name] = merged

    return RenderOutput(kind=renderer.kind, artifacts=renderer.marshal(values))
```

### 5.2 增量应用

```
function diffApply(plan, currentState):
    added   = plan.Added   - currentState   // 新增部署
    removed = plan.Removed ∩ currentState   // 确认下线
    changed = plan.Added   ∩ currentState but version/config diff  // 滚动更新
    reused  = plan.Reused  ∩ currentState   // 跳过

    for a in added:     deploy(a)
    for r in removed:   decommission(r)
    for c in changed:   rollingUpdate(c)
    for ru in reused:   skip(ru)           // 不重启已运行服务
```

**原则**：只动差异，已运行服务不重启（除非配置变更才滚动）。

### 5.3 滚动更新

```
function rollingUpdate(component):
    desired = component.replicas
    for each replica:
        startNew(replica)
        waitForReady(replica)     // 探针检查
        terminateOld(replica)
    // 参数: maxSurge=1, maxUnavailable=0
    // 每次只替换1个，始终保持全部可用
```

### 5.4 灰度升级（双写校验）

影响面大的切换（如向量库 Milvus↔Qdrant）：

```
function grayCutover(oldComponent, newComponent):
    // 阶段1：双写
    parallel:
        write(oldComponent, data)
        write(newComponent, data)

    // 阶段2：校验一致性
    for each record:
        assert read(oldComponent, key) == read(newComponent, key)

    // 阶段3：切流量
    if verifyPass:
        switchTraffic(newComponent)

    // 阶段4：退役旧组件
    markForRemoval(oldComponent)
```

**约束**：非零停机，需运维窗口；仅 full 档启用。

### 5.5 回滚

```
function rollback(component, fromRevision):
    // 声明式：重放 Plan 中 enabled=false/oldVersion
    revision = getLastRevision(component)
    manifest = renderFromRevision(revision)
    cicd.RollbackTo(manifest, revision)
```

---

## §9 并发与性能

### 9.1 执行模型

Gin 管理 API；执行编排热路径可上 Hertz/go-zero（低延迟）。

### 9.2 并行策略

- **组件并行部署**：`Plan.Added` 中无依赖关系的组件可并行起 goroutine + WaitGroup
- **拓扑排序**：有共享依赖（如先 PG 后依赖它的服务）按拓扑顺序串行
- **Redis 分布式锁**：同一组件并发 Apply 用 `SET NX` 串行化，锁 TTL 60s
- **全局信号量**：限制并发 Apply 数，避免压垮 K8s API Server

### 9.3 进度回传

```go
type ProgressChan chan ComponentProgress

type ComponentProgress struct {
    Component  string
    Phase      string // rendering | deploying | probing | done
    Progress   int    // 0-100
    Error      error
}

// 门户实时看板消费此 channel（§13.1 状态看板）
```

### 9.4 资源消耗

| 场景 | CPU | 内存 | 说明 |
|------|-----|------|------|
| 空转 | ~10m | ~32Mi | Gin server 空闲 |
| 单组件 Apply | ~50m | ~128Mi | Helm install/upgrade |
| 多组件并行 Apply | ~500m | ~512Mi | 信号量限制并发度 |
| 灰度双写 | ~200m | ~256Mi | 读写校验 |

### 9.5 拓扑排序编排（并行部署）

```go
// 按拓扑顺序编排组件部署，无依赖的并行执行
func deployByTopology(plan AssemblyPlan, deployer Deployer) error {
    graph := buildDependencyGraph(plan.Added)
    layers := graph.TopologicalLayers() // 每层内无依赖关系

    for _, layer := range layers {
        var wg sync.WaitGroup
        errCh := make(chan error, len(layer))

        for _, comp := range layer {
            wg.Add(1)
            go func(c PlannedComponent) {
                defer wg.Done()
                if err := deployer.Apply(ctx, c); err != nil {
                    errCh <- err
                }
            }(comp)
        }
        wg.Wait()
        close(errCh)

        if err := firstOrNil(errCh); err != nil {
            return fmt.Errorf("layer deploy failed: %w", err)
        }
        // 层间串行：等当前层全部 Ready 后再部署下一层
    }
    return nil
}
```

### 9.6 性能规则

| # | 标题 | 触发条件 | 约束 | 示例 |
|---|------|----------|------|------|
| P1 | goroutine 并发部署 | Plan Added > 1 | 无依赖组件并行，有依赖按拓扑串行 | `go deploy(a); go deploy(b); wg.Wait()` |
| P2 | 分布式锁串行 | 同组件并发 Apply | Redis SET NX，TTL 60s | `if !lock.Acquire("apply:milvus")` → retry |
| P3 | 信号量限并发 | 全局 Apply 数量 | `weighted.New(8)` | `sem.Acquire(ctx, 1)` |
| P4 | Context 传播 | 每个 Apply | 超时 300s，支持 Ctrl-C 取消 | `ctx, cancel := context.WithTimeout(...)` |
| P5 | 探针等待超时 | 部署后探测 Ready | 每个组件 30s 超时+3 次重试 | `waitForReady(component, 30s)` |
| P6 | 进度 channel 非阻塞 | 状态回传 | buffered(100)，满则丢弃旧 | `select { case ch <- p: default: }` |

---

## §12 安全

### 12.1 安全边界

执行属平台自身变更，所有操作必须审计。部署操作涉及实际生产环境变更，安全要求极高。

### 12.2 安全规则

| # | 标题 | 触发条件 | 约束 | 示例 |
|---|------|----------|------|------|
| S1 | 预检阻断 | Apply 前 | Plan 冲突/配额不符→直接阻断 | `if conflicts > 0: abort()` |
| S2 | 全量审计 | 每次 Apply/Rollback | 即便 security 未开，平台本身变更留痕 | `INSERT INTO provisioning_record(...)` |
| S3 | RBAC 隔离 | ArgoCD 操作 | 只动 ai-system + 租户命名空间 | ServiceAccount 仅榜单命名空间 |
| S4 | Plan Checksum 校验 | 接收 Plan | Checksum 必须与 Resolver 产出一致 | `if plan.checksum != expected: reject` |
| S5 | 回滚限制 | 回滚仅允许到历史 revision | revision 表中必须存在该记录 | `if !hasRevision(comp, rev): reject` |
| S6 | 部署目标可达校验 | 每次 Apply | K8s API Server/Compose 连通性预检 | `if !ping(target): err("unreachable")` |
| S7 | 并发锁防误操作 | 同组件并发 | 分布式锁 + 幂等 token | `lock("provisioner:" + component)` |
| S8 | 敏感值脱敏 | 日志/输出 | Helm Values 中 secrets 不打印 | `maskSensitive(values)` before log |

### 12.3 审计要求

每次 Apply/Rollback 记录：
- plan_checksum、component、action（add/remove/rolling-update/gray-cutover）
- revision（部署版本号）、status、错误信息
- tenant、操作者、时间戳

Prometheus 指标：
- `apply_total`、`apply_duration_seconds`、`apply_errors_total`
- `rollback_total`、`component_ready_duration_seconds`

### 12.4 风险场景与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| 部署中途失败导致不一致 | 部分组件新版、部分旧版 | 拓扑串行依赖 + 失败回滚 |
| 并发 Apply 同一组件 | 配置冲突 | Redis 分布式锁 + 幂等 token |
| 灰度切换数据不一致 | 向量读写错误 | 双写校验通过后才切流量 |
| ArgoCD 权限过大 | 误操作其他命名空间 | RBAC ServiceAccount 限定命名空间 |
| Helm Values 含密钥泄漏 | 凭证泄露 | 日志脱敏 + Secrets 不落磁盘 |

---

> 处理流水线参见 [design/DESIGN.md §4](../design/DESIGN.md#4-处理流水线--请求路径输入依赖展开计划生成执行)
> 接口定义与包结构参见 [arch/ARCH.md](../arch/ARCH.md)
> API 端点与部署详情参见 [specs/SPECS.md](../specs/SPECS.md)
