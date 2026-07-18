# ai-provisioning-engine · Interface/data/deployment specifications

> Corresponding design documents §7 (API/CLI), §8 (data model and storage), §11 (configuration and deployment)

---

## §7 API / CLI

### 7.1 HTTP API（Gin）

| Method | Path | Description | Request body/parameters | Response |
|------|------|------|-------------|------|
| POST | `/v1/apply` | Submit AssemblyPlan and perform deployment | `ApplyRequest` (JSON) | `ApplyResponse` |
| POST | `/v1/rollback` | Rollback the component to the specified revision | `RollbackRequest` (JSON) | `RollbackResponse` |
| GET | `/v1/status/{component}` | Query component running status | path: component name | `ComponentStatus` |
| GET | `/v1/plan/{checksum}/apply-result` | Query Plan execution results | path: checksum | `ApplyResult[]` |
| GET | `/healthz` | Liveness/readiness probe | — | `{"status":"ok"}` |
| GET | `/metrics` | Prometheus metrics | — | text/plain |

### 7.2 Request/Response Schema

**ApplyRequest**:

| Field | Type | Required | Description |
|------|------|------|------|
| plan | AssemblyPlan | Yes | Assembly plan from resolver |
| profile | string | yes | starter/standard/advanced/full |
| tenant_id | string | yes | tenant ID |

**ApplyResponse**:

| Field | Type | Description |
|------|------|------|
| results | []ApplyResult | The execution results of each component |
| plan_checksum | string | Corresponds to Plan |
| summary | string | Summary (N added, M reused, K removed) |

**ApplyResult**:

| Field | Type | Description |
|------|------|------|
| component | string | component name |
| action | string | add / reuse / remove / rolling-update / gray-cutover |
| status | string | success / failed / in-progress |
| revision | string | Deployment revision (for rollback) |
| message | string | error reason or success description |

**RollbackRequest**:

| Field | Type | Required | Description |
|------|------|------|------|
| component | string | yes | component name |
| to_revision | string | No | Target revision, empty = rollback to previous revision |

### 7.3 CLI commands (passthrough via `ai-cli`)

| Command | Description | Parameters |
|------|------|------|
| `aictl up --profile starter` | render Compose + pull up core components | `--profile <p>` `--detach` |
| `aictl apply --plan <checksum>` | Apply a Plan | `--plan <checksum>` |
| `aictl rollback --component <name>` | Rollback component | `--component <name>` |

---

## §8 Data Model

### 8.1 Persistence table

**provisioning_record**（PostgreSQL，core）：

| Column | Type | Description |
|----|------|------|
| id | BIGSERIAL PRIMARY KEY | Auto-increment primary key |
| plan_checksum | TEXT NOT NULL | Corresponds to AssemblyPlan |
| tenant_id | TEXT NOT NULL | Tenant ID |
| component | TEXT NOT NULL | component name |
| action | TEXT NOT NULL | add/reuse/remove/rolling-update/gray-cutover |
| revision | TEXT | deployment revision (Helm release rev or ArgoCD commit) |
| status | TEXT NOT NULL | success/failed/in-progress |
| error_detail | TEXT | Reason for failure |
| created_at | TIMESTAMPTZ DEFAULT now() | timestamp |

**index**:

```sql
CREATE INDEX idx_provisioning_record_checksum ON provisioning_record(plan_checksum);
CREATE INDEX idx_provisioning_record_component ON provisioning_record(component, created_at DESC);
CREATE INDEX idx_provisioning_record_tenant ON provisioning_record(tenant_id, created_at DESC);
```

### 8.2 Redis key

| Key Pattern | Data Type | TTL | Description |
|--------|----------|-----|------|
| `provisioner:lock:{component}` | string (SET NX) | 60s | Distributed execution lock |
| `provisioner:status:{component}` | JSON | 5min | Component running status cache |
| `provisioner:apply:{checksum}` | JSON | 1h | Plan execution result cache |
| `provisioner:revision:{component}` | list | None | Component revision history (up to 10 entries) |

### 8.3 Configure merge sources

| Source | Path | Priority | Description |
|------|------|--------|------|
| App local configuration | `infrastructure/config/<component>.yaml` | 1 (highest) | Comes with component repository |
| Profile overlays | `openstrata-meta/profiles/<p>/overlays/` | 2 | Profile overlays |
| BOM default | `openstrata-meta/bom.yaml` | 3 (minimum) | Global default |

### 8.4 Tracking components to be offline

```sql
CREATE TABLE decommission_log (
  id          BIGSERIAL PRIMARY KEY,
  component   TEXT NOT NULL,
  reason      TEXT,
  deactivated_at TIMESTAMPTZ,
  verified    BOOLEAN DEFAULT false,  -- After confirming that there is no traffic true
  verified_at TIMESTAMPTZ
);
```

---

## §11 Deployment

### 11.1 Deployment form

| Properties | Values ​​|
|------|-----|
| namespace | `ai-system` (§9.2) |
| Number of copies | 2 (multiple copies) |
| CICD | starter/standard/advanced → direct connection to Helm; full → ArgoCD GitOps bootstrapping |

### 11.2 K8s resource configuration

| Resource Type | Requests | Limits |
|----------|----------|--------|
| CPU | 200m | 1000m |
| Memory | 256Mi | 1024Mi |

**Note**: Rendering Helm Values ​​and calling K8s API require a certain amount of memory, and limits allow for a reserve.

### 11.3 Probe

| probe | path | initial delay | period | failure threshold |
|------|------|----------|------|----------|
| liveness | `GET /healthz` | 5s | 10s | 3 |
| readiness readiness | `GET /healthz` | 5s | 10s | 3 |

Readiness probe additional check: K8s API Server reachability (`kubectl version` or client-go ping).

### 11.4 rolling update

```yaml
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0
minReadySeconds: 10
```

### 11.5 Configuration key list

| Configuration Key | Default Value | Description |
|--------|--------|------|
| `provisioner.mode` | `helm` | Deployment mode: helm/compose/argocd |
| `provisioner.argocd.enabled` | `false` | full profile true |
| `provisioner.argocd.namespace` | `ai-system` | ArgoCD management namespace |
| `provisioner.rollout.maxSurge` | `1` | Maximum surge number for rolling updates |
| `provisioner.rollout.maxUnavailable` | `0` | Maximum unavailable number for rolling update |
| `provisioner.rollout.probeGraceSeconds` | `30` | Probe wait grace period |
| `provisioner.grayCutover.doubleWriteVerify` | `true` | canary switching double write verification |
| `provisioner.metaRepo.profilesPath` | `openstrata-meta/profiles` | Profile directory |
| `provisioner.metaRepo.configPath` | `openstrata-meta/dependencies/config` | Portfolio-level configuration |
| `provisioner.maxParallelDeploy` | `8` | Upper limit of parallel deployment (semaphore) |
| `provisioner.applyTimeout` | `300` | Apply timeout seconds |
| `provisioner.readyTimeout` | `30` | The number of seconds to wait for the component to be ready |

### 11.6 Deployment dependencies

| Dependency | Type | Required | Description |
|------|------|------|------|
| K8s API Server / Docker | Runtime | Yes | Deployment Target |
| Helm/kubectl/docker CLI | Tools | Yes | As required by mode |
| ArgoCD | CICD | full | full file GitOps |
| PostgreSQL | Database | No | Audit Record Persistence |
| Redis | Cache | No | Distributed lock |
| OTel Collector | Observable | No | Trace reporting |

### 11.7 Environment variables

| Variable | Description | Example |
|------|------|------|
| `CONFIG_PATH` | Configuration file path | `/etc/provisioner/config.yaml` |
| `KUBECONFIG` | K8s configuration | `/etc/provisioner/kubeconfig` |
| `PROVISIONER_MODE` | Deployment mode | `helm` |
| `PG_DSN` | PostgreSQL connection string | `postgres://...` |
| `REDIS_ADDR` | Redis address | `redis:6379` |
| `ARGOCD_SERVER` | ArgoCD service address | `argocd-server.ai-system:443` |
| `OTEL_ENDPOINT` | OTel export endpoint | `otel-collector:4317` |

---

> For the core interface and package structure, see [arch/ARCH.md](../arch/ARCH.md)
> For algorithm/concurrency/safety rules, see [skills/SKILLS.md](../skills/SKILLS.md)
> For the complete processing pipeline, see [design/DESIGN.md §4](../design/DESIGN.md#4-Processing Pipeline--Request path input dependency expansion plan generation and execution)
