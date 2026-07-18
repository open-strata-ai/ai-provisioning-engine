package adapter

import (
	"context"
	"sync"

	"github.com/open-strata-ai/ai-provisioning-engine/domain"
)

// HelmAdapter drives Kubernetes via Helm install/upgrade/rollback (ARCH §6.3).
// Used for standard/advanced profiles. Offline it renders Helm Values.
type HelmAdapter struct{ *core }

// NewHelmAdapter builds a Helm deployer.
func NewHelmAdapter(o Options) *HelmAdapter {
	return &HelmAdapter{core: &core{mode: domain.ModeHelm, kind: domain.KindHelmValues, opts: o.withDefaults()}}
}

// ComposeAdapter drives Docker Compose up/down (ARCH §6.3). Used for the starter
// profile; single-replica by design.
type ComposeAdapter struct{ *core }

// NewComposeAdapter builds a Compose deployer.
func NewComposeAdapter(o Options) *ComposeAdapter {
	o = o.withDefaults()
	o.Replicas = 1
	return &ComposeAdapter{core: &core{mode: domain.ModeCompose, kind: domain.KindCompose, opts: o}}
}

// ArgoCDAdapter drives GitOps sync/rollback through a CICDPort (ARCH §6.1). Used
// for the full profile.
type ArgoCDAdapter struct{ *core }

// NewArgoCDAdapter builds an ArgoCD deployer over the given CICD port.
func NewArgoCDAdapter(o Options, cicd domain.CICDPort) *ArgoCDAdapter {
	if cicd == nil {
		cicd = NewFakeCICD()
	}
	return &ArgoCDAdapter{core: &core{mode: domain.ModeArgoCD, kind: domain.KindK8sManifest, opts: o.withDefaults(), cicd: cicd}}
}

var (
	_ domain.Deployer = (*HelmAdapter)(nil)
	_ domain.Deployer = (*ComposeAdapter)(nil)
	_ domain.Deployer = (*ArgoCDAdapter)(nil)
)

// FakeCICD is an offline domain.CICDPort that records syncs/rollbacks in memory
// (production: real ArgoCD/Istio client, ARCH §6.1).
type FakeCICD struct {
	mu         sync.Mutex
	Synced     [][]byte
	RolledBack []string
}

// NewFakeCICD builds an empty fake CICD.
func NewFakeCICD() *FakeCICD { return &FakeCICD{} }

// Sync records a manifest.
func (f *FakeCICD) Sync(_ context.Context, manifest []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(manifest))
	copy(cp, manifest)
	f.Synced = append(f.Synced, cp)
	return nil
}

// RollbackTo records a rollback target.
func (f *FakeCICD) RollbackTo(_ context.Context, revision string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RolledBack = append(f.RolledBack, revision)
	return nil
}

var _ domain.CICDPort = (*FakeCICD)(nil)

// SelectDeployer picks the deployer by mode/profile (ARCH §6.2). ArgoCD/Istio are
// optional (full profile only), so starter/standard/advanced use direct-drive
// Helm/Compose and full uses ArgoCD GitOps.
func SelectDeployer(mode, profile string, o Options, cicd domain.CICDPort) domain.Deployer {
	switch {
	case mode == domain.ModeArgoCD || profile == domain.ProfileFull:
		return NewArgoCDAdapter(o, cicd)
	case mode == domain.ModeCompose || profile == domain.ProfileStarter:
		return NewComposeAdapter(o)
	default: // helm | standard | advanced
		return NewHelmAdapter(o)
	}
}
