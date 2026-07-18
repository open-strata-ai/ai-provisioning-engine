# ai-provisioning-engine · Architecture documentation

> Corresponding design documents §1 (positioning and boundaries), §2 (responsibility list), §3 (core abstraction and interface), §6 (external adapter)
> Platform version v1.4.0 | Domain: assembly (assembly domain · provisioning/deployment engine) | Required: core

---

## §1 Positioning

### 1.1 Positioning in one sentence

`ai-provisioning-engine` is the second half of the OpenStrata assembly backbone - the deployment/provisioning engine. It consumes the **AssemblyPlan** produced by `ai-dependency-resolver`, implements the plan into **Helm Values ​​/ Compose / Operator configuration**, and applies it to the running environment through **ArgoCD or direct connection to Helm** to achieve "incremental deployment, rolling update, zero downtime".

### 1.2 The only problem solved

Turn "a confirmed assembly plan" into "actual changes to the environment that are safe, observable, and rollable" - only the differences are moved, and running services are not restarted.

### 1.3 System location

```
ai-dependency-resolver
  │ AssemblyPlan (+ Checksum)
  ▼
ai-provisioning-engine  ← Main repository（Execute only、Not counted as dependence）
  │ Render + Apply
  ▼
K8s Cluster / Compose（ArgoCD/Helm/Docker）
```

### 1.4 Boundary declaration

- **IN**: `AssemblyPlan` (Added/Reused/Removed + Checksum, from resolver)
- **Out**: `ApplyResult[]` (deployment status of each component) + component Ready signal → Portal
- **Not responsible**: Dependency resolution and Plan generation (ai-dependency-resolver); component runtime management (each runtime service itself)
- **Core Principles**: Incremental deployment, rolling updates, zero downtime (§13.3 Three Principles)

### 1.5 Relationship with other Go components

| Components | Relationships | Description |
|------|------|------|
| ai-dependency-resolver | upstream producer | consume its AssemblyPlan |
| ai-cli | Upstream caller | `aictl up/apply/rollback` transparent call |
| ai-guide-portal | Upstream caller | The portal triggers execution orchestration, and this repository is the execution core |
| Deployed components | Assemblers | gateway/sandbox, etc. are their assembly targets and do not directly call the runtime |

### 1.6 Required

**core** (§13.3 Assembly Center). Stages one to three are driven by `aictl up` to drive Compose; the full file is driven by the boot portal + ArgoCD GitOps.

---

## §2 Responsibilities

| # | Responsibilities | Required | Description |
|---|------|------|------|
| R1 | Plan consumption and verification | core | Read AssemblyPlan, pre-check resources/dependencies/conflicts |
| R2 | Rendering Configuration | core | Plan → Helm Values ​​/ Compose / Operator (by profile) |
| R3 | Incremental deployment | core | Deploy/change differences only, keep existing components |
| R4 | rolling update | core | multi-copy rolling + probe keepalive |
| R5 | Canary upgrade | core | Those with greater impact will be cut after double-writing verification |
| R6 | rollback | core | declarative rollback (enabled=false replay) |
| R7 | Status Postback | core | Component Ready Status Postback Portal |

### Responsibility interaction matrix

```
  AssemblyPlan
       │
  ┌────▼─────┐
  │ R1 Preflight   │──fail→ block
  └────┬─────┘
       │ pass
  ┌────▼─────┐    ┌──────────┐
  │ R2 rendering   │◄───│ profile  │
  └────┬─────┘    └──────────┘
       │ Helm Values/Compose/Manifest
  ┌────┴─────────────────────┐
  │       R3 incremental deployment         │
  │  Added → deploy          │
  │  Removed → decommission  │
  │  Reused → skip           │
  └────┬─────────────────────┘
       │
  ┌────┴──────┬──────────┐
  ▼           ▼          ▼
R4 scroll    R5 canary    R6 rollback
  │           │          │
  └─────┬─────┴──────────┘
        ▼
  R7 status return → portal
```

---

## §3 Core interface

### 3.1 Domain layer type definition (`domain/`)

```go
package domain

import "context"

// ============================================================
//Input (consumer model)
// ============================================================

//AssemblyPlan Assembly plan from ai-dependency-resolver
type AssemblyPlan struct {
    Added    []PlannedComponent
    Reused   []PlannedComponent
    Removed  []PlannedComponent
    Checksum string
}

//PlannedComponent A single component in the plan
type PlannedComponent struct {
    RepoName  string   //Self-developed App name or OSS instance name
    Kind      string   // app | oss
    Version   string   //Crucified version
    Capability string
    DependsOn []string
}

// ============================================================
//output model
// ============================================================

//RenderOutput rendering product
type RenderOutput struct {
    Kind      string            // helm-values | compose | k8s-manifest
    Artifacts map[string][]byte //filename → YAML content
}

//ApplyResult The execution result of a single component
type ApplyResult struct {
    Component string //Component name
    Action    string // add | reuse | remove | rolling-update | gray-cutover
    Status    string // success | failed | in-progress
    Revision  string //Deploy revision (for rollback)
    Message   string //human readable description
}

//ComponentStatus component runtime status
type ComponentStatus struct {
    Name     string
    Ready    bool
    Version  string
    Replicas int
    Message  string
}
```

### 3.2 Domain Port (decoupling point)

```go
//Deployer is the field of deployment execution Port
//The implementation is located in infrastructure/adapter/ and supports the coexistence of multiple SPI implementations.
type Deployer interface {
    //Render renders the Plan into the target deployment configuration by profile
    Render(ctx context.Context, plan AssemblyPlan, profile string) (RenderOutput, error)

    //Apply applies the rendering product to the target environment
    Apply(ctx context.Context, out RenderOutput) ([]ApplyResult, error)

    //Rollback rolls the component back to the specified version
    Rollback(ctx context.Context, component string, toRevision string) error

    //Status Query the current running status of the component
    Status(ctx context.Context, component string) (ComponentStatus, error)
}

//CICDPort is the SPI port of the deployment tool (interface_versions.CICD = 1.0.0)
//ArgoCD/Istio etc. implement this interface
type CICDPort interface {
    Sync(ctx context.Context, manifest []byte) error
    RollbackTo(ctx context.Context, revision string) error
}
```

### 3.3 Package structure (DDD four layers)

```
cmd/provisioner/              #Entrance: Gin HTTP server + gRPC
├── domain/
│   ├── model.go              # AssemblyPlan, RenderOutput, ApplyResult
│   ├── port.go               # Deployer, CICDPort interfaces
│   ├── service.go            #provisionerService (orchestration implementation)
│   └── service_test.go
├── application/
│   └── usecase/
│       ├── apply.go          #Edit: Preflight → Render → Apply → Postback
│       └── rollback.go       #Rollback orchestration
├── infrastructure/
│   ├── adapter/
│   │   ├── helm_adapter.go   # HelmAdapter：Helm install/upgrade/rollback
│   │   ├── compose_adapter.go# ComposeAdapter：docker-compose up/down
│   │   └── argocd_adapter.go # ArgoCDAdapter：GitOps sync/rollback
│   ├── config/
│   │   └── config.yaml       # provisioner.mode, rollout, grayCutover
│   └── persistence/
│       ├── pg_record.go      #provisioning_record table operations
│       └── redis_lock.go     #Distributed execution lock
└── api/
    └── handler/
        ├── apply.go          # POST /v1/apply
        ├── rollback.go       # POST /v1/rollback
        ├── status.go         # GET  /v1/status/{component}
        └── result.go         # GET  /v1/plan/{checksum}/apply-result
```

### 3.4 Interface Contract: Request/Response

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
    ToRevision string `json:"to_revision"` //Empty = rollback to previous version
}
```

---

## §6 Adapter

### 6.1 SPI Adapter Matrix

| SPI Ports | Roles | External Components | Default/Alternate | Adapter |
|----------|------|----------|-----------|---------|
| CICD (1.0.0) | Consumer | ArgoCD (optional) + Istio (optional, full) | Alternative/Alternative | `ArgoCDAdapter` |
| Cache (1.0.0) | Consumer | Redis (core) | ✅ Unique | Distributed lock + state cache |
| Tracing (1.0.0) | Consumer | OTel (core) | ✅ Unique | Deployment link trace |
| Deployment Targets | Direct Driver | Kubernetes / Docker Compose | ✅ Unique | HelmAdapter / ComposeAdapter |

### 6.2 CICD default off logic

ArgoCD/Istio is **optional** (only full file), so this repository uses **direct connection to Helm/Compose** for starter/standard/advanced, and uses ArgoCD for full file.

```go
//Renderer selection logic (infrastructure/adapter/ factory)
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

### 6.3 Anti-corrosion layer design

Each Adapter implements the `Deployer` interface, switching deployment targets with zero business changes:

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

//Implement the same Deployer interface as above
```

### 6.4 Rendering process

```
Plan (Added + Removed)
  │
  ▼
according to profile Select renderer → Read meta repository dependencies/config/ + Main repository infrastructure/config/
  │
  ▼
merge Values（priority）：User coverage > profile > bom default
  │
  ▼
RenderOutput（Helm Values YAML / compose.yaml / K8s manifest）
```

### 6.5 Adapter Testing Strategy

| Test Type | Coverage | Environment |
|----------|------|------|
| Single test | Render field correctness, rolling strategy logic | Go test |
| Contract testing | Helm/Compose/ArgoCD three adapters have the same contract | SPI multi-implementation consistency |
| Integration testing | kind cluster + mock ArgoCD | Incremental subset without restart, rolling probe |
| Chaos testing | Kill Pod mid-deployment | Retry/timeout → rollback |

---

> For detailed processing pipeline, please refer to [design/DESIGN.md §4](../design/DESIGN.md#4-Processing Pipeline--Request Path Input Dependency Expansion Plan Generation and Execution)
> See [skills/SKILLS.md](../skills/SKILLS.md) for concurrency/security rules
> For API endpoints and deployment details, see [specs/SPECS.md](../specs/SPECS.md)
