package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Budget represents a spending-limit record for a given billing period.
type Budget struct {
	ID          int64
	Period      string
	PeriodStart string
	AmountUSD   float64
	LimitUSD    float64
	LastUpdated string
}

// GetBudget retrieves the budget for a specific period and period_start.
// Returns sql.ErrNoRows (wrapped) if no matching budget exists.
func (s *Store) GetBudget(period, periodStart string) (*Budget, error) {
	b := &Budget{}
	err := s.reader.QueryRow(`
		SELECT id, period, period_start, amount_usd, limit_usd, last_updated
		FROM budgets
		WHERE period = ? AND period_start = ?`, period, periodStart,
	).Scan(
		&b.ID, &b.Period, &b.PeriodStart,
		&b.AmountUSD, &b.LimitUSD, &b.LastUpdated,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get budget (%s, %s): %w", period, periodStart, err)
	}
	return b, nil
}

// AddSpending increments the spending amount for a budget period. If
// the budget row does not exist yet it is created with the given limit.
// If it already exists the amount is incremented and the limit is
// updated to the provided value.
//
// Because the budgets table uses INTEGER PRIMARY KEY AUTOINCREMENT,
// there is no natural unique constraint on (period, period_start).
// We use an UPDATE-first approach: try to update an existing row, and
// only insert when no matching row is found.
func (s *Store) AddSpending(period, periodStart string, amount, limit float64) error {
	now := time.Now().UTC().Format(time.RFC3339)

	result, err := s.writer.Exec(`
		UPDATE budgets
		SET amount_usd = amount_usd + ?, limit_usd = ?, last_updated = ?
		WHERE period = ? AND period_start = ?`,
		amount, limit, now, period, periodStart,
	)
	if err != nil {
		return fmt.Errorf("store: update budget spending: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: budget rows affected: %w", err)
	}

	if n == 0 {
		_, err = s.writer.Exec(`
			INSERT INTO budgets (period, period_start, amount_usd, limit_usd, last_updated)
			VALUES (?, ?, ?, ?, ?)`,
			period, periodStart, amount, limit, now,
		)
		if err != nil {
			return fmt.Errorf("store: insert budget: %w", err)
		}
	}

	return nil
}

// ResetBudget resets the spending amount to zero for the given period
// and period_start. Returns sql.ErrNoRows (wrapped) if no matching
// budget exists.
func (s *Store) ResetBudget(period, periodStart string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.writer.Exec(`
		UPDATE budgets SET amount_usd = 0.0, last_updated = ?
		WHERE period = ? AND period_start = ?`,
		now, period, periodStart,
	)
	if err != nil {
		return fmt.Errorf("store: reset budget: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: reset budget rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: reset budget (%s, %s): %w", period, periodStart, sql.ErrNoRows)
	}
	return nil
}
