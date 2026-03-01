package store

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// UsageEvent records a single usage event for an AI account.
type UsageEvent struct {
	ID         string  `json:"id"`
	AccountID  string  `json:"account_id"`
	SessionID  string  `json:"session_id,omitempty"`
	TokensIn   int64   `json:"tokens_in"`
	TokensOut  int64   `json:"tokens_out"`
	CostUSD    float64 `json:"cost_usd"`
	Model      string  `json:"model"`
	DurationMs int64   `json:"duration_ms"`
	Timestamp  string  `json:"timestamp"`
}

// UsageTracker appends usage events to a JSONL file and updates account
// running totals via the AccountStore.
type UsageTracker struct {
	store *AccountStore
	path  string // full path to usage.jsonl
}

// NewUsageTracker creates a new UsageTracker. It ensures the directory exists.
func NewUsageTracker(store *AccountStore) (*UsageTracker, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	dir := filepath.Join(home, ".orchestra", "agentops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create agentops dir: %w", err)
	}

	return &UsageTracker{
		store: store,
		path:  filepath.Join(dir, "usage.jsonl"),
	}, nil
}

// NewEventID generates a usage event ID in the format "EVT-XXXXXXXX".
func NewEventID() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return "EVT-" + string(b)
}

// Record appends a usage event to the JSONL file and updates the account's
// running totals. Returns the budget status after recording ("ok", "warning",
// "blocked").
func (t *UsageTracker) Record(event *UsageEvent) (string, error) {
	if event.ID == "" {
		event.ID = NewEventID()
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	// Append to JSONL file.
	line, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("marshal event: %w", err)
	}

	f, err := os.OpenFile(t.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open usage file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("write usage event: %w", err)
	}

	// Track unique sessions via a set check.
	sessionID := event.SessionID

	// Update account totals.
	var budgetStatus string
	err = t.store.Update(event.AccountID, func(acct *Account) {
		acct.TotalTokensIn += event.TokensIn
		acct.TotalTokensOut += event.TokensOut
		acct.UsedBudgetUSD += event.CostUSD

		if sessionID != "" {
			// Increment session count. In a more sophisticated implementation
			// we would track unique session IDs, but for now we count each
			// unique session_id report as a session increment. Since the caller
			// typically reports once per session, this is a reasonable
			// approximation.
			acct.TotalSessions++
		}

		budgetStatus = checkBudgetStatus(acct)
		acct.Status = mapBudgetToAccountStatus(budgetStatus)
	})
	if err != nil {
		return "", fmt.Errorf("update account totals: %w", err)
	}

	return budgetStatus, nil
}

// CheckBudget returns the budget status for an account: "ok", "warning", or
// "blocked", along with used and remaining USD amounts.
func CheckBudget(acct *Account) (status string, usedUSD float64, remainingUSD float64) {
	usedUSD = acct.UsedBudgetUSD
	status = checkBudgetStatus(acct)

	if acct.MaxBudgetUSD <= 0 {
		// Unlimited budget.
		remainingUSD = -1 // -1 signals unlimited
	} else {
		remainingUSD = acct.MaxBudgetUSD - acct.UsedBudgetUSD
		if remainingUSD < 0 {
			remainingUSD = 0
		}
	}
	return status, usedUSD, remainingUSD
}

// checkBudgetStatus determines the budget status for an account.
func checkBudgetStatus(acct *Account) string {
	if acct.MaxBudgetUSD <= 0 {
		return "ok" // unlimited
	}
	if acct.UsedBudgetUSD >= acct.MaxBudgetUSD {
		return "blocked"
	}
	alertThreshold := acct.MaxBudgetUSD * acct.AlertAtPct / 100.0
	if acct.UsedBudgetUSD >= alertThreshold {
		return "warning"
	}
	return "ok"
}

// mapBudgetToAccountStatus maps a budget status to the account Status field.
func mapBudgetToAccountStatus(budgetStatus string) string {
	switch budgetStatus {
	case "blocked":
		return "over_budget"
	case "warning":
		return "alert"
	default:
		return "active"
	}
}
