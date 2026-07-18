package domain

import (
	"context"
	"time"
)

// Deployer is the deployment-execution Port (ARCH §3.2). Implementations live in
// infrastructure/adapter/ and may coexist (Helm/Compose/ArgoCD, §10.4 SPI
// multi-implementation).
type Deployer interface {
	// Render turns a Plan into the target deployment configuration by profile.
	Render(ctx context.Context, plan AssemblyPlan, profile string) (RenderOutput, error)
	// Apply applies the rendered product to the target environment.
	Apply(ctx context.Context, out RenderOutput) ([]ApplyResult, error)
	// Rollback rolls a component back to a specific revision.
	Rollback(ctx context.Context, component, toRevision string) error
	// Status queries the current runtime status of a component.
	Status(ctx context.Context, component string) (ComponentStatus, error)
	// Mode reports the deployer mode (helm|compose|argocd).
	Mode() string
}

// CICDPort is the SPI of the CI/CD tool (interface_versions.CICD = 1.0.0,
// ARCH §3.2). ArgoCD/Istio etc. implement it.
type CICDPort interface {
	Sync(ctx context.Context, manifest []byte) error
	RollbackTo(ctx context.Context, revision string) error
}

// Store is the persistence Port (SPECS §8.1). Offline = in-memory; production =
// PostgreSQL provisioning_record + Redis revision cache.
type Store interface {
	Save(rec Record) error
	ByChecksum(checksum string) []Record
	// Revisions returns the distinct revisions of a component in chronological
	// order of first appearance (SPECS §8.2 revision history).
	Revisions(component string) []string
	LastRevision(component string) (string, bool)
	HasRevision(component, revision string) bool
}

// Locker is the distributed-execution-lock Port (Cache SPI 1.0.0, SKILLS §9.2 /
// §12 S7). Offline = in-memory; production = Redis SET NX.
type Locker interface {
	Acquire(key string, ttl time.Duration) bool
	Release(key string)
}
