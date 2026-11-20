package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/approval"
	"github.com/pburkhalter/journarr/internal/store"
)

// approvalRoutes serve the removal requests MCP clients file. Reading them
// is like any API read; deciding needs a real SSO session.
func (s *server) approvalRoutes(r chi.Router) {
	r.Get("/approvals", s.listApprovals)
	r.Get("/approvals/{id}", s.getApproval)
	r.With(s.Auth.RequireSession).Post("/approvals/{id}/approve", s.approve)
	r.With(s.Auth.RequireSession).Post("/approvals/{id}/reject", s.reject)
}

// ApprovalView is an approval with its JSON fields decoded.
type ApprovalView struct {
	store.Approval
	Target json.RawMessage `json:"target"`
	Plan   json.RawMessage `json:"plan"`
	Result json.RawMessage `json:"result,omitempty"`
}

func viewOf(a store.Approval) ApprovalView {
	v := ApprovalView{Approval: a, Target: json.RawMessage(a.Target), Plan: json.RawMessage(a.Plan)}
	if a.Result != "" {
		v.Result = json.RawMessage(a.Result)
	}
	return v
}

func (s *server) listApprovals(w http.ResponseWriter, req *http.Request) {
	if s.Approvals == nil {
		writeJSON(w, map[string]any{"approvals": []any{}, "enabled": false})
		return
	}
	list, err := s.Store.ListApprovals(req.Context(), req.URL.Query().Get("status") == "pending",
		clampInt(req.URL.Query().Get("limit"), 50, 1, 200))
	if err != nil {
		httpError(w, s.Log, "list approvals", err)
		return
	}
	out := make([]ApprovalView, 0, len(list))
	for _, a := range list {
		out = append(out, viewOf(a))
	}
	writeJSON(w, map[string]any{"approvals": out, "enabled": true, "sso": s.Auth.Enabled()})
}

// getApproval returns the request with a fresh plan next to the stored one,
// so the owner sees whether anything changed since the LLM asked.
func (s *server) getApproval(w http.ResponseWriter, req *http.Request) {
	id, ok := pathID(req)
	if !ok || s.Approvals == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	a, err := s.Approvals.Get(req.Context(), id)
	if errors.Is(err, approval.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		httpError(w, s.Log, "get approval", err)
		return
	}
	out := map[string]any{"approval": viewOf(*a)}
	if a.Status == "pending" {
		var t actions.Title
		if err := json.Unmarshal([]byte(a.Target), &t); err == nil {
			if plan, err := s.Actions.PlanRemoval(req.Context(), t); err == nil {
				out["current_plan"] = plan
				out["plan_changed"] = plan.PlanHash != a.PlanHash
			} else {
				out["current_plan_error"] = err.Error()
			}
		}
	}
	writeJSON(w, out)
}

func (s *server) approve(w http.ResponseWriter, req *http.Request) {
	id, ok := pathID(req)
	if !ok || s.Approvals == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var body struct {
		Force bool `json:"force"`
	}
	if err := decodeJSON(req, &body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	ctx, done := s.detach(req)
	defer done()
	a, err := s.Approvals.Approve(ctx, id, s.Auth.Actor(req), body.Force)
	s.decisionAnswer(w, a, err)
}

func (s *server) reject(w http.ResponseWriter, req *http.Request) {
	id, ok := pathID(req)
	if !ok || s.Approvals == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	a, err := s.Approvals.Reject(req.Context(), id, s.Auth.Actor(req))
	s.decisionAnswer(w, a, err)
}

func (s *server) decisionAnswer(w http.ResponseWriter, a *store.Approval, err error) {
	switch {
	case err == nil:
		writeJSON(w, map[string]any{"approval": viewOf(*a)})
	case errors.Is(err, approval.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, approval.ErrNotPending):
		writeJSONStatus(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "not_pending"})
	case a != nil: // the removal ran and failed; the request records why
		code := "failed"
		switch {
		case errors.Is(err, actions.ErrPlanChanged):
			code = "plan_changed"
		case errors.Is(err, actions.ErrBeingWatched):
			code = "watching"
		}
		writeJSONStatus(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": code, "approval": viewOf(*a)})
	default:
		httpError(w, s.Log, "decide approval", err)
	}
}
