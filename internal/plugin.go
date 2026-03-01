// Package internal contains the core registration logic for the tools.agentops
// plugin. The AgentOpsPlugin struct wires all 8 tool handlers to the plugin
// builder with their schemas and descriptions.
package internal

import (
	"github.com/orchestra-mcp/sdk-go/plugin"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal/store"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal/tools"
)

// AgentOpsPlugin holds the shared dependencies for all tool handlers.
type AgentOpsPlugin struct {
	Store   *store.AccountStore
	Tracker *store.UsageTracker
}

// RegisterTools registers all 8 tools on the given plugin builder.
func (p *AgentOpsPlugin) RegisterTools(builder *plugin.PluginBuilder) {
	s := p.Store
	t := p.Tracker

	// --- Account Management tools (5) ---
	builder.RegisterTool("create_account",
		"Create a new AI provider account with auth config and optional budget",
		tools.CreateAccountSchema(), tools.CreateAccount(s))

	builder.RegisterTool("list_accounts",
		"List all AI accounts with usage stats and masked secrets",
		tools.ListAccountsSchema(), tools.ListAccounts(s))

	builder.RegisterTool("get_account",
		"Get detailed account information including usage and config",
		tools.GetAccountSchema(), tools.GetAccount(s))

	builder.RegisterTool("remove_account",
		"Remove an AI provider account",
		tools.RemoveAccountSchema(), tools.RemoveAccount(s))

	builder.RegisterTool("set_budget",
		"Set the budget limit and alert threshold for an account",
		tools.SetBudgetSchema(), tools.SetBudget(s))

	// --- Usage/Budget tools (3) ---
	builder.RegisterTool("get_account_env",
		"Get environment variables needed to spawn a process with the account's credentials",
		tools.GetAccountEnvSchema(), tools.GetAccountEnv(s))

	builder.RegisterTool("check_budget",
		"Check budget status for an account (ok, warning, or blocked)",
		tools.CheckBudgetSchema(), tools.CheckBudgetTool(s))

	builder.RegisterTool("report_usage",
		"Record a usage event and update account running totals",
		tools.ReportUsageSchema(), tools.ReportUsage(s, t))
}
