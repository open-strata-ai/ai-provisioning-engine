// Package httpapi exposes the provisioning-engine HTTP surface (SPECS §7.1). It
// uses the standard library net/http; production runs behind Higress with
// Keycloak JWT verification and OTel tracing.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/open-strata-ai/ai-provisioning-engine/application/apply"
	"github.com/open-strata-ai/ai-provisioning-engine/application/rollback"
	"github.com/open-strata-ai/ai-provisioning-engine/domain"
)

// Handler wires the use cases to HTTP endpoints.
type Handler struct {
	apply    *apply.UseCase
	rollback *rollback.UseCase
	metrics  *metrics
}

// New builds a Handler.
func New(applyUC *apply.UseCase, rollbackUC *rollback.UseCase) *Handler {
	return &Handler{apply: applyUC, rollback: rollbackUC, metrics: &metrics{}}
}

// Routes returns a ServeMux with all endpoints registered (SPECS §7.1).
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/apply", h.handleApply)
	mux.HandleFunc("/v1/rollback", h.handleRollback)
	mux.HandleFunc("/v1/status/", h.handleStatus)
	mux.HandleFunc("/v1/plan/", h.handlePlan)
	mux.HandleFunc("/healthz", h.handleHealthz)
	mux.HandleFunc("/metrics", h.handleMetrics)
	return mux
}

// applyRequest is the ApplyRequest (SPECS §7.2).
type applyRequest struct {
	Plan     domain.AssemblyPlan `json:"plan"`
	Profile  string              `json:"profile"`
	TenantID string              `json:"tenant_id"`
}

func (h *Handler) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "method not allowed"))
		return
	}
	var req applyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "invalid JSON body"))
		return
	}
	resp, err := h.apply.Apply(r.Context(), req.Plan, req.Profile, req.TenantID)
	if err != nil {
		atomic.AddInt64(&h.metrics.applyErrors, 1)
		writeErr(w, err)
		return
	}
	atomic.AddInt64(&h.metrics.applyTotal, 1)
	writeJSON(w, http.StatusOK, resp)
}

// rollbackRequest is the RollbackRequest (SPECS §7.2).
type rollbackRequest struct {
	Component  string `json:"component"`
	ToRevision string `json:"to_revision"`
}

func (h *Handler) handleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "method not allowed"))
		return
	}
	var req rollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "invalid JSON body"))
		return
	}
	if req.Component == "" {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "component is required"))
		return
	}
	rev, err := h.rollback.Rollback(r.Context(), req.Component, req.ToRevision)
	if err != nil {
		writeErr(w, err)
		return
	}
	atomic.AddInt64(&h.metrics.rollbackTotal, 1)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "rolled-back",
		"component": req.Component,
		"revision":  rev,
	})
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "method not allowed"))
		return
	}
	component := strings.TrimPrefix(r.URL.Path, "/v1/status/")
	if component == "" {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "component is required"))
		return
	}
	st, err := h.apply.Status(r.Context(), component)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handlePlan serves GET /v1/plan/{checksum}/apply-result (SPECS §7.1).
func (h *Handler) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "method not allowed"))
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/plan/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "apply-result" {
		writeErr(w, domain.NewError(domain.ErrInvalidRequest, "expected /v1/plan/{checksum}/apply-result"))
		return
	}
	results := h.apply.Result(parts[0])
	writeJSON(w, http.StatusOK, map[string]any{"plan_checksum": parts[0], "results": results})
}

func (h *Handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var b strings.Builder
	writeCounter(&b, "provisioner_apply_total", "total apply requests", atomic.LoadInt64(&h.metrics.applyTotal))
	writeCounter(&b, "provisioner_apply_errors_total", "failed apply requests", atomic.LoadInt64(&h.metrics.applyErrors))
	writeCounter(&b, "provisioner_rollback_total", "total rollbacks", atomic.LoadInt64(&h.metrics.rollbackTotal))
	_, _ = w.Write([]byte(b.String()))
}

type metrics struct {
	applyTotal    int64
	applyErrors   int64
	rollbackTotal int64
}

func writeCounter(b *strings.Builder, name, help string, v int64) {
	b.WriteString("# HELP " + name + " " + help + "\n")
	b.WriteString("# TYPE " + name + " counter\n")
	b.WriteString(name + " " + strconv.FormatInt(v, 10) + "\n")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := string(domain.ErrUpstream)
	msg := err.Error()
	if pe, ok := err.(*domain.ProvisionError); ok {
		status = pe.HTTPStatus()
		code = string(pe.Code)
		msg = pe.Message
	}
	var body errBody
	body.Error.Code = code
	body.Error.Message = msg
	writeJSON(w, status, body)
}
