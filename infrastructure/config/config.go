// Package config loads provisioning-engine configuration (SPECS §11.5). To keep
// the build stdlib-only it ships typed defaults and an optional JSON overlay;
// production renders infrastructure/config/config.yaml via the meta repository.
package config

import (
	"encoding/json"
	"os"
)

// Config mirrors the DESIGN §7.3 / SPECS §11.5 config keys.
type Config struct {
	Provisioner struct {
		Mode   string `json:"mode"` // helm | compose | argocd
		ArgoCD struct {
			Enabled   bool   `json:"enabled"`
			Namespace string `json:"namespace"`
		} `json:"argocd"`
		Rollout struct {
			MaxSurge          int `json:"maxSurge"`
			MaxUnavailable    int `json:"maxUnavailable"`
			ProbeGraceSeconds int `json:"probeGraceSeconds"`
		} `json:"rollout"`
		GrayCutover struct {
			DoubleWriteVerify bool `json:"doubleWriteVerify"`
		} `json:"grayCutover"`
		MetaRepo struct {
			ProfilesPath string `json:"profilesPath"`
			ConfigPath   string `json:"configPath"`
		} `json:"metaRepo"`
		MaxParallelDeploy int `json:"maxParallelDeploy"`
		ApplyTimeout      int `json:"applyTimeout"`
		ReadyTimeout      int `json:"readyTimeout"`
		Replicas          int `json:"replicas"`
		LockTTLSeconds    int `json:"lockTTLSeconds"`
	} `json:"provisioner"`
}

// Default returns the SPECS §11.5 defaults.
func Default() Config {
	var c Config
	c.Provisioner.Mode = "helm"
	c.Provisioner.ArgoCD.Enabled = false
	c.Provisioner.ArgoCD.Namespace = "ai-system"
	c.Provisioner.Rollout.MaxSurge = 1
	c.Provisioner.Rollout.MaxUnavailable = 0
	c.Provisioner.Rollout.ProbeGraceSeconds = 30
	c.Provisioner.GrayCutover.DoubleWriteVerify = true
	c.Provisioner.MetaRepo.ProfilesPath = "openstrata-meta/profiles"
	c.Provisioner.MetaRepo.ConfigPath = "openstrata-meta/dependencies/config"
	c.Provisioner.MaxParallelDeploy = 8
	c.Provisioner.ApplyTimeout = 300
	c.Provisioner.ReadyTimeout = 30
	c.Provisioner.Replicas = 2
	c.Provisioner.LockTTLSeconds = 60
	return c
}

// Load reads a JSON overlay from path and merges it onto the defaults. A missing
// path returns the defaults unchanged.
func Load(path string) (Config, error) {
	c := Default()
	if path == "" {
		return c, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}
