// Package store provides the data layer for the tools.agentops plugin.
// AccountStore handles CRUD operations on AI provider accounts, persisted to
// globaldb (~/.orchestra/db/global.db).
package store

import (
	"fmt"
	"math/rand"

	"github.com/orchestra-mcp/sdk-go/globaldb"
)

// Account represents an AI provider account with budget tracking.
// This is a thin wrapper around globaldb.Account for backward compatibility.
type Account = globaldb.Account

// AccountStore provides thread-safe CRUD operations on accounts via globaldb.
// All mutations go through the global SQLite database — no JSON files, no
// markdown export. Accounts contain secrets and must NOT be synced via git.
type AccountStore struct{}

// NewAccountStore creates a new AccountStore. The global database is lazily
// initialized on first use.
func NewAccountStore() (*AccountStore, error) {
	// Migrate existing JSON accounts into globaldb on first use.
	globaldb.MigrateAccountsJSON()
	return &AccountStore{}, nil
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

// Create adds a new account. Returns an error if the ID already exists.
func (s *AccountStore) Create(acct *Account) error {
	if _, err := globaldb.GetAccount(acct.ID); err == nil {
		return fmt.Errorf("account %q already exists", acct.ID)
	}
	return globaldb.CreateAccount(acct)
}

// Get returns a single account by ID.
func (s *AccountStore) Get(id string) (*Account, error) {
	return globaldb.GetAccount(id)
}

// List returns all accounts.
func (s *AccountStore) List() ([]*Account, error) {
	return globaldb.ListAccounts()
}

// Update modifies an existing account. The provided function receives the
// current account and should mutate it in place.
func (s *AccountStore) Update(id string, fn func(acct *Account)) error {
	return globaldb.UpdateAccount(id, fn)
}

// Delete removes an account by ID.
func (s *AccountStore) Delete(id string) error {
	return globaldb.DeleteAccount(id)
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
