package adapter

import (
	"context"
	"testing"

	"github.com/open-strata-ai/ai-provisioning-engine/domain"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/persistence"
)

func samplePlan() domain.AssemblyPlan {
	return domain.AssemblyPlan{
		Added: []domain.PlannedComponent{
			{RepoName: "ai-gateway-core", Kind: domain.KindApp, Version: "1.0.0"},
			{RepoName: "ai-sandbox-manager", Kind: domain.KindApp, Version: "1.0.0", DependsOn: []string{"ai-gateway-core"}},
		},
		Reused:   []domain.PlannedComponent{{RepoName: "postgres", Kind: domain.KindOSS, Version: "16"}},
		Removed:  []domain.PlannedComponent{{RepoName: "legacy-svc", Kind: domain.KindApp, Version: "0.9"}},
		Checksum: "deadbeefcafe",
	}
}

// TestDeployerContract runs the same render/apply/status/rollback flow against
// every Deployer implementation (ARCH §6.5 SPI multi-implementation consistency).
func TestDeployerContract(t *testing.T) {
	opts := Options{Replicas: 2, Locker: persistence.NewLocker()}
	deployers := map[string]domain.Deployer{
		"helm":    NewHelmAdapter(opts),
		"compose": NewComposeAdapter(opts),
		"argocd":  NewArgoCDAdapter(opts, NewFakeCICD()),
	}
	plan := samplePlan()
	ctx := context.Background()

	for name, dep := range deployers {
		t.Run(name, func(t *testing.T) {
			out, err := dep.Render(ctx, plan, domain.ProfileStandard)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			// One artifact per added + removed component.
			if len(out.Artifacts) != 3 {
				t.Fatalf("expected 3 artifacts, got %d", len(out.Artifacts))
			}
			results, err := dep.Apply(ctx, out)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			// 2 added + 1 removed + 1 reused = 4 results.
			if len(results) != 4 {
				t.Fatalf("expected 4 results, got %d: %+v", len(results), results)
			}
			byComp := map[string]domain.ApplyResult{}
			for _, r := range results {
				byComp[r.Component] = r
			}
			if byComp["ai-gateway-core"].Status != domain.StatusSuccess {
				t.Fatalf("gateway should be success: %+v", byComp["ai-gateway-core"])
			}
			if byComp["ai-gateway-core"].Revision == "" {
				t.Fatal("added component must carry a revision")
			}
			if byComp["legacy-svc"].Action != domain.ActionRemove {
				t.Fatalf("legacy-svc should be removed: %+v", byComp["legacy-svc"])
			}
			if byComp["postgres"].Action != domain.ActionReuse {
				t.Fatalf("postgres should be reused: %+v", byComp["postgres"])
			}

			st, err := dep.Status(ctx, "ai-gateway-core")
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			if !st.Ready {
				t.Fatal("status should be ready")
			}

			if err := dep.Rollback(ctx, "ai-gateway-core", "rev-1"); err != nil {
				t.Fatalf("rollback: %v", err)
			}
			if err := dep.Rollback(ctx, "ai-gateway-core", ""); err == nil {
				t.Fatal("rollback with empty revision should fail")
			}
		})
	}
}

func TestSelectDeployer(t *testing.T) {
	opts := Options{}
	cases := []struct {
		mode, profile, want string
	}{
		{"", domain.ProfileStarter, domain.ModeCompose},
		{"", domain.ProfileStandard, domain.ModeHelm},
		{"", domain.ProfileAdvanced, domain.ModeHelm},
		{"", domain.ProfileFull, domain.ModeArgoCD},
		{domain.ModeCompose, domain.ProfileStandard, domain.ModeCompose},
		{domain.ModeArgoCD, domain.ProfileStarter, domain.ModeArgoCD},
	}
	for _, c := range cases {
		got := SelectDeployer(c.mode, c.profile, opts, NewFakeCICD()).Mode()
		if got != c.want {
			t.Fatalf("mode=%q profile=%q: want %s, got %s", c.mode, c.profile, c.want, got)
		}
	}
}

func TestArgoCDSyncsManifests(t *testing.T) {
	cicd := NewFakeCICD()
	dep := NewArgoCDAdapter(Options{}, cicd)
	out, _ := dep.Render(context.Background(), samplePlan(), domain.ProfileFull)
	if _, err := dep.Apply(context.Background(), out); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(cicd.Synced) != 2 { // 2 added components synced
		t.Fatalf("expected 2 synced manifests, got %d", len(cicd.Synced))
	}
}
