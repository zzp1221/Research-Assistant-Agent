// Package budget implements the owner-scoped cost ledger and the user budget
// reservations used by the scheduler's pre-dispatch gate.
package budget

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrBudgetExceeded means the reservation would cross the configured limit.
	ErrBudgetExceeded = errors.New("budget exceeded")
	// ErrInvalid marks caller input errors.
	ErrInvalid = errors.New("budget invalid")
)

// Entry is one settled model/task cost record.
type Entry struct {
	OwnerID    string    `json:"owner_id"`
	PlanID     string    `json:"plan_id"`
	TaskID     string    `json:"task_id,omitempty"`
	Model      string    `json:"model,omitempty"`
	Prompt     int       `json:"prompt_tokens,omitempty"`
	Completion int       `json:"completion_tokens,omitempty"`
	CostUSD    float64   `json:"cost_usd"`
	Estimated  bool      `json:"estimated,omitempty"`
	CostSource string    `json:"cost_source,omitempty"`
	At         time.Time `json:"at"`
}

// UserBudget is one owner's day/month spending limit.
type UserBudget struct {
	OwnerID   string    `json:"owner_id"`
	Period    string    `json:"period"` // day | month
	LimitUSD  float64   `json:"limit_usd"`
	Policy    string    `json:"policy"` // soft | hard
	UpdatedAt time.Time `json:"updated_at"`
}

// Alert records a budget threshold crossing for the alerts API.
type Alert struct {
	ID       string    `json:"id"`
	OwnerID  string    `json:"owner_id"`
	PlanID   string    `json:"plan_id,omitempty"`
	Kind     string    `json:"kind"`  // warning | exceeded
	Scope    string    `json:"scope"` // plan | user
	Policy   string    `json:"policy"`
	Message  string    `json:"message"`
	SpentUSD float64   `json:"spent_usd"`
	LimitUSD float64   `json:"limit_usd"`
	At       time.Time `json:"at"`
}

// Reservation is a pre-dispatch hold against the user budget.
type Reservation struct {
	ID             string    `json:"id"`
	OwnerID        string    `json:"owner_id"`
	PlanID         string    `json:"plan_id"`
	TaskID         string    `json:"task_id"`
	EstimateUSD    float64   `json:"estimate_usd"`
	EstimateTokens int       `json:"estimate_tokens"`
	CreatedAt      time.Time `json:"created_at"`
}

type persisted struct {
	Entries []Entry                `json:"entries"`
	Budgets map[string]*UserBudget `json:"budgets"`
	Alerts  []Alert                `json:"alerts"`
}

// Store is the durable budget state.
type Store struct {
	mu      sync.Mutex
	path    string
	entries []Entry
	budgets map[string]*UserBudget
	alerts  []Alert
}

func NewMemoryStore() *Store {
	return &Store{budgets: map[string]*UserBudget{}}
}

func NewFileStore(path string) (*Store, error) {
	store := &Store{path: path, budgets: map[string]*UserBudget{}}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read budget store: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	var payload persisted
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode budget store: %w", err)
	}
	s.entries = payload.Entries
	s.alerts = payload.Alerts
	if payload.Budgets != nil {
		s.budgets = payload.Budgets
	}
	return nil
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create budget store directory: %w", err)
	}
	raw, err := json.Marshal(persisted{Entries: s.entries, Budgets: s.budgets, Alerts: s.alerts})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".budget-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, s.path)
}

// Manager coordinates reservations and settled entries.
type Manager struct {
	mu           sync.Mutex
	store        *Store
	reservations map[string]Reservation
	now          func() time.Time
}

func NewManager(store *Store) *Manager {
	if store == nil {
		store = NewMemoryStore()
	}
	return &Manager{store: store, reservations: map[string]Reservation{}, now: time.Now}
}

func (m *Manager) Now() time.Time { return m.now().UTC() }

func (m *Manager) Budget(ownerID string) (UserBudget, bool) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	budget, ok := m.store.budgets[ownerID]
	if !ok || budget == nil {
		return UserBudget{}, false
	}
	return *budget, true
}

func (m *Manager) SetBudget(ownerID, period string, limitUSD float64, policy string) (UserBudget, error) {
	period = strings.ToLower(strings.TrimSpace(period))
	if period != "day" && period != "month" {
		return UserBudget{}, fmt.Errorf("%w: period must be day or month", ErrInvalid)
	}
	if limitUSD < 0 {
		return UserBudget{}, fmt.Errorf("%w: limit must be non-negative", ErrInvalid)
	}
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy != "hard" {
		policy = "soft"
	}
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	budget := &UserBudget{OwnerID: ownerID, Period: period, LimitUSD: limitUSD, Policy: policy, UpdatedAt: m.now().UTC()}
	m.store.budgets[ownerID] = budget
	if err := m.store.persistLocked(); err != nil {
		return UserBudget{}, err
	}
	return *budget, nil
}

// periodStart returns the start of the budget period containing now.
func periodStart(period string, now time.Time) time.Time {
	now = now.UTC()
	if period == "month" {
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// Spent returns the settled spend in one period.
func (m *Manager) Spent(ownerID, period string) float64 {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	start := periodStart(period, m.now())
	total := 0.0
	for _, entry := range m.store.entries {
		if entry.OwnerID == ownerID && !entry.At.Before(start) {
			total += entry.CostUSD
		}
	}
	return total
}

func (m *Manager) reservedUSD(ownerID string) float64 {
	total := 0.0
	for _, reservation := range m.reservations {
		if reservation.OwnerID == ownerID {
			total += reservation.EstimateUSD
		}
	}
	return total
}

// Reserve holds an estimated amount before task dispatch. A configured user
// budget is enforced here; without one the reservation is still recorded so
// concurrent tasks cannot oversubscribe, but it never blocks.
func (m *Manager) Reserve(ownerID, planID, taskID string, estimateUSD float64, estimateTokens int) (Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if estimateUSD < 0 {
		estimateUSD = 0
	}
	reservation := Reservation{
		ID:             fmt.Sprintf("res-%s-%s-%d", planID, taskID, m.now().UnixNano()),
		OwnerID:        ownerID,
		PlanID:         planID,
		TaskID:         taskID,
		EstimateUSD:    estimateUSD,
		EstimateTokens: estimateTokens,
		CreatedAt:      m.now().UTC(),
	}
	if budget, ok := m.Budget(ownerID); ok && budget.LimitUSD > 0 {
		spent := m.Spent(ownerID, budget.Period)
		if spent+m.reservedUSD(ownerID)+estimateUSD > budget.LimitUSD {
			return Reservation{}, fmt.Errorf("%w: user %s budget %.4f, spent %.4f, reserved %.4f, estimate %.4f", ErrBudgetExceeded, budget.Period, budget.LimitUSD, spent, m.reservedUSD(ownerID), estimateUSD)
		}
	}
	m.reservations[reservation.ID] = reservation
	return reservation, nil
}

// Settle releases a reservation and appends the actual cost entry.
func (m *Manager) Settle(reservation Reservation, actual Entry) {
	m.mu.Lock()
	delete(m.reservations, reservation.ID)
	m.mu.Unlock()
	if actual.At.IsZero() {
		actual.At = m.now().UTC()
	}
	if actual.OwnerID == "" {
		actual.OwnerID = reservation.OwnerID
	}
	if actual.PlanID == "" {
		actual.PlanID = reservation.PlanID
	}
	if actual.TaskID == "" {
		actual.TaskID = reservation.TaskID
	}
	m.store.mu.Lock()
	m.store.entries = append(m.store.entries, actual)
	_ = m.store.persistLocked()
	m.store.mu.Unlock()
}

// Record appends a settled cost entry without a prior reservation.
func (m *Manager) Record(entry Entry) {
	if entry.At.IsZero() {
		entry.At = m.now().UTC()
	}
	m.store.mu.Lock()
	m.store.entries = append(m.store.entries, entry)
	_ = m.store.persistLocked()
	m.store.mu.Unlock()
}

// HasPlanEntry reports whether the ledger already has a settled entry for one
// plan. Startup backfill uses it to avoid double-counting legacy plans.
func (m *Manager) HasPlanEntry(planID string) bool {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	for _, entry := range m.store.entries {
		if entry.PlanID == planID {
			return true
		}
	}
	return false
}

// ReleasePlan releases every outstanding reservation for one plan.
func (m *Manager) ReleasePlan(planID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, reservation := range m.reservations {
		if reservation.PlanID == planID {
			delete(m.reservations, id)
		}
	}
}

// Release removes one reservation without recording a cost.
func (m *Manager) Release(reservation Reservation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.reservations, reservation.ID)
}

// RecordAlert persists a threshold crossing.
func (m *Manager) RecordAlert(alert Alert) {
	if alert.At.IsZero() {
		alert.At = m.now().UTC()
	}
	if alert.ID == "" {
		alert.ID = fmt.Sprintf("alert-%d", m.now().UnixNano())
	}
	m.store.mu.Lock()
	m.store.alerts = append(m.store.alerts, alert)
	_ = m.store.persistLocked()
	m.store.mu.Unlock()
}

func (m *Manager) Alerts(ownerID string, limit int) []Alert {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	out := make([]Alert, 0)
	for _, alert := range m.store.alerts {
		if alert.OwnerID == ownerID {
			out = append(out, alert)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Summary aggregates today/month spend, budget and top plans/models.
type PlanCost struct {
	PlanID  string  `json:"plan_id"`
	CostUSD float64 `json:"cost_usd"`
	Calls   int     `json:"calls"`
}

type ModelCost struct {
	Model   string  `json:"model"`
	CostUSD float64 `json:"cost_usd"`
	Calls   int     `json:"calls"`
}

type Summary struct {
	OwnerID      string      `json:"owner_id"`
	Period       string      `json:"period"`
	TodayUSD     float64     `json:"today_usd"`
	MonthUSD     float64     `json:"month_usd"`
	SpentUSD     float64     `json:"spent_usd"`
	ReservedUSD  float64     `json:"reserved_usd"`
	LimitUSD     float64     `json:"limit_usd"`
	RemainingUSD float64     `json:"remaining_usd"`
	Policy       string      `json:"policy,omitempty"`
	Configured   bool        `json:"configured"`
	TopPlans     []PlanCost  `json:"top_plans"`
	TopModels    []ModelCost `json:"top_models"`
}

func (m *Manager) Summary(ownerID, period string) Summary {
	if period != "day" && period != "month" {
		period = "day"
	}
	m.mu.Lock()
	reserved := m.reservedUSD(ownerID)
	m.mu.Unlock()
	budget, configured := m.Budget(ownerID)
	spent := m.Spent(ownerID, period)
	summary := Summary{
		OwnerID:     ownerID,
		Period:      period,
		TodayUSD:    m.Spent(ownerID, "day"),
		MonthUSD:    m.Spent(ownerID, "month"),
		SpentUSD:    spent,
		ReservedUSD: reserved,
		Configured:  configured,
		TopPlans:    []PlanCost{},
		TopModels:   []ModelCost{},
	}
	if configured {
		summary.LimitUSD = budget.LimitUSD
		summary.Policy = budget.Policy
		summary.RemainingUSD = budget.LimitUSD - spent - reserved
		if summary.RemainingUSD < 0 {
			summary.RemainingUSD = 0
		}
	}
	start := periodStart(period, m.now())
	planTotals := map[string]*PlanCost{}
	modelTotals := map[string]*ModelCost{}
	m.store.mu.Lock()
	for _, entry := range m.store.entries {
		if entry.OwnerID != ownerID || entry.At.Before(start) {
			continue
		}
		plan := planTotals[entry.PlanID]
		if plan == nil {
			plan = &PlanCost{PlanID: entry.PlanID}
			planTotals[entry.PlanID] = plan
		}
		plan.CostUSD += entry.CostUSD
		plan.Calls++
		modelKey := strings.TrimSpace(entry.Model)
		if modelKey == "" {
			modelKey = "unknown"
		}
		model := modelTotals[modelKey]
		if model == nil {
			model = &ModelCost{Model: modelKey}
			modelTotals[modelKey] = model
		}
		model.CostUSD += entry.CostUSD
		model.Calls++
	}
	m.store.mu.Unlock()
	for _, plan := range planTotals {
		summary.TopPlans = append(summary.TopPlans, *plan)
	}
	for _, model := range modelTotals {
		summary.TopModels = append(summary.TopModels, *model)
	}
	sort.Slice(summary.TopPlans, func(i, j int) bool { return summary.TopPlans[i].CostUSD > summary.TopPlans[j].CostUSD })
	sort.Slice(summary.TopModels, func(i, j int) bool { return summary.TopModels[i].CostUSD > summary.TopModels[j].CostUSD })
	if len(summary.TopPlans) > 10 {
		summary.TopPlans = summary.TopPlans[:10]
	}
	if len(summary.TopModels) > 10 {
		summary.TopModels = summary.TopModels[:10]
	}
	return summary
}

// Point is one timeseries bucket.
type Point struct {
	Key              string  `json:"key"`
	CostUSD          float64 `json:"cost_usd"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	Calls            int     `json:"calls"`
}

func (m *Manager) Timeseries(ownerID string, from, to time.Time, group string) []Point {
	if group != "model" && group != "plan" {
		group = "day"
	}
	if from.IsZero() {
		from = m.now().AddDate(0, 0, -29)
	}
	if to.IsZero() {
		to = m.now()
	}
	from = from.UTC()
	to = to.UTC()
	buckets := map[string]*Point{}
	m.store.mu.Lock()
	for _, entry := range m.store.entries {
		if entry.OwnerID != ownerID || entry.At.Before(from) || entry.At.After(to) {
			continue
		}
		var key string
		switch group {
		case "model":
			key = strings.TrimSpace(entry.Model)
			if key == "" {
				key = "unknown"
			}
		case "plan":
			key = entry.PlanID
		default:
			key = entry.At.UTC().Format("2006-01-02")
		}
		bucket := buckets[key]
		if bucket == nil {
			bucket = &Point{Key: key}
			buckets[key] = bucket
		}
		bucket.CostUSD += entry.CostUSD
		bucket.PromptTokens += entry.Prompt
		bucket.CompletionTokens += entry.Completion
		bucket.Calls++
	}
	m.store.mu.Unlock()
	out := make([]Point, 0, len(buckets))
	for _, bucket := range buckets {
		out = append(out, *bucket)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
