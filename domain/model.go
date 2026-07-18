// Package domain holds source-independent value types and Port interfaces for
// ai-provisioning-engine. It has no external dependencies (DDD domain layer,
// DESIGN §3 / ARCH §3.1).
package domain

import "time"

// Component kinds (ARCH §3.1).
const (
	KindApp = "app"
	KindOSS = "oss"
)

// Apply actions (ARCH §3.1 / SPECS §7.2).
const (
	ActionAdd           = "add"
	ActionReuse         = "reuse"
	ActionRemove        = "remove"
	ActionRollingUpdate = "rolling-update"
	ActionGrayCutover   = "gray-cutover"
	ActionRollback      = "rollback"
)

// Apply statuses (ARCH §3.1).
const (
	StatusSuccess    = "success"
	StatusFailed     = "failed"
	StatusInProgress = "in-progress"
)

// Four-level profiles (§12.2 prefabrication).
const (
	ProfileStarter  = "starter"
	ProfileStandard = "standard"
	ProfileAdvanced = "advanced"
	ProfileFull     = "full"
)

// Deployer modes (§6.2).
const (
	ModeHelm    = "helm"
	ModeCompose = "compose"
	ModeArgoCD  = "argocd"
)

// RenderOutput kinds (ARCH §3.1).
const (
	KindHelmValues  = "helm-values"
	KindCompose     = "compose"
	KindK8sManifest = "k8s-manifest"
)

// PlannedComponent is a single component in an AssemblyPlan (ARCH §3.1).
type PlannedComponent struct {
	RepoName   string   `json:"repo_name"`
	Kind       string   `json:"kind"` // app | oss
	Version    string   `json:"version"`
	Capability string   `json:"capability,omitempty"`
	DependsOn  []string `json:"depends_on,omitempty"`
}

// AssemblyPlan is the input consumed from ai-dependency-resolver (ARCH §3.1).
type AssemblyPlan struct {
	Added    []PlannedComponent `json:"added"`
	Reused   []PlannedComponent `json:"reused"`
	Removed  []PlannedComponent `json:"removed"`
	Checksum string             `json:"checksum"`
}

// RenderOutput is the rendered deployment product (ARCH §3.1).
//
// Plan/Profile are carried alongside the documented Kind/Artifacts so that a
// Deployer.Apply is a pure function of its input — this keeps adapters stateless
// and makes the SPI contract test (§6.5) reproducible.
type RenderOutput struct {
	Kind      string            `json:"kind"` // helm-values | compose | k8s-manifest
	Artifacts map[string][]byte `json:"artifacts"`
	Plan      AssemblyPlan      `json:"-"`
	Profile   string            `json:"profile,omitempty"`
}

// ApplyResult is the execution result of a single component (ARCH §3.1).
type ApplyResult struct {
	Component string `json:"component"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	Revision  string `json:"revision,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ComponentStatus is a component's runtime status (ARCH §3.1).
type ComponentStatus struct {
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
	Version  string `json:"version"`
	Replicas int    `json:"replicas"`
	Message  string `json:"message,omitempty"`
}

// Record mirrors the provisioning_record table (SPECS §8.1). It is the audit
// trail written on every Apply/Rollback (SKILLS §12 S2).
type Record struct {
	PlanChecksum string    `json:"plan_checksum"`
	TenantID     string    `json:"tenant_id"`
	Component    string    `json:"component"`
	Action       string    `json:"action"`
	Revision     string    `json:"revision"`
	Status       string    `json:"status"`
	ErrorDetail  string    `json:"error_detail,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
