// Package adapter implements domain.Deployer for Helm / Compose / ArgoCD targets
// (ARCH §6, DDD infrastructure/adapter anti-corrosion layer). All adapters share
// a deterministic, dependency-free core so the SPI contract test (§6.5) can run
// the same "render/apply/rollback/status" flow against every implementation.
package adapter

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/open-strata-ai/ai-provisioning-engine/domain"
)

// Options configures a deployer core.
type Options struct {
	Replicas       int
	MaxSurge       int
	MaxUnavailable int
	MaxParallel    int
	LockTTL        time.Duration
	Locker         domain.Locker
}

func (o Options) withDefaults() Options {
	if o.Replicas < 1 {
		o.Replicas = 1
	}
	if o.MaxSurge < 1 {
		o.MaxSurge = 1
	}
	if o.MaxParallel < 1 {
		o.MaxParallel = 8
	}
	if o.LockTTL <= 0 {
		o.LockTTL = 60 * time.Second
	}
	return o
}

// core is the shared deployer implementation. Concrete adapters embed it and set
// mode/kind/cicd.
type core struct {
	mode string
	kind string
	opts Options
	cicd domain.CICDPort // nil for helm/compose direct-drive
}

// Mode reports the deployer mode.
func (c *core) Mode() string { return c.mode }

// Render turns a Plan into deployment configuration by profile (SKILLS §5.1).
func (c *core) Render(_ context.Context, plan domain.AssemblyPlan, profile string) (domain.RenderOutput, error) {
	if profile == "" {
		profile = domain.ProfileStandard
	}
	artifacts := make(map[string][]byte)
	for _, comp := range plan.Added {
		artifacts[comp.RepoName+".yaml"] = c.renderComponent(comp, profile, true)
	}
	for _, comp := range plan.Removed {
		artifacts[comp.RepoName+".yaml"] = c.renderComponent(comp, profile, false)
	}
	return domain.RenderOutput{
		Kind:      c.kind,
		Artifacts: artifacts,
		Plan:      plan,
		Profile:   profile,
	}, nil
}

func (c *core) renderComponent(comp domain.PlannedComponent, profile string, enabled bool) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# rendered by ai-provisioning-engine (kind=%s, profile=%s)\n", c.kind, profile)
	fmt.Fprintf(&b, "component: %s\n", comp.RepoName)
	fmt.Fprintf(&b, "kind: %s\n", nonEmpty(comp.Kind, domain.KindApp))
	fmt.Fprintf(&b, "version: %s\n", nonEmpty(comp.Version, "latest"))
	fmt.Fprintf(&b, "enabled: %t\n", enabled)
	fmt.Fprintf(&b, "replicas: %d\n", c.opts.Replicas)
	fmt.Fprintf(&b, "rollout:\n  maxSurge: %d\n  maxUnavailable: %d\n", c.opts.MaxSurge, c.opts.MaxUnavailable)
	if comp.Capability != "" {
		fmt.Fprintf(&b, "capability: %s\n", comp.Capability)
	}
	return []byte(b.String())
}

// Apply applies the rendered product (SKILLS §5.2 diff apply + §9.5 topology).
// Added components deploy layer-by-layer (topological order); within a layer they
// run in parallel bounded by a semaphore and serialized per-component by a lock.
func (c *core) Apply(ctx context.Context, out domain.RenderOutput) ([]domain.ApplyResult, error) {
	plan := out.Plan
	layers, err := domain.TopologicalLayers(plan.Added)
	if err != nil {
		return nil, err
	}

	var (
		mu      sync.Mutex
		results []domain.ApplyResult
	)
	for _, layer := range layers {
		sem := make(chan struct{}, c.opts.MaxParallel)
		var wg sync.WaitGroup
		for _, comp := range layer {
			wg.Add(1)
			go func(comp domain.PlannedComponent) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				res := c.applyComponent(ctx, plan.Checksum, comp)
				mu.Lock()
				results = append(results, res)
				mu.Unlock()
			}(comp)
		}
		wg.Wait()
	}

	for _, comp := range plan.Removed {
		results = append(results, domain.ApplyResult{
			Component: comp.RepoName,
			Action:    domain.ActionRemove,
			Status:    domain.StatusSuccess,
			Message:   "decommissioned (enabled=false)",
		})
	}
	for _, comp := range plan.Reused {
		results = append(results, domain.ApplyResult{
			Component: comp.RepoName,
			Action:    domain.ActionReuse,
			Status:    domain.StatusSuccess,
			Message:   "unchanged; not restarted",
		})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Component < results[j].Component })
	return results, nil
}

func (c *core) applyComponent(ctx context.Context, checksum string, comp domain.PlannedComponent) domain.ApplyResult {
	if c.opts.Locker != nil {
		lockKey := "provisioner:lock:" + comp.RepoName
		if !c.opts.Locker.Acquire(lockKey, c.opts.LockTTL) {
			return domain.ApplyResult{
				Component: comp.RepoName,
				Action:    domain.ActionAdd,
				Status:    domain.StatusInProgress,
				Message:   "another apply holds the component lock",
			}
		}
		defer c.opts.Locker.Release(lockKey)
	}

	rev := revisionOf(checksum, comp)
	if c.cicd != nil {
		manifest := c.renderComponent(comp, "", true)
		if err := c.cicd.Sync(ctx, manifest); err != nil {
			return domain.ApplyResult{
				Component: comp.RepoName,
				Action:    domain.ActionAdd,
				Status:    domain.StatusFailed,
				Revision:  rev,
				Message:   err.Error(),
			}
		}
	}
	return domain.ApplyResult{
		Component: comp.RepoName,
		Action:    domain.ActionAdd,
		Status:    domain.StatusSuccess,
		Revision:  rev,
		Message:   "deployed via " + c.mode,
	}
}

// Rollback rolls a component back to a revision (SKILLS §5.5). For GitOps modes
// it delegates to the CICD port; direct-drive Helm/Compose replay is offline.
func (c *core) Rollback(ctx context.Context, component, toRevision string) error {
	if toRevision == "" {
		return domain.NewError(domain.ErrRevisionNotFound, "target revision required")
	}
	if c.cicd != nil {
		return c.cicd.RollbackTo(ctx, toRevision)
	}
	return nil
}

// Status returns the component runtime status. Offline it reports Ready with the
// configured replica count; production queries K8s/ArgoCD/Compose.
func (c *core) Status(_ context.Context, component string) (domain.ComponentStatus, error) {
	return domain.ComponentStatus{
		Name:     component,
		Ready:    true,
		Replicas: c.opts.Replicas,
		Message:  "offline status stub (mode=" + c.mode + ")",
	}, nil
}

func revisionOf(checksum string, comp domain.PlannedComponent) string {
	c := checksum
	if len(c) > 8 {
		c = c[:8]
	}
	if c == "" {
		c = "rev"
	}
	return c + "-" + comp.RepoName + "-" + nonEmpty(comp.Version, "0")
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
