package tools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/plugin-tools-agentops/internal/store"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	claudeOAuthAuthorizeURL = "https://claude.ai/oauth/authorize"
	claudeOAuthTokenURL     = "https://console.anthropic.com/v1/oauth/token"
	claudeOAuthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeOAuthRedirectURI  = "https://console.anthropic.com/oauth/code/callback"
	claudeOAuthScopes       = "org:create_api_key user:profile user:inference"
)

// pendingOAuth holds PKCE state for an in-progress OAuth flow.
type pendingOAuth struct {
	Name         string
	CodeVerifier string
	State        string
	ExpiresAt    time.Time
}

// pendingOAuths stores in-progress OAuth flows keyed by state.
var pendingOAuths sync.Map

// ConnectClaudeCodeAccountSchema returns the JSON Schema for the connect_claude_code_account tool.
func ConnectClaudeCodeAccountSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Display name for the account (e.g. 'Work', 'Personal')",
			},
			"code": map[string]any{
				"type":        "string",
				"description": "Authorization code from Claude Code approval page. Omit on first call to start the OAuth flow.",
			},
			"state": map[string]any{
				"type":        "string",
				"description": "State token returned from the first call. Required when providing code.",
			},
		},
		"required": []any{"name"},
	})
	return s
}

// ConnectClaudeCodeAccount implements a 2-call OAuth flow for connecting a Claude Code account.
//
// Call 1 (no code): Generates PKCE verifier + opens browser to approve URL.
// Call 2 (with code + state): Exchanges the authorization code for tokens and creates the account.
func ConnectClaudeCodeAccount(s *store.AccountStore) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if err := helpers.ValidateRequired(req.Arguments, "name"); err != nil {
			return helpers.ErrorResult("validation_error", err.Error()), nil
		}

		name := helpers.GetString(req.Arguments, "name")
		code := helpers.GetString(req.Arguments, "code")
		state := helpers.GetString(req.Arguments, "state")

		if code != "" {
			return handleOAuthExchange(s, name, code, state)
		}
		return handleOAuthStart(name)
	}
}

// handleOAuthStart generates PKCE values, stores them, opens the browser, and returns instructions.
func handleOAuthStart(name string) (*pluginv1.ToolResponse, error) {
	// Generate code_verifier (32 random bytes, base64url-encoded).
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return helpers.ErrorResult("internal_error", fmt.Sprintf("generate verifier: %v", err)), nil
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// code_challenge = SHA256(code_verifier), base64url-encoded.
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Generate state (16 random bytes, hex-like base64url).
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return helpers.ErrorResult("internal_error", fmt.Sprintf("generate state: %v", err)), nil
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// Store pending OAuth.
	pendingOAuths.Store(state, &pendingOAuth{
		Name:         name,
		CodeVerifier: codeVerifier,
		State:        state,
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	})

	// Build authorize URL.
	params := url.Values{
		"client_id":             {claudeOAuthClientID},
		"response_type":        {"code"},
		"redirect_uri":          {claudeOAuthRedirectURI},
		"scope":                 {claudeOAuthScopes},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"code":                  {"true"},
	}
	authorizeURL := claudeOAuthAuthorizeURL + "?" + params.Encode()

	// Try to open browser.
	openBrowser(authorizeURL)

	md := "### Claude Code OAuth — Step 1\n\n"
	md += "A browser window should have opened. If not, open this URL manually:\n\n"
	md += fmt.Sprintf("[Authorize URL](%s)\n\n", authorizeURL)
	md += "**Instructions:**\n"
	md += "1. Approve the request in your browser\n"
	md += "2. You'll be redirected to a page showing an authorization code\n"
	md += "3. Copy the code and call this tool again:\n\n"
	md += "```\n"
	md += fmt.Sprintf("connect_claude_code_account(name=\"%s\", code=\"PASTE_CODE_HERE\", state=\"%s\")\n", name, state)
	md += "```\n"

	return helpers.TextResult(md), nil
}

// handleOAuthExchange exchanges the authorization code for tokens and creates an Orchestra account.
func handleOAuthExchange(s *store.AccountStore, name, code, state string) (*pluginv1.ToolResponse, error) {
	if state == "" {
		return helpers.ErrorResult("validation_error", "state is required when providing code"), nil
	}

	// Look up pending OAuth.
	val, ok := pendingOAuths.LoadAndDelete(state)
	if !ok {
		return helpers.ErrorResult("not_found", "no pending OAuth flow for this state — start a new flow by calling without code"), nil
	}
	pending := val.(*pendingOAuth)

	if time.Now().After(pending.ExpiresAt) {
		return helpers.ErrorResult("expired", "OAuth flow expired — start a new flow"), nil
	}

	// Exchange code for tokens.
	tokenResp, err := exchangeClaudeCode(code, pending.CodeVerifier, state)
	if err != nil {
		return helpers.ErrorResult("token_exchange_error", err.Error()), nil
	}

	// Calculate expiration.
	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	// Create Orchestra account.
	acctID := store.NewAccountID()
	config := map[string]string{
		"access_token":  tokenResp.AccessToken,
		"refresh_token": tokenResp.RefreshToken,
		"expires_at":    expiresAt.Format(time.RFC3339),
	}

	acct := &store.Account{
		ID:         acctID,
		Name:       name,
		Provider:   "claude",
		AuthMethod: "oauth",
		Config:     config,
	}

	if err := s.Create(acct); err != nil {
		return helpers.ErrorResult("storage_error", err.Error()), nil
	}

	md := fmt.Sprintf("### Claude Code Account Connected: %s (%s)\n\n", name, acctID)
	md += fmt.Sprintf("- **Provider:** claude\n")
	md += fmt.Sprintf("- **Auth Method:** oauth\n")
	md += fmt.Sprintf("- **Token:** %s...%s\n", tokenResp.AccessToken[:10], tokenResp.AccessToken[len(tokenResp.AccessToken)-4:])
	md += fmt.Sprintf("- **Expires:** %s\n", expiresAt.Format("2006-01-02"))
	md += fmt.Sprintf("- **Scopes:** %s\n", tokenResp.Scope)
	md += "\nYou can now use this account to spawn Claude Code sessions.\n"

	return helpers.TextResult(md), nil
}

// claudeTokenResponse is the response from Anthropic's OAuth token endpoint.
type claudeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// exchangeClaudeCode exchanges an authorization code for tokens via Anthropic's token endpoint.
func exchangeClaudeCode(code, codeVerifier, state string) (*claudeTokenResponse, error) {
	payload := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     claudeOAuthClientID,
		"code":          code,
		"redirect_uri":  claudeOAuthRedirectURI,
		"code_verifier": codeVerifier,
		"state":         state,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", claudeOAuthTokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://claude.ai/")
	req.Header.Set("Origin", "https://claude.ai")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp claudeTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access token in response")
	}

	return &tokenResp, nil
}

// openBrowser opens a URL in the user's default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}
