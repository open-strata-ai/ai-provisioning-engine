// Command ai-provisioning-engine boots the deployment/provisioning engine.
// Dependency wiring is done by hand here (the offline stand-in for Wire
// compile-time DI). Production swaps in the PostgreSQL/Redis persistence and a
// real ArgoCD CICD client.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/open-strata-ai/ai-provisioning-engine/application/apply"
	"github.com/open-strata-ai/ai-provisioning-engine/application/rollback"
	"github.com/open-strata-ai/ai-provisioning-engine/domain"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/adapter"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/config"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/persistence"
	httpapi "github.com/open-strata-ai/ai-provisioning-engine/interfaces/http"
)

func main() {
	cfg, err := config.Load(os.Getenv("CONFIG_PATH"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	h := Bootstrap(cfg)

	listen := "0.0.0.0:8080"
	log.Printf("ai-provisioning-engine listening on %s (mode=%s)", listen, cfg.Provisioner.Mode)
	srv := &http.Server{
		Addr:              listen,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// Bootstrap assembles the full object graph from config and returns the HTTP
// handler. It is exported so tests can reuse the wiring.
func Bootstrap(cfg config.Config) *httpapi.Handler {
	store := persistence.NewStore()
	locker := persistence.NewLocker()

	opts := adapter.Options{
		Replicas:       cfg.Provisioner.Replicas,
		MaxSurge:       cfg.Provisioner.Rollout.MaxSurge,
		MaxUnavailable: cfg.Provisioner.Rollout.MaxUnavailable,
		MaxParallel:    cfg.Provisioner.MaxParallelDeploy,
		LockTTL:        time.Duration(cfg.Provisioner.LockTTLSeconds) * time.Second,
		Locker:         locker,
	}
	// Offline default: a fake CICD; production wires a real ArgoCD client.
	cicd := adapter.NewFakeCICD()
	selectDeployer := apply.SelectDeployer(func(profile string) domain.Deployer {
		return adapter.SelectDeployer(cfg.Provisioner.Mode, profile, opts, cicd)
	})

	applyUC := apply.New(selectDeployer, store)
	rollbackUC := rollback.New(selectDeployer, store)
	return httpapi.New(applyUC, rollbackUC)
}
