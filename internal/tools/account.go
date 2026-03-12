// Package tools implements the 8 MCP tool handlers for the tools.agentops
// plugin: 5 account management tools and 3 usage/budget tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal/store"
	"google.golang.org/protobuf/types/known/structpb"
)

// ToolHandler is the standard tool handler function signature.
type ToolHandler = func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error)

// ---------- Schemas ----------

// CreateAccountSchema returns the JSON Schema for the create_account tool.
func CreateAccountSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Account display name",
			},
			"provider": map[string]any{
				"type":        "string",
				"description": "AI provider (default: claude)",
				"enum":        []any{"claude", "openai", "gemini", "ollama", "grok", "perplexity", "deepseek", "qwen", "kimi"},
			},
			"auth_method": map[string]any{
				"type":        "string",
				"description": "Authentication method",
				"enum":        []any{"claude_code", "setup_token", "api_key", "oauth", "custom"},
			},
			"config": map[string]any{
				"type":        "string",
				"description": "JSON string with auth config (e.g. {\"ANTHROPIC_API_KEY\": \"sk-...\"})",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Default model name (e.g. claude-sonnet-4-20250514)",
			},
			"max_budget": map[string]any{
				"type":        "number",
				"description": "Maximum budget in USD (0 = unlimited)",
			},
		},
		"required": []any{"name", "auth_method"},
	})
	return s
}

// ListAccountsSchema returns the JSON Schema for the list_accounts tool.
func ListAccountsSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	return s
}

// GetAccountSchema returns the JSON Schema for the get_account tool.
func GetAccountSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "string",
				"description": "Account ID (e.g. ACC-XXXX)",
			},
		},
		"required": []any{"account_id"},
	})
	return s
}

// RemoveAccountSchema returns the JSON Schema for the remove_account tool.
func RemoveAccountSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "string",
				"description": "Account ID to remove",
			},
		},
		"required": []any{"account_id"},
	})
	return s
}

// SetBudgetSchema returns the JSON Schema for the set_budget tool.
func SetBudgetSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "string",
				"description": "Account ID",
			},
			"budget_usd": map[string]any{
				"type":        "number",
				"description": "Maximum budget in USD (0 = unlimited)",
			},
			"alert_at": map[string]any{
				"type":        "number",
				"description": "Alert threshold as percentage 0-100 (default 80)",
			},
		},
		"required": []any{"account_id", "budget_usd"},
	})
	return s
}

// ---------- Handlers ----------

// CreateAccount creates a new AI provider account.
func CreateAccount(s *store.AccountStore) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if err := helpers.ValidateRequired(req.Arguments, "name", "auth_method"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		name := helpers.GetString(req.Arguments, "name")
		provider := helpers.GetString(req.Arguments, "provider")
		authMethod := helpers.GetString(req.Arguments, "auth_method")
		configStr := helpers.GetString(req.Arguments, "config")
		model := helpers.GetString(req.Arguments, "model")
		maxBudget := helpers.GetFloat64(req.Arguments, "max_budget")

		// Validate provider (default: claude).
		if provider != "" {
			if err := helpers.ValidateOneOf(provider, "claude", "openai", "gemini", "ollama", "grok", "perplexity"); err != nil {
				return helpers.ErrorResult("validation_error", err.Error()), nil
			}
		}

		// Validate auth method.
		if err := helpers.ValidateOneOf(authMethod, "claude_code", "setup_token", "api_key", "oauth", "custom"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		// Parse optional config JSON.
		config := make(map[string]string)
		if configStr != "" {
			if err := json.Unmarshal([]byte(configStr), &config); err != nil {
				return helpers.ErrorResult("validation_error", fmt.Sprintf("invalid config JSON: %v", err)), nil
			}
		}

		acctID := store.NewAccountID()
		acct := &store.Account{
			ID:           acctID,
			Name:         name,
			Provider:     provider,
			AuthMethod:   authMethod,
			Config:       config,
			DefaultModel: model,
			MaxBudgetUSD: maxBudget,
		}

		if err := s.Create(acct); err != nil {
			return helpers.ErrorResult("storage_error", err.Error()), nil
		}

		md := formatAccountMD(store.MaskedCopy(acct), "Created account")
		return helpers.TextResult(md), nil
	}
}

// ListAccounts returns all accounts with usage stats and masked secrets.
func ListAccounts(s *store.AccountStore) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		accounts, err := s.List()
		if err != nil {
			return helpers.ErrorResult("storage_error", err.Error()), nil
		}

		if len(accounts) == 0 {
			return helpers.TextResult("## Accounts\n\nNo accounts configured.\n"), nil
		}

		// Sort by name for consistent output.
		sort.Slice(accounts, func(i, j int) bool {
			return accounts[i].Name < accounts[j].Name
		})

		var b strings.Builder
		fmt.Fprintf(&b, "## Accounts (%d)\n\n", len(accounts))
		fmt.Fprintf(&b, "| ID | Name | Provider | Auth Method | Model | Budget | Used | Status |\n")
		fmt.Fprintf(&b, "|----|------|----------|-------------|-------|--------|------|--------|\n")
		for _, acct := range accounts {
			budget := "unlimited"
			if acct.MaxBudgetUSD > 0 {
				budget = fmt.Sprintf("$%.2f", acct.MaxBudgetUSD)
			}
			used := fmt.Sprintf("$%.4f", acct.UsedBudgetUSD)
			model := acct.DefaultModel
			if model == "" {
				model = "-"
			}
			provider := acct.Provider
			if provider == "" {
				provider = "claude"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
				acct.ID, acct.Name, provider, acct.AuthMethod, model, budget, used, acct.Status)
		}
		return helpers.TextResult(b.String()), nil
	}
}

// GetAccount returns detailed account information.
func GetAccount(s *store.AccountStore) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if err := helpers.ValidateRequired(req.Arguments, "account_id"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		accountID := helpers.GetString(req.Arguments, "account_id")
		acct, err := s.Get(accountID)
		if err != nil {
			return helpers.ErrorResult("not_found", err.Error()), nil
		}

		md := formatAccountMD(store.MaskedCopy(acct), "Account details")
		return helpers.TextResult(md), nil
	}
}

// RemoveAccount deletes an account.
func RemoveAccount(s *store.AccountStore) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if err := helpers.ValidateRequired(req.Arguments, "account_id"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		accountID := helpers.GetString(req.Arguments, "account_id")
		if err := s.Delete(accountID); err != nil {
			return helpers.ErrorResult("not_found", err.Error()), nil
		}

		return helpers.TextResult(fmt.Sprintf("Removed account **%s**", accountID)), nil
	}
}

// SetBudget updates the budget and alert threshold for an account.
func SetBudget(s *store.AccountStore) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if err := helpers.ValidateRequired(req.Arguments, "account_id"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		accountID := helpers.GetString(req.Arguments, "account_id")
		budgetUSD := helpers.GetFloat64(req.Arguments, "budget_usd")
		alertAt := helpers.GetFloat64(req.Arguments, "alert_at")

		if alertAt <= 0 || alertAt > 100 {
			alertAt = 80
		}

		err := s.Update(accountID, func(acct *store.Account) {
			acct.MaxBudgetUSD = budgetUSD
			acct.AlertAtPct = alertAt
			// Recalculate status after budget change.
			status, _, _ := store.CheckBudget(acct)
			switch status {
			case "blocked":
				acct.Status = "over_budget"
			case "warning":
				acct.Status = "alert"
			default:
				acct.Status = "active"
			}
		})
		if err != nil {
			return helpers.ErrorResult("not_found", err.Error()), nil
		}

		budgetStr := "unlimited"
		if budgetUSD > 0 {
			budgetStr = fmt.Sprintf("$%.2f", budgetUSD)
		}
		md := fmt.Sprintf("Updated budget for **%s**\n\n- **Budget:** %s\n- **Alert at:** %.0f%%\n",
			accountID, budgetStr, alertAt)
		return helpers.TextResult(md), nil
	}
}

// ---------- Formatters ----------

// formatAccountMD formats a single account as a Markdown block.
func formatAccountMD(acct *store.Account, header string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s: %s (%s)\n\n", header, acct.Name, acct.ID)
	provider := acct.Provider
	if provider == "" {
		provider = "claude"
	}
	fmt.Fprintf(&b, "- **Provider:** %s\n", provider)
	fmt.Fprintf(&b, "- **Auth Method:** %s\n", acct.AuthMethod)
	if acct.DefaultModel != "" {
		fmt.Fprintf(&b, "- **Default Model:** %s\n", acct.DefaultModel)
	}
	fmt.Fprintf(&b, "- **Status:** %s\n", acct.Status)

	budget := "unlimited"
	if acct.MaxBudgetUSD > 0 {
		budget = fmt.Sprintf("$%.2f", acct.MaxBudgetUSD)
	}
	fmt.Fprintf(&b, "- **Budget:** %s\n", budget)
	fmt.Fprintf(&b, "- **Alert Threshold:** %.0f%%\n", acct.AlertAtPct)
	fmt.Fprintf(&b, "- **Used:** $%.4f\n", acct.UsedBudgetUSD)
	fmt.Fprintf(&b, "- **Total Tokens In:** %d\n", acct.TotalTokensIn)
	fmt.Fprintf(&b, "- **Total Tokens Out:** %d\n", acct.TotalTokensOut)
	fmt.Fprintf(&b, "- **Total Sessions:** %d\n", acct.TotalSessions)
	fmt.Fprintf(&b, "- **Created:** %s\n", acct.CreatedAt)

	if len(acct.Config) > 0 {
		fmt.Fprintf(&b, "\n**Config:**\n")
		for k, v := range acct.Config {
			fmt.Fprintf(&b, "- `%s`: `%s`\n", k, v)
		}
	}

	return b.String()
}
