package store

import "context"

type Stats struct {
	Requests   map[string]int `json:"requests"`    // status -> count
	MediaItems map[string]int `json:"media_items"` // current_stage -> count
}

func (s *Store) FetchStats(ctx context.Context) (*Stats, error) {
	st := &Stats{Requests: map[string]int{}, MediaItems: map[string]int{}}

	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM requests GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		st.Requests[k] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows2, err := s.db.QueryContext(ctx, `SELECT current_stage, COUNT(*) FROM media_items GROUP BY current_stage`)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var k string
		var n int
		if err := rows2.Scan(&k, &n); err != nil {
			return nil, err
		}
		st.MediaItems[k] = n
	}
	return st, rows2.Err()
}
