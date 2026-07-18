// Package rollback orchestrates declarative rollback (DESIGN §5.5, R6). A rollback
// is only allowed to a revision that exists in the audit trail (SKILLS §12 S5).
package rollback

import (
	"context"
	"time"

	"github.com/open-strata-ai/ai-provisioning-engine/application/apply"
	"github.com/open-strata-ai/ai-provisioning-engine/domain"
)

// UseCase implements component rollback.
type UseCase struct {
	selectDeployer apply.SelectDeployer
	store          domain.Store
	now            func() time.Time
}

// New builds a rollback use case.
func New(sel apply.SelectDeployer, store domain.Store) *UseCase {
	return &UseCase{selectDeployer: sel, store: store, now: time.Now}
}

// Rollback rolls a component back. An empty toRevision means "the previous
// revision" (the one before the current). Returns the revision applied.
func (u *UseCase) Rollback(ctx context.Context, component, toRevision string) (string, error) {
	revs := u.store.Revisions(component)
	if len(revs) == 0 {
		return "", domain.NewError(domain.ErrNotFound, "no revisions recorded for component "+component)
	}
	target := toRevision
	if target == "" {
		if len(revs) >= 2 {
			target = revs[len(revs)-2]
		} else {
			target = revs[len(revs)-1]
		}
	}
	if !u.store.HasRevision(component, target) {
		return "", domain.NewError(domain.ErrRevisionNotFound, "revision not found: "+target)
	}
	dep := u.selectDeployer(domain.ProfileStandard)
	if err := dep.Rollback(ctx, component, target); err != nil {
		return "", err
	}
	_ = u.store.Save(domain.Record{
		Component: component,
		Action:    domain.ActionRollback,
		Revision:  target,
		Status:    domain.StatusSuccess,
		CreatedAt: u.now(),
	})
	return target, nil
}
