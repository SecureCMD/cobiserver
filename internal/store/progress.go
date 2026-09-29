package store

import (
	"database/sql"
	"errors"
	"time"
)

type Progress struct {
	Document    string
	Percentage  float64
	ProgressStr string
	Device      string
	DeviceID    string
	Metadata    string
	UpdatedAt   time.Time
}

func (s *Store) UpsertProgress(userID int64, p Progress) error {
	_, err := s.write.Exec(`
		INSERT INTO progress (user_id, document, percentage, progress, device, device_id, metadata, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, document) DO UPDATE SET
			percentage = excluded.percentage,
			progress = excluded.progress,
			device = excluded.device,
			device_id = excluded.device_id,
			metadata = excluded.metadata,
			updated_at = CURRENT_TIMESTAMP
	`, userID, p.Document, p.Percentage, p.ProgressStr, p.Device, p.DeviceID, p.Metadata)
	return err
}

func (s *Store) GetProgress(userID int64, document string) (*Progress, error) {
	row := s.read.QueryRow(`
		SELECT document, percentage, progress, device, device_id, metadata, updated_at
		FROM progress WHERE user_id = ? AND document = ?
	`, userID, document)
	var p Progress
	var percentage sql.NullFloat64
	if err := row.Scan(&p.Document, &percentage, &p.ProgressStr, &p.Device, &p.DeviceID, &p.Metadata, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.Percentage = percentage.Float64
	return &p, nil
}

func (s *Store) ListProgressForUser(userID int64) ([]Progress, error) {
	rows, err := s.read.Query(`
		SELECT document, percentage, progress, device, device_id, metadata, updated_at
		FROM progress WHERE user_id = ? ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Progress
	for rows.Next() {
		var p Progress
		var percentage sql.NullFloat64
		if err := rows.Scan(&p.Document, &percentage, &p.ProgressStr, &p.Device, &p.DeviceID, &p.Metadata, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Percentage = percentage.Float64
		out = append(out, p)
	}
	return out, rows.Err()
}
