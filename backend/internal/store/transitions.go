package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type StageTransition struct {
	ID            int64     `json:"id"`
	MediaItemID   int64     `json:"media_item_id"`
	Cycle         int       `json:"cycle"`
	Stage         string    `json:"stage"`
	EnteredAt     time.Time `json:"entered_at"`
	SourceEventID *int64    `json:"source_event_id,omitempty"`
	Note          string    `json:"note,omitempty"`
}

// Stage is one row of the pipeline stage catalog.
type Stage struct {
	Key     string `json:"key"`
	Ordinal int    `json:"ordinal"`
	Label   string `json:"label"`
	Active  bool   `json:"active"`
}

// ListStages returns the full stage catalog ordered by ordinal. It is the
// canonical stage enum; the frontend fetches it instead of re-declaring stages.
func (s *Store) ListStages(ctx context.Context) ([]Stage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, ordinal, label, active FROM stages ORDER BY ordinal`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stage
	for rows.Next() {
		var st Stage
		if err := rows.Scan(&st.Key, &st.Ordinal, &st.Label, &st.Active); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// LoadStageOrdinals reads the stage catalog once at startup.
func (s *Store) LoadStageOrdinals(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, ordinal FROM stages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var o int
		if err := rows.Scan(&k, &o); err != nil {
			return nil, err
		}
		out[k] = o
	}
	return out, rows.Err()
}

// ApplyStage records a media item entering a stage within a cycle. The
// UNIQUE(media_item_id, cycle, stage) constraint makes this idempotent —
// a webhook and the reconciling poller reporting the same fact converge to
// one transition. current_stage only ever moves forward within a cycle;
// a higher cycle always wins.
func (s *Store) ApplyStage(ctx context.Context, itemID int64, cycle int, stage string, sourceEventID int64, note string) (bool, error) {
	var evID any
	if sourceEventID > 0 {
		evID = sourceEventID
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO stage_transitions (media_item_id, cycle, stage, source_event_id, note)
		VALUES (?, ?, ?, ?, ?)`, itemID, cycle, stage, evID, note)
	if err != nil {
		return false, fmt.Errorf("insert transition: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	applied := n > 0

	// Advance the derived pointer if (cycle, ordinal) is ahead of the current one.
	_, err = s.db.ExecContext(ctx, `
		UPDATE media_items SET current_stage = ?, current_cycle = ?, stuck_since = NULL,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND (
			current_cycle < ?
			OR (current_cycle = ? AND
				(SELECT ordinal FROM stages WHERE key = media_items.current_stage) <
				(SELECT ordinal FROM stages WHERE key = ?))
		)`, stage, cycle, itemID, cycle, cycle, stage)
	if err != nil {
		return applied, fmt.Errorf("advance current_stage: %w", err)
	}
	return applied, nil
}

// TransitionsForItem returns the full stage history of an item, all cycles.
func (s *Store) TransitionsForItem(ctx context.Context, itemID int64) ([]StageTransition, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, media_item_id, cycle, stage, entered_at, source_event_id, COALESCE(note,'')
		FROM stage_transitions WHERE media_item_id = ?
		ORDER BY cycle, (SELECT ordinal FROM stages WHERE key = stage)`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StageTransition{}
	for rows.Next() {
		var t StageTransition
		var entered sql.NullTime
		if err := rows.Scan(&t.ID, &t.MediaItemID, &t.Cycle, &t.Stage, &entered, &t.SourceEventID, &t.Note); err != nil {
			return nil, err
		}
		t.EnteredAt = entered.Time
		out = append(out, t)
	}
	return out, rows.Err()
}

// HasTransition reports whether the item already entered the stage in the cycle.
func (s *Store) HasTransition(ctx context.Context, itemID int64, cycle int, stage string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM stage_transitions WHERE media_item_id = ? AND cycle = ? AND stage = ?`,
		itemID, cycle, stage).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// NotifiedItemIDs returns the set of a request's items that already have a
// 'notified' transition in any cycle. The notify controller uses it to avoid
// re-announcing an item that re-completes after a retry or a quality upgrade
// (each such cycle re-reaches the notify stage).
func (s *Store) NotifiedItemIDs(ctx context.Context, requestID int64) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT st.media_item_id
		FROM stage_transitions st
		JOIN media_items mi ON mi.id = st.media_item_id
		WHERE mi.request_id = ? AND st.stage = 'notified'`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
