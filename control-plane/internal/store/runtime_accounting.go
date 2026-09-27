package store

import (
	"context"
	"database/sql"
	"time"
)

// RuntimeAccounting is elapsed controller-observed running time since tracking
// began. It is neither CPU usage nor a reconstruction of older runtime history.
type RuntimeAccounting struct {
	StartedAt            *string `json:"started_at"`
	ObservedRunningSince *string `json:"observed_running_since"`
	TrackingSince        string  `json:"tracking_since"`
	TotalRunningSeconds  int64   `json:"total_running_seconds"`
	CheckedAt            string  `json:"checked_at"`
}

func (s *Store) RuntimeAccounting(ctx context.Context, id string) (*RuntimeAccounting, error) {
	var since, total, now int64
	var running sql.NullInt64
	var known bool
	err := s.db.QueryRowContext(ctx, `SELECT tracking_since,running_since,start_known,total_seconds,unixepoch() FROM sandbox_runtime_accounting WHERE sandbox_id=?`, id).Scan(&since, &running, &known, &total, &now)
	if err != nil {
		return nil, err
	}
	out := &RuntimeAccounting{TrackingSince: time.Unix(since, 0).UTC().Format(time.RFC3339), TotalRunningSeconds: total, CheckedAt: time.Unix(now, 0).UTC().Format(time.RFC3339)}
	if running.Valid {
		start := time.Unix(running.Int64, 0).UTC().Format(time.RFC3339)
		out.ObservedRunningSince = &start
		if known {
			out.StartedAt = &start
		}
		if now > running.Int64 {
			out.TotalRunningSeconds += now - running.Int64
		}
	}
	return out, nil
}
