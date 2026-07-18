package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-strata-ai/ai-provisioning-engine/application/apply"
	"github.com/open-strata-ai/ai-provisioning-engine/application/rollback"
	"github.com/open-strata-ai/ai-provisioning-engine/domain"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/adapter"
	"github.com/open-strata-ai/ai-provisioning-engine/infrastructure/persistence"
)

func newServer() *httptest.Server {
	store := persistence.NewStore()
	opts := adapter.Options{Replicas: 2, Locker: persistence.NewLocker()}
	sel := apply.SelectDeployer(func(profile string) domain.Deployer {
		return adapter.SelectDeployer(domain.ModeArgoCD, profile, opts, adapter.NewFakeCICD())
	})
	h := New(apply.New(sel, store), rollback.New(sel, store))
	return httptest.NewServer(h.Routes())
}

func TestE2EApplyThenRollbackThenStatus(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	// Apply.
	body := map[string]any{
		"plan": map[string]any{
			"added": []map[string]any{
				{"repo_name": "svc-a", "version": "1.0.0"},
				{"repo_name": "svc-b", "version": "1.0.0", "depends_on": []string{"svc-a"}},
			},
			"removed":  []map[string]any{{"repo_name": "old"}},
			"checksum": "chk-e2e",
		},
		"profile":   "full",
		"tenant_id": "t1",
	}
	resp := postJSON(t, srv.URL+"/v1/apply", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply status %d", resp.StatusCode)
	}
	var applyResp apply.Response
	decode(t, resp, &applyResp)
	if applyResp.Summary != "2 added, 0 reused, 1 removed" {
		t.Fatalf("unexpected summary: %q", applyResp.Summary)
	}

	// apply-result lookup.
	r2, err := http.Get(srv.URL + "/v1/plan/chk-e2e/apply-result")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Results []domain.ApplyResult `json:"results"`
	}
	decode(t, r2, &res)
	if len(res.Results) != 3 {
		t.Fatalf("expected 3 apply results, got %d", len(res.Results))
	}

	// Rollback svc-a to its recorded revision.
	var svcARev string
	for _, r := range applyResp.Results {
		if r.Component == "svc-a" {
			svcARev = r.Revision
		}
	}
	rb := postJSON(t, srv.URL+"/v1/rollback", map[string]any{"component": "svc-a", "to_revision": svcARev})
	if rb.StatusCode != http.StatusOK {
		t.Fatalf("rollback status %d", rb.StatusCode)
	}

	// Status.
	st, err := http.Get(srv.URL + "/v1/status/svc-a")
	if err != nil {
		t.Fatal(err)
	}
	var cs domain.ComponentStatus
	decode(t, st, &cs)
	if !cs.Ready || cs.Name != "svc-a" {
		t.Fatalf("unexpected status: %+v", cs)
	}
}

func TestE2EPreflightError(t *testing.T) {
	srv := newServer()
	defer srv.Close()
	// Missing checksum -> preflight 400.
	resp := postJSON(t, srv.URL+"/v1/apply", map[string]any{
		"plan":    map[string]any{"added": []map[string]any{{"repo_name": "a"}}},
		"profile": "standard",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv := newServer()
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status %d", resp.StatusCode)
	}
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
