package store

import (
	"context"
	"database/sql"
	"time"
)

type Event struct {
	ID          int64
	Source      string // seerr|sonarr|radarr|arrarr|jellyfin|notifyarr|poller|action
	Kind        string
	DedupeKey   string // "" = no natural key
	Payload     []byte
	ReceivedAt  time.Time
	MatchStatus string
}

// InsertEvent appends a raw event. A duplicate dedupe_key is silently
// ignored (inserted=false) — that is the first idempotency layer between
// webhooks and reconciling pollers.
func (s *Store) InsertEvent(ctx context.Context, source, kind, dedupeKey string, payload []byte) (int64, bool, error) {
	var key any
	if dedupeKey != "" {
		key = dedupeKey
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO events (source, kind, dedupe_key, payload)
		VALUES (?, ?, ?, ?)`, source, kind, key, string(payload))
	if err != nil {
		return 0, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	if n == 0 {
		return 0, false, nil
	}
	id, err := res.LastInsertId()
	return id, true, err
}

// FetchUnprocessed returns the oldest pending events, in insert order.
func (s *Store) FetchUnprocessed(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, kind, COALESCE(dedupe_key,''), payload, received_at
		FROM events WHERE processed_at IS NULL ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var payload string
		var received sql.NullTime
		if err := rows.Scan(&e.ID, &e.Source, &e.Kind, &e.DedupeKey, &payload, &received); err != nil {
			return nil, err
		}
		e.Payload = []byte(payload)
		e.ReceivedAt = received.Time
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkProcessed finalizes an event with its match outcome and entity links
// (zero ids are stored as NULL).
func (s *Store) MarkProcessed(ctx context.Context, eventID int64, matchStatus string, requestID, mediaItemID, downloadID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE events SET processed_at = CURRENT_TIMESTAMP, match_status = ?,
			request_id = NULLIF(?, 0), media_item_id = NULLIF(?, 0), download_id = NULLIF(?, 0)
		WHERE id = ?`, matchStatus, requestID, mediaItemID, downloadID, eventID)
	return err
}

// EventsForMedia returns the processed event log linked to a media item.
func (s *Store) EventsForMedia(ctx context.Context, mediaItemID int64, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, kind, COALESCE(dedupe_key,''), payload, received_at
		FROM events WHERE media_item_id = ? ORDER BY id DESC LIMIT ?`, mediaItemID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var payload string
		var received sql.NullTime
		if err := rows.Scan(&e.ID, &e.Source, &e.Kind, &e.DedupeKey, &payload, &received); err != nil {
			return nil, err
		}
		e.Payload = []byte(payload)
		e.ReceivedAt = received.Time
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReapEvents deletes processed events older than the retention window.
func (s *Store) ReapEvents(ctx context.Context, olderThanDays int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM events WHERE processed_at IS NOT NULL
		AND received_at < datetime('now', ?)`, // e.g. '-90 days'
		"-"+itoa(olderThanDays)+" days")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func itoa(n int) string {
	if n < 0 {
		n = 0
	}
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
