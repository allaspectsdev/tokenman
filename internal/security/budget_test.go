package security

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// ---------------------------------------------------------------------------
// Mock BudgetStore
// ---------------------------------------------------------------------------

type budgetRecord struct {
	amount float64
	limit  float64
}

type mockBudgetStore struct {
	budgets map[string]*budgetRecord
}

func newMockBudgetStore() *mockBudgetStore {
	return &mockBudgetStore{budgets: make(map[string]*budgetRecord)}
}

func (m *mockBudgetStore) GetBudget(period, periodStart string) (float64, float64, error) {
	key := period + ":" + periodStart
	if r, ok := m.budgets[key]; ok {
		return r.amount, r.limit, nil
	}
	return 0, 0, fmt.Errorf("not found")
}

func (m *mockBudgetStore) AddSpending(period, periodStart string, amount, limit float64) error {
	key := period + ":" + periodStart
	if r, ok := m.budgets[key]; ok {
		r.amount += amount
		r.limit = limit
	} else {
		m.budgets[key] = &budgetRecord{amount: amount, limit: limit}
	}
	return nil
}

// setBudget is a helper to pre-seed a budget for testing.
func (m *mockBudgetStore) setBudget(period, periodStart string, amount, limit float64) {
	key := period + ":" + periodStart
	m.budgets[key] = &budgetRecord{amount: amount, limit: limit}
}

// ---------------------------------------------------------------------------
// Budget within limit allows request
// ---------------------------------------------------------------------------

func TestBudget_WithinLimitAllows(t *testing.T) {
	store := newMockBudgetStore()
	mw := NewBudgetMiddleware(store, 10.0, 0, 0, nil, true)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("expected request to be allowed, got error: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil request")
	}
}

func TestBudget_WithinLimitPartialSpending(t *testing.T) {
	store := newMockBudgetStore()
	mw := NewBudgetMiddleware(store, 10.0, 0, 0, nil, true)

	// Pre-seed some spending (below the limit).
	start := periodStart("hourly")
	store.setBudget("hourly", start, 5.0, 10.0)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("expected request to be allowed with partial spending, got error: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil request")
	}
}

// ---------------------------------------------------------------------------
// Budget at/over limit returns BudgetError
// ---------------------------------------------------------------------------

func TestBudget_AtLimitReturnsError(t *testing.T) {
	store := newMockBudgetStore()
	mw := NewBudgetMiddleware(store, 10.0, 0, 0, nil, true)

	start := periodStart("hourly")
	store.setBudget("hourly", start, 10.0, 10.0) // exactly at limit

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}

	_, err := mw.ProcessRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when budget is at limit")
	}

	budgetErr, ok := err.(*BudgetError)
	if !ok {
		t.Fatalf("expected *BudgetError, got %T", err)
	}
	if budgetErr.Period != "hourly" {
		t.Errorf("expected period 'hourly', got %q", budgetErr.Period)
	}
	if budgetErr.Limit != 10.0 {
		t.Errorf("expected limit 10.0, got %f", budgetErr.Limit)
	}
}

func TestBudget_OverLimitReturnsError(t *testing.T) {
	store := newMockBudgetStore()
	mw := NewBudgetMiddleware(store, 10.0, 0, 0, nil, true)

	start := periodStart("hourly")
	store.setBudget("hourly", start, 15.0, 10.0) // over limit

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}

	_, err := mw.ProcessRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when budget is over limit")
	}
	if _, ok := err.(*BudgetError); !ok {
		t.Fatalf("expected *BudgetError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// BudgetError implements error interface and has correct ToJSON
// ---------------------------------------------------------------------------

func TestBudgetError_Interface(t *testing.T) {
	be := &BudgetError{
		Type:    "budget_exceeded",
		Message: "hourly budget limit exceeded: spent $10.0000 of $10.0000",
		Period:  "hourly",
		Limit:   10.0,
		Spent:   10.0,
	}

	// Implements error interface.
	var err error = be
	if err.Error() != be.Message {
		t.Errorf("Error() = %q, want %q", err.Error(), be.Message)
	}
}

func TestBudgetError_ToJSON(t *testing.T) {
	be := &BudgetError{
		Type:    "budget_exceeded",
		Message: "daily limit exceeded",
		Period:  "daily",
		Limit:   100.0,
		Spent:   105.0,
	}

	body := be.ToJSON()

	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("ToJSON produced invalid JSON: %v", err)
	}

	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'error' object in JSON")
	}
	if errObj["type"] != "budget_exceeded" {
		t.Errorf("expected type 'budget_exceeded', got %v", errObj["type"])
	}
	if errObj["period"] != "daily" {
		t.Errorf("expected period 'daily', got %v", errObj["period"])
	}
	if errObj["limit"].(float64) != 100.0 {
		t.Errorf("expected limit 100.0, got %v", errObj["limit"])
	}
	if errObj["spent"].(float64) != 105.0 {
		t.Errorf("expected spent 105.0, got %v", errObj["spent"])
	}
}

// ---------------------------------------------------------------------------
// ProcessResponse records spending
// ---------------------------------------------------------------------------

func TestBudget_ProcessResponseRecordsSpending(t *testing.T) {
	store := newMockBudgetStore()
	mw := NewBudgetMiddleware(store, 10.0, 100.0, 0, nil, true)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}
	resp := &pipeline.Response{
		StatusCode: 200,
		CostUSD:    0.05,
	}

	_, err := mw.ProcessResponse(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}

	// Both hourly and daily budgets should have recorded the spending.
	hourlyStart := periodStart("hourly")
	amount, _, err := store.GetBudget("hourly", hourlyStart)
	if err != nil {
		t.Fatalf("GetBudget hourly: %v", err)
	}
	if amount != 0.05 {
		t.Errorf("expected hourly spending 0.05, got %f", amount)
	}

	dailyStart := periodStart("daily")
	amount, _, err = store.GetBudget("daily", dailyStart)
	if err != nil {
		t.Fatalf("GetBudget daily: %v", err)
	}
	if amount != 0.05 {
		t.Errorf("expected daily spending 0.05, got %f", amount)
	}
}

func TestBudget_ProcessResponseZeroCostNoOp(t *testing.T) {
	store := newMockBudgetStore()
	mw := NewBudgetMiddleware(store, 10.0, 0, 0, nil, true)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}
	resp := &pipeline.Response{
		StatusCode: 200,
		CostUSD:    0,
	}

	_, err := mw.ProcessResponse(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}

	// No budget records should have been created.
	if len(store.budgets) != 0 {
		t.Errorf("expected no budget records for zero cost, got %d", len(store.budgets))
	}
}

// ---------------------------------------------------------------------------
// Alert thresholds are set in metadata
// ---------------------------------------------------------------------------

func TestBudget_AlertThresholds(t *testing.T) {
	store := newMockBudgetStore()
	thresholds := []float64{0.5, 0.8}
	mw := NewBudgetMiddleware(store, 10.0, 0, 0, thresholds, true)

	// Set spending at 80% of the limit.
	start := periodStart("hourly")
	store.setBudget("hourly", start, 8.0, 10.0)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	// Both the 50% and 80% thresholds should trigger.
	alertKey := "budget_alert_hourly"
	alert, ok := out.Metadata[alertKey].(map[string]interface{})
	if !ok {
		t.Fatalf("expected alert metadata at key %q", alertKey)
	}
	if alert["threshold"].(float64) != 0.8 {
		// The last matching threshold is written.
		// Check that at least one threshold was recorded.
		if alert["threshold"].(float64) != 0.5 {
			t.Errorf("expected threshold 0.5 or 0.8, got %v", alert["threshold"])
		}
	}
	if alert["spent"].(float64) != 8.0 {
		t.Errorf("expected spent 8.0, got %v", alert["spent"])
	}
}

// ---------------------------------------------------------------------------
// Disabled middleware is no-op
// ---------------------------------------------------------------------------

func TestBudget_DisabledIsNoOp(t *testing.T) {
	store := newMockBudgetStore()
	mw := NewBudgetMiddleware(store, 10.0, 0, 0, nil, false)

	if mw.Enabled() {
		t.Error("expected disabled middleware to report Enabled() = false")
	}

	// Even though budget limits are configured, a disabled middleware should
	// not be used in the pipeline. The caller (chain) checks Enabled() first.
	// We verify the Name still works.
	if mw.Name() != "budget" {
		t.Errorf("expected name 'budget', got %q", mw.Name())
	}
}

// ---------------------------------------------------------------------------
// Multiple periods can have independent limits
// ---------------------------------------------------------------------------

func TestBudget_MultiplePeriods(t *testing.T) {
	store := newMockBudgetStore()
	// hourly=5, daily=50, monthly=500
	mw := NewBudgetMiddleware(store, 5.0, 50.0, 500.0, nil, true)

	// Set hourly spending below limit, but daily above limit.
	hourlyStart := periodStart("hourly")
	dailyStart := periodStart("daily")
	store.setBudget("hourly", hourlyStart, 3.0, 5.0) // under hourly
	store.setBudget("daily", dailyStart, 55.0, 50.0)  // over daily

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}

	_, err := mw.ProcessRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when daily budget is exceeded")
	}

	budgetErr, ok := err.(*BudgetError)
	if !ok {
		t.Fatalf("expected *BudgetError, got %T", err)
	}
	if budgetErr.Period != "daily" {
		t.Errorf("expected daily period to be exceeded, got %q", budgetErr.Period)
	}
}

func TestBudget_HourlyLimitDoesNotAffectMonthly(t *testing.T) {
	store := newMockBudgetStore()
	mw := NewBudgetMiddleware(store, 5.0, 0, 500.0, nil, true)

	// Only hourly is at limit; monthly is fine.
	hourlyStart := periodStart("hourly")
	store.setBudget("hourly", hourlyStart, 5.0, 5.0)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}

	_, err := mw.ProcessRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when hourly budget is at limit")
	}

	budgetErr := err.(*BudgetError)
	if budgetErr.Period != "hourly" {
		t.Errorf("expected hourly period to be exceeded, got %q", budgetErr.Period)
	}
}
