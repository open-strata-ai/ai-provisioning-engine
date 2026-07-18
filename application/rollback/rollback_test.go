package rollback

import (
	"context"
	"testing"

	"github.com/open-strata-ai/ai-provisioning-engine/application/apply"
	"github.com/open-strata-ai/ai-provisioning-engine/domain"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/adapter"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/persistence"
)

func newRollback() (*UseCase, domain.Store) {
	store := persistence.NewStore()
	opts := adapter.Options{Replicas: 2, Locker: persistence.NewLocker()}
	sel := apply.SelectDeployer(func(profile string) domain.Deployer {
		return adapter.SelectDeployer(domain.ModeArgoCD, profile, opts, adapter.NewFakeCICD())
	})
	return New(sel, store), store
}

func TestRollbackToPrevious(t *testing.T) {
	uc, store := newRollback()
	_ = store.Save(domain.Record{Component: "svc", Revision: "r1", Status: domain.StatusSuccess})
	_ = store.Save(domain.Record{Component: "svc", Revision: "r2", Status: domain.StatusSuccess})

	rev, err := uc.Rollback(context.Background(), "svc", "")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rev != "r1" {
		t.Fatalf("expected previous revision r1, got %q", rev)
	}
}

func TestRollbackExplicitRevision(t *testing.T) {
	uc, store := newRollback()
	_ = store.Save(domain.Record{Component: "svc", Revision: "r1", Status: domain.StatusSuccess})
	_ = store.Save(domain.Record{Component: "svc", Revision: "r2", Status: domain.StatusSuccess})

	rev, err := uc.Rollback(context.Background(), "svc", "r1")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rev != "r1" {
		t.Fatalf("expected r1, got %q", rev)
	}
}

func TestRollbackUnknownRevision(t *testing.T) {
	uc, store := newRollback()
	_ = store.Save(domain.Record{Component: "svc", Revision: "r1", Status: domain.StatusSuccess})
	_, err := uc.Rollback(context.Background(), "svc", "does-not-exist")
	pe, ok := err.(*domain.ProvisionError)
	if !ok || pe.Code != domain.ErrRevisionNotFound {
		t.Fatalf("expected revision_not_found, got %v", err)
	}
}

func TestRollbackNoHistory(t *testing.T) {
	uc, _ := newRollback()
	_, err := uc.Rollback(context.Background(), "ghost", "")
	pe, ok := err.(*domain.ProvisionError)
	if !ok || pe.Code != domain.ErrNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}
}
