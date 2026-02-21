package security

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// BudgetStore is the persistence interface for budget tracking.
type BudgetStore interface {
	GetBudget(period, periodStart string) (amount, limit float64, err error)
	AddSpending(period, periodStart string, amount, limit float64) error
}

// BudgetError is returned when a budget limit is exceeded. It carries
// structured data that the HTTP handler can serialize to a JSON response.
type BudgetError struct {
	Type    string  `json:"type"`
	Message string  `json:"message"`
	Period  string  `json:"period"`
	Limit   float64 `json:"limit"`
	Spent   float64 `json:"spent"`
}

// Error implements the error interface.
func (e *BudgetError) Error() string {
	return e.Message
}

// ToJSON serializes the budget error to a JSON body suitable for an HTTP
// response.
func (e *BudgetError) ToJSON() []byte {
	body := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    e.Type,
			"message": e.Message,
			"period":  e.Period,
			"limit":   e.Limit,
			"spent":   e.Spent,
		},
	}
	b, _ := json.Marshal(body)
	return b
}

// budgetPeriod defines a budget tracking period.
type budgetPeriod struct {
	Name  string
	Limit float64
}

// BudgetMiddleware is a pipeline.Middleware that enforces spending limits
// across hourly, daily, and monthly periods.
type BudgetMiddleware struct {
	limits          []budgetPeriod
	alertThresholds []float64
	store           BudgetStore
	enabled         bool
}

// Compile-time assertion that BudgetMiddleware implements pipeline.Middleware.
var _ pipeline.Middleware = (*BudgetMiddleware)(nil)

// NewBudgetMiddleware creates a new BudgetMiddleware.
//
//   - store is the persistence backend for budget data.
//   - hourly, daily, monthly are the spending limits in USD (0 means no limit for that period).
//   - thresholds are alert percentages (e.g., []float64{0.5, 0.8, 0.95}).
//   - enabled controls whether the middleware is active.
func NewBudgetMiddleware(store BudgetStore, hourly, daily, monthly float64, thresholds []float64, enabled bool) *BudgetMiddleware {
	var limits []budgetPeriod
	if hourly > 0 {
		limits = append(limits, budgetPeriod{Name: "hourly", Limit: hourly})
	}
	if daily > 0 {
		limits = append(limits, budgetPeriod{Name: "daily", Limit: daily})
	}
	if monthly > 0 {
		limits = append(limits, budgetPeriod{Name: "monthly", Limit: monthly})
	}

	return &BudgetMiddleware{
		limits:          limits,
		alertThresholds: thresholds,
		store:           store,
		enabled:         enabled,
	}
}

// Name returns the middleware name.
func (b *BudgetMiddleware) Name() string {
	return "budget"
}

// Enabled reports whether this middleware is active.
func (b *BudgetMiddleware) Enabled() bool {
	return b.enabled
}

// ProcessRequest checks current spending against all configured limits. If any
// limit is exceeded, it returns a BudgetError that the HTTP handler should
// convert to an HTTP 429 response.
func (b *BudgetMiddleware) ProcessRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	if b.store == nil {
		return req, nil
	}

	for _, period := range b.limits {
		start := periodStart(period.Name)
		amount, _, err := b.store.GetBudget(period.Name, start)
		if err != nil {
			// If no record exists yet, spending is zero.
			amount = 0
		}

		if amount >= period.Limit {
			return nil, &BudgetError{
				Type:    "budget_exceeded",
				Message: fmt.Sprintf("%s budget limit exceeded: spent $%.4f of $%.4f", period.Name, amount, period.Limit),
				Period:  period.Name,
				Limit:   period.Limit,
				Spent:   amount,
			}
		}

		// Store alert information in metadata if approaching threshold.
		if req.Metadata == nil {
			req.Metadata = make(map[string]interface{})
		}
		for _, threshold := range b.alertThresholds {
			if period.Limit > 0 && amount/period.Limit >= threshold {
				key := fmt.Sprintf("budget_alert_%s", period.Name)
				req.Metadata[key] = map[string]interface{}{
					"threshold": threshold,
					"spent":     amount,
					"limit":     period.Limit,
					"percent":   amount / period.Limit,
				}
			}
		}
	}

	return req, nil
}

// ProcessResponse records the cost of the completed request against all
// configured budget periods.
func (b *BudgetMiddleware) ProcessResponse(ctx context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	if b.store == nil {
		return resp, nil
	}

	cost := resp.CostUSD
	if cost <= 0 {
		return resp, nil
	}

	for _, period := range b.limits {
		start := periodStart(period.Name)
		if err := b.store.AddSpending(period.Name, start, cost, period.Limit); err != nil {
			// Log but do not fail the response.
			_ = err
		}
	}

	return resp, nil
}

// periodStart returns the start of the current period as an ISO 8601 string.
//   - "hourly": current hour truncated (e.g., "2024-01-15T14:00:00Z")
//   - "daily": current day truncated (e.g., "2024-01-15T00:00:00Z")
//   - "monthly": first of the current month (e.g., "2024-01-01T00:00:00Z")
func periodStart(period string) string {
	now := time.Now().UTC()
	switch period {
	case "hourly":
		return now.Truncate(time.Hour).Format(time.RFC3339)
	case "daily":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	case "monthly":
		y, m, _ := now.Date()
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	default:
		return now.Truncate(time.Hour).Format(time.RFC3339)
	}
}
