package tools

import (
	"context"
	"encoding/json"
	"fmt"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal/store"
	"google.golang.org/protobuf/types/known/structpb"
)

// ---------- Schemas ----------

// GetAccountEnvSchema returns the JSON Schema for the get_account_env tool.
func GetAccountEnvSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "string",
				"description": "Account ID",
			},
		},
		"required": []any{"account_id"},
	})
	return s
}

// CheckBudgetSchema returns the JSON Schema for the check_budget tool.
func CheckBudgetSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "string",
				"description": "Account ID",
			},
		},
		"required": []any{"account_id"},
	})
	return s
}

// ReportUsageSchema returns the JSON Schema for the report_usage tool.
func ReportUsageSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "string",
				"description": "Account ID",
			},
			"tokens_in": map[string]any{
				"type":        "number",
				"description": "Number of input tokens consumed",
			},
			"tokens_out": map[string]any{
				"type":        "number",
				"description": "Number of output tokens generated",
			},
			"cost_usd": map[string]any{
				"type":        "number",
				"description": "Cost in USD for this usage",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model used (e.g. claude-sonnet-4-20250514)",
			},
			"duration_ms": map[string]any{
				"type":        "number",
				"description": "Duration of the request in milliseconds",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Optional session identifier for grouping usage",
			},
		},
		"required": []any{"account_id", "tokens_in", "tokens_out", "cost_usd", "model", "duration_ms"},
	})
	return s
}

// ---------- Handlers ----------

// GetAccountEnv returns environment variables and provider info needed to spawn
// a process using the account's credentials. The response is a JSON text block
// containing {"provider": "...", "env": {...}}.
func GetAccountEnv(s *store.AccountStore) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if err := helpers.ValidateRequired(req.Arguments, "account_id"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		accountID := helpers.GetString(req.Arguments, "account_id")
		acct, err := s.Get(accountID)
		if err != nil {
			return helpers.ErrorResult("not_found", err.Error()), nil
		}

		envVars := buildEnvVars(acct)
		provider := acct.Provider
		if provider == "" {
			provider = "claude"
		}

		result := map[string]any{
			"provider": provider,
			"env":      envVars,
		}

		envJSON, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return helpers.ErrorResult("internal_error", fmt.Sprintf("marshal env vars: %v", err)), nil
		}

		return helpers.TextResult(string(envJSON)), nil
	}
}

// buildEnvVars constructs the environment variable map based on provider and
// auth method. Each provider uses different env var names for its API key.
func buildEnvVars(acct *store.Account) map[string]string {
	provider := acct.Provider
	if provider == "" {
		provider = "claude"
	}

	// Custom auth returns all config as env vars regardless of provider.
	if acct.AuthMethod == "custom" {
		envVars := make(map[string]string, len(acct.Config))
		for k, v := range acct.Config {
			envVars[k] = v
		}
		return envVars
	}

	// Provider-specific env var mapping.
	switch provider {
	case "claude":
		return buildClaudeEnv(acct)
	case "openai", "grok", "perplexity", "deepseek", "qwen", "kimi":
		return buildOpenAICompatEnv(acct, provider)
	case "gemini":
		return buildGeminiEnv(acct)
	case "ollama":
		return buildOllamaEnv(acct)
	default:
		return buildClaudeEnv(acct)
	}
}

// buildClaudeEnv returns env vars for Anthropic Claude.
func buildClaudeEnv(acct *store.Account) map[string]string {
	switch acct.AuthMethod {
	case "claude_code":
		return map[string]string{}
	case "setup_token":
		token := acct.Config["token"]
		if token == "" {
			token = acct.Config["CLAUDE_CODE_TOKEN"]
		}
		return map[string]string{
			"CLAUDE_CODE_TOKEN": token,
		}
	case "api_key":
		key := acct.Config["key"]
		if key == "" {
			key = acct.Config["ANTHROPIC_API_KEY"]
		}
		return map[string]string{
			"ANTHROPIC_API_KEY": key,
		}
	default:
		return map[string]string{}
	}
}

// buildOpenAICompatEnv returns env vars for OpenAI-compatible providers
// (OpenAI, Grok/xAI, Perplexity). These all use OPENAI_API_KEY + optional base URL.
func buildOpenAICompatEnv(acct *store.Account, provider string) map[string]string {
	key := acct.Config["key"]
	if key == "" {
		key = acct.Config["OPENAI_API_KEY"]
	}

	env := map[string]string{
		"OPENAI_API_KEY": key,
	}

	// Set base URL for non-OpenAI providers.
	baseURL := acct.Config["base_url"]
	if baseURL == "" {
		baseURL = acct.Config["OPENAI_BASE_URL"]
	}
	if baseURL == "" {
		switch provider {
		case "grok":
			baseURL = "https://api.x.ai/v1"
		case "perplexity":
			baseURL = "https://api.perplexity.ai"
		case "deepseek":
			baseURL = "https://api.deepseek.com"
		case "qwen":
			baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
		case "kimi":
			baseURL = "https://api.moonshot.cn/v1"
		}
	}
	if baseURL != "" {
		env["OPENAI_BASE_URL"] = baseURL
	}

	return env
}

// buildGeminiEnv returns env vars for Google Gemini.
func buildGeminiEnv(acct *store.Account) map[string]string {
	key := acct.Config["key"]
	if key == "" {
		key = acct.Config["GOOGLE_API_KEY"]
	}
	return map[string]string{
		"GOOGLE_API_KEY": key,
	}
}

// buildOllamaEnv returns env vars for Ollama (local LLM).
func buildOllamaEnv(acct *store.Account) map[string]string {
	host := acct.Config["host"]
	if host == "" {
		host = acct.Config["OLLAMA_HOST"]
	}
	if host == "" {
		host = "http://localhost:11434"
	}
	return map[string]string{
		"OLLAMA_HOST": host,
	}
}

// CheckBudgetTool checks the budget status for an account and returns status,
// used_usd, and remaining_usd.
func CheckBudgetTool(s *store.AccountStore) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if err := helpers.ValidateRequired(req.Arguments, "account_id"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		accountID := helpers.GetString(req.Arguments, "account_id")
		acct, err := s.Get(accountID)
		if err != nil {
			return helpers.ErrorResult("not_found", err.Error()), nil
		}

		status, usedUSD, remainingUSD := store.CheckBudget(acct)

		var b string
		remainingStr := "unlimited"
		if remainingUSD >= 0 {
			remainingStr = fmt.Sprintf("$%.4f", remainingUSD)
		}

		b = fmt.Sprintf("### Budget Check: %s (%s)\n\n", acct.Name, accountID)
		b += fmt.Sprintf("- **Status:** %s\n", status)
		b += fmt.Sprintf("- **Used:** $%.4f\n", usedUSD)
		b += fmt.Sprintf("- **Remaining:** %s\n", remainingStr)

		if acct.MaxBudgetUSD > 0 {
			b += fmt.Sprintf("- **Budget:** $%.2f\n", acct.MaxBudgetUSD)
			b += fmt.Sprintf("- **Alert At:** %.0f%%\n", acct.AlertAtPct)
			pctUsed := (usedUSD / acct.MaxBudgetUSD) * 100
			b += fmt.Sprintf("- **Percent Used:** %.1f%%\n", pctUsed)
		} else {
			b += "- **Budget:** unlimited\n"
		}

		return helpers.TextResult(b), nil
	}
}

// ReportUsage records a usage event and returns the updated budget status.
func ReportUsage(s *store.AccountStore, tracker *store.UsageTracker) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if err := helpers.ValidateRequired(req.Arguments, "account_id", "model"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		accountID := helpers.GetString(req.Arguments, "account_id")
		tokensIn := int64(helpers.GetFloat64(req.Arguments, "tokens_in"))
		tokensOut := int64(helpers.GetFloat64(req.Arguments, "tokens_out"))
		costUSD := helpers.GetFloat64(req.Arguments, "cost_usd")
		model := helpers.GetString(req.Arguments, "model")
		durationMs := int64(helpers.GetFloat64(req.Arguments, "duration_ms"))
		sessionID := helpers.GetString(req.Arguments, "session_id")

		// Verify account exists before recording.
		_, err := s.Get(accountID)
		if err != nil {
			return helpers.ErrorResult("not_found", err.Error()), nil
		}

		event := &store.UsageEvent{
			AccountID:  accountID,
			SessionID:  sessionID,
			TokensIn:   tokensIn,
			TokensOut:  tokensOut,
			CostUSD:    costUSD,
			Model:      model,
			DurationMs: durationMs,
		}

		budgetStatus, err := tracker.Record(event)
		if err != nil {
			return helpers.ErrorResult("storage_error", err.Error()), nil
		}

		md := fmt.Sprintf("### Usage Recorded: %s\n\n", event.ID)
		md += fmt.Sprintf("- **Account:** %s\n", accountID)
		md += fmt.Sprintf("- **Model:** %s\n", model)
		md += fmt.Sprintf("- **Tokens In:** %d\n", tokensIn)
		md += fmt.Sprintf("- **Tokens Out:** %d\n", tokensOut)
		md += fmt.Sprintf("- **Cost:** $%.6f\n", costUSD)
		md += fmt.Sprintf("- **Duration:** %dms\n", durationMs)
		if sessionID != "" {
			md += fmt.Sprintf("- **Session:** %s\n", sessionID)
		}
		md += fmt.Sprintf("- **Budget Status:** %s\n", budgetStatus)

		return helpers.TextResult(md), nil
	}
}
