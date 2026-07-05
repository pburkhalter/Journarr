package store

import (
	"context"
	"database/sql"
	"time"
)

type ServiceHealth struct {
	Service   string    `json:"service"`
	Status    string    `json:"status"`
	LatencyMS int64     `json:"latency_ms"`
	Version   string    `json:"version,omitempty"`
	Detail    string    `json:"detail,omitempty"` // raw JSON blob, per-service extras
	CheckedAt time.Time `json:"checked_at"`
}

func (s *Store) UpsertServiceHealth(ctx context.Context, h ServiceHealth) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO service_health (service, status, latency_ms, version, detail, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(service) DO UPDATE SET
			status = excluded.status,
			latency_ms = excluded.latency_ms,
			version = excluded.version,
			detail = excluded.detail,
			checked_at = excluded.checked_at`,
		h.Service, h.Status, h.LatencyMS, h.Version, h.Detail, h.CheckedAt.UTC())
	return err
}

func (s *Store) ListServiceHealth(ctx context.Context) ([]ServiceHealth, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT service, status, latency_ms, COALESCE(version,''), COALESCE(detail,''), checked_at
		FROM service_health ORDER BY service`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceHealth{}
	for rows.Next() {
		var h ServiceHealth
		var checked sql.NullTime
		if err := rows.Scan(&h.Service, &h.Status, &h.LatencyMS, &h.Version, &h.Detail, &checked); err != nil {
			return nil, err
		}
		h.CheckedAt = checked.Time
		out = append(out, h)
	}
	return out, rows.Err()
}
