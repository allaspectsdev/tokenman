package store

import (
	"database/sql"
	"fmt"
	"time"
)

// migration represents a single schema migration step.
type migration struct {
	Version int
	SQL     string
}

// migrations is the ordered list of all migrations. Version 1 creates
// the initial schema; later versions add incremental changes.
var migrations = []migration{
	{
		Version: 1,
		SQL:     "", // handled specially: applies allSchemas
	},
	{
		Version: 2,
		SQL: `ALTER TABLE requests ADD COLUMN request_body TEXT DEFAULT '';
ALTER TABLE requests ADD COLUMN response_body TEXT DEFAULT '';`,
	},
	{
		Version: 3,
		SQL:     `ALTER TABLE requests ADD COLUMN project TEXT DEFAULT '';`,
	},
}

// Migrate brings the database up to the latest schema version.
// It uses the writer connection and wraps each migration in a transaction.
func (s *Store) Migrate() error {
	// Ensure the migrations table exists first so we can query it.
	if _, err := s.writer.Exec(schemaMigrations); err != nil {
		return fmt.Errorf("store: create migrations table: %w", err)
	}

	current, err := s.currentVersion()
	if err != nil {
		return fmt.Errorf("store: read migration version: %w", err)
	}

	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return fmt.Errorf("store: migration v%d: %w", m.Version, err)
		}
	}
	return nil
}

// currentVersion returns the highest applied migration version, or 0
// if no migrations have been applied yet.
func (s *Store) currentVersion() (int, error) {
	var version int
	err := s.writer.QueryRow("SELECT COALESCE(MAX(version), 0) FROM migrations").Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// applyMigration runs a single migration inside a transaction and
// records it in the migrations table.
func (s *Store) applyMigration(m migration) error {
	tx, err := s.writer.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	if m.Version == 1 {
		// Version 1 is the initial schema creation.
		if err := applyInitialSchema(tx); err != nil {
			return err
		}
	} else if m.SQL != "" {
		if _, err := tx.Exec(m.SQL); err != nil {
			return err
		}
	}

	_, err = tx.Exec(
		"INSERT INTO migrations (version, applied_at) VALUES (?, ?)",
		m.Version, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// applyInitialSchema executes every DDL block in allSchemas inside
// the provided transaction.
func applyInitialSchema(tx *sql.Tx) error {
	for _, ddl := range allSchemas {
		if _, err := tx.Exec(ddl); err != nil {
			return fmt.Errorf("exec schema: %w", err)
		}
	}
	return nil
}
