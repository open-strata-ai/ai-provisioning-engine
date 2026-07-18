package apply

import (
	"context"
	"testing"

	"github.com/open-strata-ai/ai-provisioning-engine/domain"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/adapter"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/persistence"
)

func newUseCase() (*UseCase, domain.Store) {
	store := persistence.NewStore()
	opts := adapter.Options{Replicas: 2, Locker: persistence.NewLocker()}
	sel := SelectDeployer(func(profile string) domain.Deployer {
		return adapter.SelectDeployer(domain.ModeHelm, profile, opts, nil)
	})
	return New(sel, store), store
}

func TestApplyPipeline(t *testing.T) {
	uc, _ := newUseCase()
	plan := domain.AssemblyPlan{
		Added:    []domain.PlannedComponent{{RepoName: "svc-a", Version: "1.0.0"}},
		Reused:   []domain.PlannedComponent{{RepoName: "pg", Version: "16"}},
		Removed:  []domain.PlannedComponent{{RepoName: "old", Version: "0.1"}},
		Checksum: "chk-1",
	}
	resp, err := uc.Apply(context.Background(), plan, domain.ProfileStandard, "tenant-1")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Summary != "1 added, 1 reused, 1 removed" {
		t.Fatalf("unexpected summary: %q", resp.Summary)
	}
	if resp.PlanRef != "chk-1" {
		t.Fatalf("unexpected plan ref: %q", resp.PlanRef)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(resp.Results))
	}

	// Recorded audit trail is retrievable by checksum.
	got := uc.Result("chk-1")
	if len(got) != 3 {
		t.Fatalf("expected 3 recorded results, got %d", len(got))
	}
}

func TestApplyPreflightFailure(t *testing.T) {
	uc, store := newUseCase()
	plan := domain.AssemblyPlan{Added: []domain.PlannedComponent{{RepoName: "a"}}} // no checksum
	_, err := uc.Apply(context.Background(), plan, domain.ProfileStandard, "t")
	if err == nil {
		t.Fatal("expected preflight error")
	}
	pe, ok := err.(*domain.ProvisionError)
	if !ok || pe.Code != domain.ErrPreflight {
		t.Fatalf("expected preflight error, got %v", err)
	}
	if len(store.ByChecksum("")) != 0 {
		t.Fatal("no records should be written on preflight failure")
	}
}

func TestStatus(t *testing.T) {
	uc, _ := newUseCase()
	st, err := uc.Status(context.Background(), "svc-a")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Ready || st.Name != "svc-a" {
		t.Fatalf("unexpected status: %+v", st)
	}
}
