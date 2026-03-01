// Package store provides the data layer for the tools.agentops plugin.
// AccountStore handles CRUD operations on AI provider accounts, persisted to
// ~/.orchestra/agentops/accounts.json.
package store

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Account represents an AI provider account with budget tracking.
type Account struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Provider       string            `json:"provider"`         // claude, openai, gemini, ollama, grok, perplexity (default: claude)
	AuthMethod     string            `json:"auth_method"`      // claude_code, setup_token, api_key, custom
	Config         map[string]string `json:"config"`           // auth-specific config (tokens, keys, etc.)
	DefaultModel   string            `json:"default_model"`
	MaxBudgetUSD   float64           `json:"max_budget_usd"`   // 0 = unlimited
	AlertAtPct     float64           `json:"alert_at_pct"`     // default 80
	UsedBudgetUSD  float64           `json:"used_budget_usd"`
	TotalTokensIn  int64             `json:"total_tokens_in"`
	TotalTokensOut int64             `json:"total_tokens_out"`
	TotalSessions  int               `json:"total_sessions"`
	CreatedAt      string            `json:"created_at"`
	Status         string            `json:"status"` // active, paused, over_budget, alert
}

// AccountStore provides thread-safe CRUD operations on a JSON file containing
// accounts. All mutations are read-modify-write under a mutex.
type AccountStore struct {
	mu   sync.Mutex
	path string // full path to accounts.json
}

// NewAccountStore creates a new AccountStore. It ensures the directory exists.
func NewAccountStore() (*AccountStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	dir := filepath.Join(home, ".orchestra", "agentops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create agentops dir: %w", err)
	}

	return &AccountStore{
		path: filepath.Join(dir, "accounts.json"),
	}, nil
}

// NewAccountID generates an account ID in the format "ACC-XXXX" where XXXX is
// four random uppercase ASCII letters.
func NewAccountID() string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 4)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return "ACC-" + string(b)
}

// loadAll reads and unmarshals all accounts from disk. Returns an empty map if
// the file does not exist yet.
func (s *AccountStore) loadAll() (map[string]*Account, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*Account), nil
		}
		return nil, fmt.Errorf("read accounts file: %w", err)
	}

	var accounts map[string]*Account
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("unmarshal accounts: %w", err)
	}
	if accounts == nil {
		accounts = make(map[string]*Account)
	}
	return accounts, nil
}

// saveAll marshals and writes all accounts to disk.
func (s *AccountStore) saveAll(accounts map[string]*Account) error {
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal accounts: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write accounts file: %w", err)
	}
	return nil
}

// Create adds a new account. Returns an error if the ID already exists.
func (s *AccountStore) Create(acct *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	accounts, err := s.loadAll()
	if err != nil {
		return err
	}

	if _, exists := accounts[acct.ID]; exists {
		return fmt.Errorf("account %q already exists", acct.ID)
	}

	if acct.Provider == "" {
		acct.Provider = "claude"
	}
	if acct.CreatedAt == "" {
		acct.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if acct.Status == "" {
		acct.Status = "active"
	}
	if acct.AlertAtPct == 0 {
		acct.AlertAtPct = 80
	}
	if acct.Config == nil {
		acct.Config = make(map[string]string)
	}

	accounts[acct.ID] = acct
	return s.saveAll(accounts)
}

// Get returns a single account by ID.
func (s *AccountStore) Get(id string) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	accounts, err := s.loadAll()
	if err != nil {
		return nil, err
	}

	acct, ok := accounts[id]
	if !ok {
		return nil, fmt.Errorf("account %q not found", id)
	}
	return acct, nil
}

// List returns all accounts.
func (s *AccountStore) List() ([]*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	accounts, err := s.loadAll()
	if err != nil {
		return nil, err
	}

	result := make([]*Account, 0, len(accounts))
	for _, acct := range accounts {
		result = append(result, acct)
	}
	return result, nil
}

// Update modifies an existing account. The provided function receives the
// current account and should mutate it in place.
func (s *AccountStore) Update(id string, fn func(acct *Account)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	accounts, err := s.loadAll()
	if err != nil {
		return err
	}

	acct, ok := accounts[id]
	if !ok {
		return fmt.Errorf("account %q not found", id)
	}

	fn(acct)
	return s.saveAll(accounts)
}

// Delete removes an account by ID.
func (s *AccountStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	accounts, err := s.loadAll()
	if err != nil {
		return err
	}

	if _, ok := accounts[id]; !ok {
		return fmt.Errorf("account %q not found", id)
	}

	delete(accounts, id)
	return s.saveAll(accounts)
}

// MaskSecret masks a secret string, showing the first 7 and last 4 characters
// with "..." in between. If the string is too short (<=11 chars), it is fully
// masked as "****".
func MaskSecret(s string) string {
	if len(s) <= 11 {
		return "****"
	}
	return s[:7] + "..." + s[len(s)-4:]
}

// MaskedCopy returns a copy of the account with secrets in Config masked.
func MaskedCopy(acct *Account) *Account {
	cp := *acct
	cp.Config = make(map[string]string, len(acct.Config))
	for k, v := range acct.Config {
		cp.Config[k] = MaskSecret(v)
	}
	return &cp
}
