// Package apply orchestrates the deployment pipeline: Preflight → Render → Apply
// → audit → summary (DESIGN §4, R1–R4/R7). It is framework-free and depends only
// on domain ports.
package apply

import (
	"context"
	"time"

	"github.com/open-strata-ai/ai-provisioning-engine/domain"
)

// SelectDeployer returns a domain.Deployer for a profile (ARCH §6.2 factory).
type SelectDeployer func(profile string) domain.Deployer

// Response is the ApplyResponse (SPECS §7.2).
type Response struct {
	Results []domain.ApplyResult `json:"results"`
	PlanRef string               `json:"plan_checksum"`
	Summary string               `json:"summary"`
}

// UseCase implements the apply/status pipeline.
type UseCase struct {
	selectDeployer SelectDeployer
	store          domain.Store
	now            func() time.Time
}

// New builds an apply use case.
func New(sel SelectDeployer, store domain.Store) *UseCase {
	return &UseCase{selectDeployer: sel, store: store, now: time.Now}
}

// Apply validates, renders and applies a plan, recording an audit trail (S2).
func (u *UseCase) Apply(ctx context.Context, plan domain.AssemblyPlan, profile, tenantID string) (Response, error) {
	if err := domain.Preflight(plan); err != nil {
		return Response{}, err
	}
	dep := u.selectDeployer(profile)
	out, err := dep.Render(ctx, plan, profile)
	if err != nil {
		return Response{}, err
	}
	results, err := dep.Apply(ctx, out)
	if err != nil {
		return Response{}, err
	}
	for _, r := range results {
		rec := domain.Record{
			PlanChecksum: plan.Checksum,
			TenantID:     tenantID,
			Component:    r.Component,
			Action:       r.Action,
			Revision:     r.Revision,
			Status:       r.Status,
			CreatedAt:    u.now(),
		}
		if r.Status == domain.StatusFailed {
			rec.ErrorDetail = r.Message
		}
		_ = u.store.Save(rec)
	}
	return Response{Results: results, PlanRef: plan.Checksum, Summary: domain.Summary(plan)}, nil
}

// Result returns the recorded apply results for a plan checksum (SPECS §7.1).
func (u *UseCase) Result(checksum string) []domain.ApplyResult {
	recs := u.store.ByChecksum(checksum)
	out := make([]domain.ApplyResult, 0, len(recs))
	for _, r := range recs {
		out = append(out, domain.ApplyResult{
			Component: r.Component,
			Action:    r.Action,
			Status:    r.Status,
			Revision:  r.Revision,
			Message:   r.ErrorDetail,
		})
	}
	return out
}

// Status queries a component's runtime status (SPECS §7.1).
func (u *UseCase) Status(ctx context.Context, component string) (domain.ComponentStatus, error) {
	return u.selectDeployer(domain.ProfileStandard).Status(ctx, component)
}
