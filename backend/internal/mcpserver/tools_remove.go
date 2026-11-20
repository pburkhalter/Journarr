package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/audit"
	"github.com/pburkhalter/journarr/internal/store"
)

// Approvals files and reports removal requests (internal/approval). There
// is deliberately no way to approve through this interface.
type Approvals interface {
	Request(ctx context.Context, t actions.Title, reason, requestedBy string) (*store.Approval, bool, error)
	Get(ctx context.Context, id int64) (*store.Approval, error)
	CountPending(ctx context.Context) (int, error)
}

func (s *Server) registerRemovalTools() {
	addTool(s, &mcp.Tool{Name: "plan_removal", Annotations: readOnly("Preview a removal"),
		Description: "Show what removing a title would delete: the Sonarr/Radarr entry with its files " +
			"(count, size, folder, recycle bin), running downloads, Seerr entries, Journarr requests and " +
			"who is watching it. Changes nothing."},
		true, s.planRemoval)

	addTool(s, &mcp.Tool{Name: "request_removal", Annotations: mutating("Ask to remove a title", true, true),
		Description: "Ask the owner to remove a movie or series everywhere (Sonarr/Radarr with files, " +
			"downloads, Seerr, Journarr). Nothing is deleted now: the owner approves or rejects it in the " +
			"Journarr UI (approve_url), and it expires otherwise. Give a reason. Tell the user it waits " +
			"for their approval; check with get_removal_status."},
		true, s.requestRemoval)

	addTool(s, &mcp.Tool{Name: "get_removal_status", Annotations: readOnly("Removal request status"),
		Description: "Status of a removal request: pending, rejected, expired, executed (with the steps " +
			"taken) or failed (with the reason)."},
		true, s.getRemovalStatus)
}

type removalTarget struct {
	TitleRef
	RequestID int64 `json:"request_id,omitempty" jsonschema:"a Journarr request id instead of the title ids"`
}

func (r removalTarget) title() actions.Title {
	t := r.TitleRef.title()
	t.RequestID = r.RequestID
	return t
}

func (s *Server) planRemoval(ctx context.Context, in removalTarget) (any, error) {
	return s.d.Actions.PlanRemoval(ctx, in.title())
}

type requestRemovalIn struct {
	removalTarget
	Reason string `json:"reason" jsonschema:"why the title should go, shown to the owner"`
}

func (s *Server) requestRemoval(ctx context.Context, in requestRemovalIn) (any, error) {
	a, existing, err := s.d.Approvals.Request(ctx, in.title(), in.Reason, audit.Actor(ctx))
	if err != nil {
		return nil, err
	}
	out := s.approvalView(a)
	if existing {
		out["note"] = "a removal request for this title was already pending; nothing new was filed"
	} else {
		out["note"] = "nothing is deleted yet: the owner has to approve this in the Journarr UI"
	}
	return out, nil
}

type removalStatusIn struct {
	ApprovalID int64 `json:"approval_id" jsonschema:"id returned by request_removal"`
}

func (s *Server) getRemovalStatus(ctx context.Context, in removalStatusIn) (any, error) {
	a, err := s.d.Approvals.Get(ctx, in.ApprovalID)
	if err != nil {
		return nil, err
	}
	return s.approvalView(a), nil
}

func (s *Server) approvalView(a *store.Approval) map[string]any {
	v := map[string]any{
		"approval_id":  a.ID,
		"title":        a.Title,
		"status":       a.Status,
		"requested_by": a.RequestedBy,
		"reason":       a.Reason,
		"created_at":   a.CreatedAt.UTC().Format(time.RFC3339),
		"expires_at":   a.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if s.cfg.PublicURL != "" {
		v["approve_url"] = strings.TrimRight(s.cfg.PublicURL, "/") + fmt.Sprintf("/approvals?id=%d", a.ID)
	}
	var plan actions.RemovalPlan
	if json.Unmarshal([]byte(a.Plan), &plan) == nil {
		v["plan"] = map[string]any{"arr": plan.Arr, "path": plan.Path, "files": plan.Files,
			"size_gib": gibs(plan.SizeBytes), "recycle_bin": plan.RecycleBin, "requests": plan.Requests,
			"in_seerr": plan.InSeerr, "downloads": plan.Downloads}
	}
	if a.DecidedBy != "" {
		v["decided_by"] = a.DecidedBy
	}
	if a.DecidedAt != nil {
		v["decided_at"] = a.DecidedAt.UTC().Format(time.RFC3339)
	}
	if a.Result != "" {
		v["result"] = json.RawMessage(a.Result)
	}
	return v
}
