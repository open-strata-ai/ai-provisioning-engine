# ai-provisioning-engine · Algorithm/Concurrency/Safety Rules

> Corresponding design documents §5 (key algorithms), §9 (concurrency and performance), §12 (observability/security)

---

## §5 Key algorithm

### 5.1 Configure rendering (Plan → Deployment configuration)

**Input**: AssemblyPlan + profile + meta repository local config
**Output**: Helm Values/Compose YAML/K8s Manifest

```
function render(plan, profile):
    renderer = selectRenderer(profile)
    // starter → Compose; standard+/advanced → Helm; full → ArgoCD

    values = {}
    for component in (plan.Added ∪ plan.Changed):
        base    = loadFromBom(component.version)     //bom.yaml pinned version
        local   = loadFromProfile(profile, component) //profile external section
        appConf = loadFromAppConfig(component)       //infrastructure/config/local
        merged  = merge(local, appConf, base)        //Priority: app > profile > bom
        values[component.name] = merged

    return RenderOutput(kind=renderer.kind, artifacts=renderer.marshal(values))
```

### 5.2 Incremental application

```
function diffApply(plan, currentState):
    added   = plan.Added   - currentState   //New deployment
    removed = plan.Removed ∩ currentState   //Confirm offline
    changed = plan.Added   ∩ currentState but version/config diff  //rolling update
    reused  = plan.Reused  ∩ currentState   //jump over

    for a in added:     deploy(a)
    for r in removed:   decommission(r)
    for c in changed:   rollingUpdate(c)
    for ru in reused:   skip(ru)           //Do not restart running services
```

**Principle**: Only the differences are moved, running services are not restarted (unless the configuration changes, they are rolled).

### 5.3 Rolling update

```
function rollingUpdate(component):
    desired = component.replicas
    for each replica:
        startNew(replica)
        waitForReady(replica)     //Probe check
        terminateOld(replica)
    //Parameters: maxSurge=1, maxUnavailable=0
    //Only replace 1 at a time, always keep them all available
```

### 5.4 Canary upgrade (double-write verification)

Switching with large impact (such as vector library Milvus↔Qdrant):

```
function grayCutover(oldComponent, newComponent):
    //Stage 1: Double Write
    parallel:
        write(oldComponent, data)
        write(newComponent, data)

    //Phase 2: Verify consistency
    for each record:
        assert read(oldComponent, key) == read(newComponent, key)

    //Stage 3: Cut over traffic
    if verifyPass:
        switchTraffic(newComponent)

    //Phase 4: Retirement of old components
    markForRemoval(oldComponent)
```

**Constraints**: Non-zero downtime, operation and maintenance window required; only full file enabled.

### 5.5 Rollback

```
function rollback(component, fromRevision):
    //Declarative: enabled=false/oldVersion in Replay Plan
    revision = getLastRevision(component)
    manifest = renderFromRevision(revision)
    cicd.RollbackTo(manifest, revision)
```

---

## §9 Concurrency and performance

### 9.1 Execution model

Gin management API; execution orchestration hot path can be on Hertz/go-zero (low latency).

### 9.2 Parallel Strategy

- **Parallel deployment of components**: Components without dependencies in `Plan.Added` can be started in parallel using goroutine + WaitGroup
- **Topological sorting**: If there are shared dependencies (such as PG first and then the services that depend on it), they are serialized in topological order.
- **Redis distributed lock**: Concurrent Apply of the same component is serialized with `SET NX`, lock TTL 60s
- **Global Semaphore**: Limit the number of concurrent Applys to avoid overwhelming the K8s API Server

### 9.3 Progress return

```go
type ProgressChan chan ComponentProgress

type ComponentProgress struct {
    Component  string
    Phase      string // rendering | deploying | probing | done
    Progress   int    // 0-100
    Error      error
}

//The portal real-time dashboard consumes this channel (§13.1 Status Dashboard)
```

### 9.4 Resource consumption

| Scenario | CPU | Memory | Description |
|------|-----|------|------|
| Idle | ~10m | ~32Mi | Gin server idle |
| Single component Apply | ~50m | ~128Mi | Helm install/upgrade |
| Multi-component parallelism Apply | ~500m | ~512Mi | Semaphore limit concurrency |
| canary dual writing | ~200m | ~256Mi | Read and write verification |

### 9.5 Topological sorting and orchestration (parallel deployment)

```go
//Orchestrate component deployment in topological order and execute in parallel without dependencies
func deployByTopology(plan AssemblyPlan, deployer Deployer) error {
    graph := buildDependencyGraph(plan.Added)
    layers := graph.TopologicalLayers() //No dependencies within each layer

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
        //Inter-layer serialization: wait until all current layers are Ready before deploying the next layer
    }
    return nil
}
```

### 9.6 Performance Rules

| # | Title | Trigger Condition | Constraints | Example |
|---|------|----------|------|------|
| P1 | goroutine concurrent deployment | Plan Added > 1 | Parallel components without dependencies, serial topology with dependencies | `go deploy(a); go deploy(b); wg.Wait()` |
| P2 | Distributed lock serial | Same component concurrency Apply | Redis SET NX, TTL 60s | `if !lock.Acquire("apply:milvus")` → retry |
| P3 | Semaphore limit concurrency | Global Apply number | `weighted.New(8)` | `sem.Acquire(ctx, 1)` |
| P4 | Context propagation | Each Apply | Timeout 300s, supports Ctrl-C cancellation | `ctx, cancel := context.WithTimeout(...)` |
| P5 | Probe wait timeout | Post-deployment probe Ready | 30s timeout + 3 retries per component | `waitForReady(component, 30s)` |
| P6 | Progress channel non-blocking | Status return | buffered(100), discard old ones when full | `select { case ch <- p: default: }` |

---

## §12 Security

### 12.1 Security Boundary

The execution is a change of the platform itself, and all operations must be audited. Deployment operations involve changes to the actual production environment and require extremely high security requirements.

### 12.2 Security rules

| # | Title | Trigger Condition | Constraints | Example |
|---|------|----------|------|------|
| S1 | Preflight blocking | Before Apply | Plan conflict/quota discrepancy → direct blocking | `if conflicts > 0: abort()` |
| S2 | Full audit | Each Apply/Rollback | Even if security is not turned on, the platform itself changes leaving traces | `INSERT INTO provisioning_record(...)` |
| S3 | RBAC isolation | ArgoCD operations | ai-system + tenant namespace only | ServiceAccount list namespace only |
| S4 | Plan Checksum verification | Receive Plan | Checksum must be consistent with Resolver output | `if plan.checksum != expected: reject` |
| S5 | Rollback restrictions | Rollback is only allowed to historical revision | The record must exist in the revision table | `if !hasRevision(comp, rev): reject` |
| S6 | Deployment target reachability verification | Each Apply | K8s API Server/Compose connectivity pre-check | `if !ping(target): err("unreachable")` |
| S7 | Concurrency lock to prevent misoperation | Same component concurrency | Distributed lock + idempotent token | `lock("provisioner:" + component)` |
| S8 | Sensitive value desensitization | Log/output | Secrets in Helm Values ​​are not printed | `maskSensitive(values)` before log |

### 12.3 Audit requirements

Each Apply/Rollback record:
- plan_checksum、component、action（add/remove/rolling-update/gray-cutover）
- revision (deployment version number), status, error message
- tenant, operator, timestamp

Prometheus metrics:
- `apply_total`、`apply_duration_seconds`、`apply_errors_total`
- `rollback_total`、`component_ready_duration_seconds`

### 12.4 Risk Scenarios and Mitigation

| Risk | Impact | Mitigation |
|------|------|------|
| Deployment failed midway, resulting in inconsistency | Some components are new versions, some are old versions | Topology serial dependency + failure rollback |
| Concurrent Apply same component | Configuration conflict | Redis distributed lock + idempotent token |
| canary switching data is inconsistent | Vector read and write errors | Traffic is cut only after double-write verification passes |
| ArgoCD has excessive permissions | Misoperation of other namespaces | RBAC ServiceAccount restricted namespace |
| Helm Values ​​including key leakage | Credential leakage | Log desensitization + Secrets will not be lost on disk |

---

> For the processing pipeline, see [docs/DESIGN.md §4](./DESIGN.md#4-Processing Pipeline--Request path input dependency expansion plan generation and execution)
> For interface definition and package structure, see [docs/ARCH.md](./ARCH.md)
> For API endpoints and deployment details, see [docs/SPECS.md](./SPECS.md)
