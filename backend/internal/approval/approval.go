// Package approval holds removals an MCP client asked for until the owner
// decides. A request stores the plan the client saw and its hash; approving
// re-plans and deletes only if the hash still matches. Approving needs an SSO
// session (enforced by the API), so no token — MCP or API — can approve.
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/pburkhalter/journarr/internal/actions"
	"github.com/pburkhalter/journarr/internal/store"
)

// DefaultTTL is how long a request waits for a decision. There is no push
// for it (owner decision), so it must survive a few days of not looking.
const DefaultTTL = 72 * time.Hour

var (
	ErrNotFound   = errors.New("removal request not found")
	ErrNotPending = errors.New("removal request is not pending (decided or expired)")
)

// Remover is the part of actions the service needs.
type Remover interface {
	PlanRemoval(ctx context.Context, t actions.Title) (*actions.RemovalPlan, error)
	Remove(ctx context.Context, t actions.Title, planHash string, force bool) ([]actions.RemovalStep, error)
}

type Service struct {
	Store   *store.Store
	Actions Remover
	Log     *slog.Logger
	TTL     time.Duration
	Publish func(event string, data any)
	Now     func() time.Time // tests
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}

// Result is the outcome stored on an executed or failed request.
type Result struct {
	Error string                `json:"error,omitempty"`
	Steps []actions.RemovalStep `json:"steps,omitempty"`
}

// targetKey identifies a title for the one-pending-request rule.
func targetKey(p *actions.RemovalPlan) string {
	if p.ArrID != 0 {
		return fmt.Sprintf("%s:%d", p.Arr, p.ArrID)
	}
	return fmt.Sprintf("%s:tmdb:%d:tvdb:%d", p.MediaType, p.TmdbID, p.TvdbID)
}

// Request files a removal request for a title. When the title already has a
// pending one, that one is returned with existing=true.
func (s *Service) Request(ctx context.Context, t actions.Title, reason, requestedBy string) (a *store.Approval, existing bool, err error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, false, errors.New("a reason is required: say why the title should go")
	}
	plan, err := s.Actions.PlanRemoval(ctx, t)
	if err != nil {
		return nil, false, err
	}
	key := targetKey(plan)
	if a, err := s.Store.PendingApprovalFor(ctx, key); err != nil || a != nil {
		return a, a != nil, err
	}
	target, _ := json.Marshal(t)
	planJSON, _ := json.Marshal(plan)
	ttl := s.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	id, err := s.Store.InsertApproval(ctx, store.Approval{
		Target: string(target), TargetKey: key, Title: plan.Title, Plan: string(planJSON),
		PlanHash: plan.PlanHash, Reason: truncate(reason, 1000), RequestedBy: requestedBy,
		ExpiresAt: s.now().Add(ttl),
	})
	if errors.Is(err, store.ErrApprovalPending) { // lost a race with an identical request
		a, err := s.Store.PendingApprovalFor(ctx, key)
		return a, a != nil, err
	}
	if err != nil {
		return nil, false, err
	}
	a, err = s.Store.GetApproval(ctx, id)
	if err == nil {
		s.Log.Info("approval: removal requested", "id", id, "title", plan.Title, "by", requestedBy)
		s.publish(a)
	}
	return a, false, err
}

// Get returns a request; ErrNotFound when unknown.
func (s *Service) Get(ctx context.Context, id int64) (*store.Approval, error) {
	a, err := s.Store.GetApproval(ctx, id)
	if err == nil && a == nil {
		err = ErrNotFound
	}
	return a, err
}

// CountPending counts requests waiting for a decision.
func (s *Service) CountPending(ctx context.Context) (int, error) {
	return s.Store.CountPendingApprovals(ctx)
}

// Approve executes a pending request for user. The removal runs with the
// stored plan hash: if the title changed since it was requested (other
// Sonarr/Radarr entry, folder, requests or Seerr entries), nothing is deleted
// and the request fails with actions.ErrPlanChanged. force overrides "someone
// is watching".
func (s *Service) Approve(ctx context.Context, id int64, user string, force bool) (*store.Approval, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var t actions.Title
	if err := json.Unmarshal([]byte(a.Target), &t); err != nil {
		return nil, fmt.Errorf("stored target: %w", err)
	}
	ok, err := s.Store.DecideApproval(ctx, id, "approved", user, s.now())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotPending
	}
	s.Log.Info("approval: approved", "id", id, "title", a.Title, "by", user, "force", force)

	steps, rmErr := s.Actions.Remove(ctx, t, a.PlanHash, force)
	status, res := "executed", Result{Steps: steps}
	if rmErr != nil {
		status, res.Error = "failed", rmErr.Error()
		s.Log.Warn("approval: removal failed", "id", id, "title", a.Title, "err", rmErr)
	}
	b, _ := json.Marshal(res)
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.Store.FinishApproval(wctx, id, status, string(b)); err != nil {
		return nil, err
	}
	a, err = s.Store.GetApproval(wctx, id)
	if err == nil {
		s.publish(a)
	}
	if rmErr != nil {
		return a, rmErr
	}
	return a, err
}

// Reject closes a pending request without deleting anything.
func (s *Service) Reject(ctx context.Context, id int64, user string) (*store.Approval, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	ok, err := s.Store.DecideApproval(ctx, id, "rejected", user, s.now())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotPending
	}
	s.Log.Info("approval: rejected", "id", id, "by", user)
	a, err := s.Store.GetApproval(ctx, id)
	if err == nil {
		s.publish(a)
	}
	return a, err
}

// ExpireDue closes requests past their deadline.
func (s *Service) ExpireDue(ctx context.Context) {
	ids, err := s.Store.ExpireApprovals(ctx, s.now())
	if err != nil {
		s.Log.Warn("approval: expire", "err", err)
		return
	}
	for _, id := range ids {
		s.Log.Info("approval: expired", "id", id)
		if a, err := s.Store.GetApproval(ctx, id); err == nil && a != nil {
			s.publish(a)
		}
	}
}

// Run expires due requests every interval until ctx ends.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s.ExpireDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) publish(a *store.Approval) {
	if s.Publish != nil && a != nil {
		s.Publish("approval.updated", map[string]any{"id": a.ID, "status": a.Status})
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
