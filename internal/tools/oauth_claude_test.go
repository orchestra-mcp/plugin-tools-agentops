package tools

import (
	"context"
	"strings"
	"testing"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal/store"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestConnectClaudeCodeAccountSchema(t *testing.T) {
	schema := ConnectClaudeCodeAccountSchema()
	if schema == nil {
		t.Fatal("schema should not be nil")
	}

	props := schema.Fields["properties"].GetStructValue()
	if props == nil {
		t.Fatal("properties should not be nil")
	}

	for _, field := range []string{"name", "code", "state"} {
		if props.Fields[field] == nil {
			t.Errorf("missing property: %s", field)
		}
	}

	required := schema.Fields["required"].GetListValue().Values
	if len(required) != 1 || required[0].GetStringValue() != "name" {
		t.Errorf("required should be [\"name\"], got %v", required)
	}
}

func TestConnectClaudeCodeAccount_Step1_StartFlow(t *testing.T) {
	s := newTestAccountStore(t)
	handler := ConnectClaudeCodeAccount(s)

	args, _ := structpb.NewStruct(map[string]any{
		"name": "Test Account",
	})

	resp, err := handler(context.Background(), &pluginv1.ToolRequest{
		ToolName:  "connect_claude_code_account",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(resp)
	if text == "" {
		t.Fatal("response should contain text")
	}

	if !strings.Contains(text, "claude.ai/oauth/authorize") {
		t.Error("response should contain authorize URL")
	}
	if !strings.Contains(text, "Step 1") {
		t.Error("response should mention Step 1")
	}
	if !strings.Contains(text, "state=") || !strings.Contains(text, "connect_claude_code_account") {
		t.Error("response should contain state and callback instructions")
	}
}

func TestConnectClaudeCodeAccount_MissingName(t *testing.T) {
	s := newTestAccountStore(t)
	handler := ConnectClaudeCodeAccount(s)

	args, _ := structpb.NewStruct(map[string]any{})

	resp, err := handler(context.Background(), &pluginv1.ToolRequest{
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Success {
		t.Error("should not succeed with missing name")
	}
	if resp.ErrorCode == "" {
		t.Error("should have error code")
	}
}

func TestConnectClaudeCodeAccount_InvalidState(t *testing.T) {
	s := newTestAccountStore(t)
	handler := ConnectClaudeCodeAccount(s)

	args, _ := structpb.NewStruct(map[string]any{
		"name":  "Test",
		"code":  "some-auth-code",
		"state": "invalid-state-that-does-not-exist",
	})

	resp, err := handler(context.Background(), &pluginv1.ToolRequest{
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Success {
		t.Error("should not succeed with invalid state")
	}
	if resp.ErrorCode == "" {
		t.Error("should have error code for invalid state")
	}
}

func TestBuildClaudeEnv_OAuth(t *testing.T) {
	acct := &store.Account{
		ID:         "ACC-TEST",
		Provider:   "claude",
		AuthMethod: "oauth",
		Config: map[string]string{
			"access_token":  "sk-ant-oat01-test-token-123",
			"refresh_token": "rt-test-refresh-456",
			"expires_at":    "2027-01-01T00:00:00Z",
		},
	}

	env := buildClaudeEnv(acct)

	if env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-test-token-123" {
		t.Errorf("expected CLAUDE_CODE_OAUTH_TOKEN to be set, got %q", env["CLAUDE_CODE_OAUTH_TOKEN"])
	}
}

func TestBuildClaudeEnv_OAuthEmpty(t *testing.T) {
	acct := &store.Account{
		ID:         "ACC-TEST",
		Provider:   "claude",
		AuthMethod: "oauth",
		Config:     map[string]string{},
	}

	env := buildClaudeEnv(acct)

	if _, ok := env["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Error("CLAUDE_CODE_OAUTH_TOKEN should not be set when access_token is empty")
	}
}

func TestBuildClaudeEnv_ConfigDir(t *testing.T) {
	acct := &store.Account{
		ID:         "ACC-TEST",
		Provider:   "claude",
		AuthMethod: "claude_code",
		Config: map[string]string{
			"config_dir": "/home/user/.config/claude-work",
		},
	}

	env := buildClaudeEnv(acct)

	if env["CLAUDE_CONFIG_DIR"] != "/home/user/.config/claude-work" {
		t.Errorf("expected CLAUDE_CONFIG_DIR to be set, got %q", env["CLAUDE_CONFIG_DIR"])
	}
}

// --- helpers ---

func newTestAccountStore(t *testing.T) *store.AccountStore {
	t.Helper()
	s, err := store.NewAccountStore()
	if err != nil {
		t.Fatalf("failed to create account store: %v", err)
	}
	return s
}

func extractText(resp *pluginv1.ToolResponse) string {
	if resp == nil || resp.Result == nil {
		return ""
	}
	if v, ok := resp.Result.Fields["text"]; ok {
		return v.GetStringValue()
	}
	return ""
}
