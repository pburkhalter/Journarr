package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type Download struct {
	ID               int64      `json:"id"`
	ClientDownloadID string     `json:"client_download_id"`
	ArrarrNzoID      string     `json:"arrarr_nzo_id,omitempty"`
	Arr              string     `json:"arr"`
	Source           string     `json:"source,omitempty"`
	ReleaseTitle     string     `json:"release_title,omitempty"`
	Indexer          string     `json:"indexer,omitempty"`
	SizeBytes        *int64     `json:"size_bytes,omitempty"`
	State            string     `json:"state"`
	BytesDownloaded  *int64     `json:"bytes_downloaded,omitempty"`
	BytesTotal       *int64     `json:"bytes_total,omitempty"`
	LocalPath        string     `json:"local_path,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	GrabbedAt        *time.Time `json:"grabbed_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const dlSelect = `SELECT id, client_download_id, COALESCE(arrarr_nzo_id,''), arr,
	COALESCE(source,''), COALESCE(release_title,''), COALESCE(indexer,''), size_bytes,
	state, bytes_downloaded, bytes_total, COALESCE(local_path,''), COALESCE(last_error,''),
	grabbed_at, updated_at FROM downloads`

func scanDownload(row rowScanner) (*Download, error) {
	var d Download
	var grabbed, updated sql.NullTime
	if err := row.Scan(&d.ID, &d.ClientDownloadID, &d.ArrarrNzoID, &d.Arr, &d.Source,
		&d.ReleaseTitle, &d.Indexer, &d.SizeBytes, &d.State,
		&d.BytesDownloaded, &d.BytesTotal, &d.LocalPath, &d.LastError,
		&grabbed, &updated); err != nil {
		return nil, err
	}
	if grabbed.Valid {
		d.GrabbedAt = &grabbed.Time
	}
	d.UpdatedAt = updated.Time
	return &d, nil
}

// NormalizeDownloadID lowercases arr downloadIds so SAB nzo ids and BT
// infohashes compare stably (Sonarr uppercases infohashes, arrarr stores
// them lowercase).
func NormalizeDownloadID(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// UpsertDownload records a grab. Matching prefers the newest non-terminal
// download with the same client id (torrent infohash reuse: a later re-grab
// of the same release gets its own row once the old one is terminal).
func (s *Store) UpsertDownload(ctx context.Context, d Download) (int64, error) {
	d.ClientDownloadID = NormalizeDownloadID(d.ClientDownloadID)
	existing, err := s.FindActiveDownloadByClientID(ctx, d.ClientDownloadID)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		_, err = s.db.ExecContext(ctx, `
			UPDATE downloads SET release_title = CASE WHEN ? != '' THEN ? ELSE release_title END,
				indexer = CASE WHEN ? != '' THEN ? ELSE indexer END,
				size_bytes = COALESCE(?, size_bytes),
				source = CASE WHEN ? != '' THEN ? ELSE source END,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			d.ReleaseTitle, d.ReleaseTitle, d.Indexer, d.Indexer, d.SizeBytes,
			d.Source, d.Source, existing.ID)
		return existing.ID, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO downloads (client_download_id, arr, source, release_title, indexer,
			size_bytes, state, grabbed_at)
		VALUES (?, ?, ?, ?, ?, ?, 'grabbed', CURRENT_TIMESTAMP)`,
		d.ClientDownloadID, d.Arr, d.Source, d.ReleaseTitle, d.Indexer, d.SizeBytes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FindActiveDownloadByClientID(ctx context.Context, clientID string) (*Download, error) {
	d, err := scanDownload(s.db.QueryRowContext(ctx,
		dlSelect+` WHERE client_download_id = ? AND state NOT IN ('imported','failed','canceled')
		ORDER BY id DESC LIMIT 1`, NormalizeDownloadID(clientID)))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func (s *Store) FindDownloadByClientID(ctx context.Context, clientID string) (*Download, error) {
	d, err := scanDownload(s.db.QueryRowContext(ctx,
		dlSelect+` WHERE client_download_id = ? ORDER BY id DESC LIMIT 1`, NormalizeDownloadID(clientID)))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func (s *Store) LinkDownloadItem(ctx context.Context, downloadID, mediaItemID int64, cycle int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO download_items (download_id, media_item_id, cycle)
		VALUES (?, ?, ?)`, downloadID, mediaItemID, cycle)
	return err
}

func (s *Store) SetDownloadState(ctx context.Context, id int64, state, lastError string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE downloads SET state = ?,
			last_error = CASE WHEN ? != '' THEN ? ELSE last_error END,
			completed_at = CASE WHEN ? IN ('imported','failed','canceled') THEN CURRENT_TIMESTAMP ELSE completed_at END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, state, lastError, lastError, state, id)
	return err
}

// UpdateDownloadProgress is fed by the queue poller; it writes directly (no
// events row) because byte counters are ephemeral, not transitions.
func (s *Store) UpdateDownloadProgress(ctx context.Context, id int64, downloaded, total int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE downloads SET bytes_downloaded = ?, bytes_total = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, downloaded, total, id)
	return err
}

// DownloadsForRequest returns downloads linked (via items) to a request.
func (s *Store) DownloadsForRequest(ctx context.Context, requestID int64) ([]Download, error) {
	rows, err := s.db.QueryContext(ctx, dlSelect+` WHERE id IN (
			SELECT DISTINCT di.download_id FROM download_items di
			JOIN media_items mi ON mi.id = di.media_item_id
			WHERE mi.request_id = ?)
		ORDER BY id DESC`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ItemIDsForDownload lists the media items linked to a download.
func (s *Store) ItemIDsForDownload(ctx context.Context, downloadID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT media_item_id FROM download_items WHERE download_id = ?`, downloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ActiveDownloads returns non-terminal downloads (for the queue poller).
func (s *Store) ActiveDownloads(ctx context.Context) ([]Download, error) {
	rows, err := s.db.QueryContext(ctx,
		dlSelect+` WHERE state NOT IN ('imported','failed','canceled') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}
