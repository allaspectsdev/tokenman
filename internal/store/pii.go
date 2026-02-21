package store

import (
	"fmt"
)

// PIILogEntry represents a single PII detection/action log record.
type PIILogEntry struct {
	ID        int64
	RequestID string
	Timestamp string
	PIIType   string
	Action    string
	FieldPath string
	Context   string
}

// LogPII inserts a new PII log entry. The ID field is ignored and
// auto-assigned by the database.
func (s *Store) LogPII(entry *PIILogEntry) error {
	result, err := s.writer.Exec(`
		INSERT INTO pii_log (request_id, timestamp, pii_type, action, field_path, context)
		VALUES (?, ?, ?, ?, ?, ?)`,
		entry.RequestID, entry.Timestamp, entry.PIIType,
		entry.Action, entry.FieldPath, entry.Context,
	)
	if err != nil {
		return fmt.Errorf("store: log pii: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: log pii last insert id: %w", err)
	}
	entry.ID = id
	return nil
}

// GetPIILog retrieves all PII log entries for a specific request ID,
// ordered by timestamp ascending.
func (s *Store) GetPIILog(requestID string) ([]*PIILogEntry, error) {
	rows, err := s.reader.Query(`
		SELECT id, request_id, timestamp, pii_type, action, field_path, context
		FROM pii_log
		WHERE request_id = ?
		ORDER BY timestamp ASC`, requestID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get pii log for request %s: %w", requestID, err)
	}
	defer rows.Close()

	var results []*PIILogEntry
	for rows.Next() {
		e := &PIILogEntry{}
		if err := rows.Scan(
			&e.ID, &e.RequestID, &e.Timestamp, &e.PIIType,
			&e.Action, &e.FieldPath, &e.Context,
		); err != nil {
			return nil, fmt.Errorf("store: scan pii log row: %w", err)
		}
		results = append(results, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get pii log iteration: %w", err)
	}
	return results, nil
}

// ListPIILog returns a page of PII log entries ordered by timestamp
// descending.
func (s *Store) ListPIILog(limit, offset int) ([]*PIILogEntry, error) {
	rows, err := s.reader.Query(`
		SELECT id, request_id, timestamp, pii_type, action, field_path, context
		FROM pii_log
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list pii log: %w", err)
	}
	defer rows.Close()

	var results []*PIILogEntry
	for rows.Next() {
		e := &PIILogEntry{}
		if err := rows.Scan(
			&e.ID, &e.RequestID, &e.Timestamp, &e.PIIType,
			&e.Action, &e.FieldPath, &e.Context,
		); err != nil {
			return nil, fmt.Errorf("store: scan pii log row: %w", err)
		}
		results = append(results, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list pii log iteration: %w", err)
	}
	return results, nil
}
