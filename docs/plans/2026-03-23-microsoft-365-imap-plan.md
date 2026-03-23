# Microsoft 365 OAuth2 IMAP — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add Microsoft 365 IMAP support via OAuth2 XOAUTH2 SASL authentication.

**Architecture:** Separate `internal/microsoft/` OAuth2 package parallel to `internal/oauth/` (Gmail). XOAUTH2 SASL client in IMAP package. Standalone `add-o365` CLI command auto-configures IMAP settings.

**Tech Stack:** `emersion/go-sasl` (XOAUTH2 client), `emersion/go-imap/v2` (SASL authenticate), `golang.org/x/oauth2` (token management), Azure AD v2.0 endpoints.

**Key finding:** `go-sasl` does NOT have an XOAUTH2 implementation — only OAUTHBEARER (RFC 7628). Microsoft Exchange Online requires XOAUTH2 specifically. We implement the SASL client ourselves (it's trivial: one struct, two methods).

---

### Task 1: XOAUTH2 SASL Client

Implement the `sasl.Client` interface for the XOAUTH2 mechanism. This is a standalone unit with no dependencies on the rest of the codebase.

**Files:**
- Create: `internal/imap/xoauth2.go`
- Test: `internal/imap/xoauth2_test.go`

**Step 1: Write the failing test**

```go
// internal/imap/xoauth2_test.go
package imap

import "testing"

func TestXOAuth2Client_Start(t *testing.T) {
	tests := []struct {
		name     string
		username string
		token    string
		wantMech string
		wantIR   string
	}{
		{
			name:     "basic",
			username: "user@example.com",
			token:    "ya29.access-token",
			wantMech: "XOAUTH2",
			wantIR:   "user=user@example.com\x01auth=Bearer ya29.access-token\x01\x01",
		},
		{
			name:     "empty token",
			username: "user@example.com",
			token:    "",
			wantMech: "XOAUTH2",
			wantIR:   "user=user@example.com\x01auth=Bearer \x01\x01",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewXOAuth2Client(tt.username, tt.token)
			mech, ir, err := c.Start()
			if err != nil {
				t.Fatalf("Start() error: %v", err)
			}
			if mech != tt.wantMech {
				t.Errorf("mech = %q, want %q", mech, tt.wantMech)
			}
			if string(ir) != tt.wantIR {
				t.Errorf("ir = %q, want %q", string(ir), tt.wantIR)
			}
		})
	}
}

func TestXOAuth2Client_Next(t *testing.T) {
	c := NewXOAuth2Client("user@example.com", "token")
	_, err := c.Next([]byte("some challenge"))
	if err == nil {
		t.Fatal("Next() should return error (XOAUTH2 is single-step)")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/imap/ -run TestXOAuth2 -v`
Expected: FAIL — `NewXOAuth2Client` undefined

**Step 3: Write minimal implementation**

```go
// internal/imap/xoauth2.go
package imap

import (
	"fmt"

	"github.com/emersion/go-sasl"
)

// xoauth2Client implements sasl.Client for the XOAUTH2 mechanism
// used by Microsoft Exchange Online and Gmail IMAP.
//
// The initial response format is:
//
//	"user=" + username + "\x01" + "auth=Bearer " + token + "\x01\x01"
//
// See https://developers.google.com/gmail/imap/xoauth2-protocol
type xoauth2Client struct {
	username string
	token    string
}

// NewXOAuth2Client creates a SASL client for XOAUTH2 authentication.
func NewXOAuth2Client(username, token string) sasl.Client {
	return &xoauth2Client{username: username, token: token}
}

func (c *xoauth2Client) Start() (mech string, ir []byte, err error) {
	resp := "user=" + c.username + "\x01auth=Bearer " + c.token + "\x01\x01"
	return "XOAUTH2", []byte(resp), nil
}

func (c *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	return nil, fmt.Errorf("XOAUTH2: unexpected server challenge")
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/imap/ -run TestXOAuth2 -v`
Expected: PASS

**Step 5: Commit**

```
git add internal/imap/xoauth2.go internal/imap/xoauth2_test.go
git commit -m "feat: add XOAUTH2 SASL client for Microsoft 365 IMAP"
```

---

### Task 2: Add AuthMethod to IMAP Config

Extend `imap.Config` with `AuthMethod` field. Backward-compatible: missing field defaults to password.

**Files:**
- Modify: `internal/imap/config.go`
- Modify: `internal/imap/config_test.go`

**Step 1: Write the failing test**

Add to `internal/imap/config_test.go`:

```go
func TestConfigAuthMethod_DefaultsToPassword(t *testing.T) {
	// Existing JSON without auth_method should default to password
	cfg, err := ConfigFromJSON(`{"host":"imap.example.com","port":993,"tls":true,"username":"user"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthMethod != "" && cfg.AuthMethod != AuthPassword {
		t.Errorf("AuthMethod = %q, want empty or %q", cfg.AuthMethod, AuthPassword)
	}
	if cfg.EffectiveAuthMethod() != AuthPassword {
		t.Errorf("EffectiveAuthMethod() = %q, want %q", cfg.EffectiveAuthMethod(), AuthPassword)
	}
}

func TestConfigAuthMethod_XOAuth2(t *testing.T) {
	cfg, err := ConfigFromJSON(`{"host":"outlook.office365.com","port":993,"tls":true,"username":"user@company.com","auth_method":"xoauth2"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthMethod != AuthXOAuth2 {
		t.Errorf("AuthMethod = %q, want %q", cfg.AuthMethod, AuthXOAuth2)
	}
	if cfg.EffectiveAuthMethod() != AuthXOAuth2 {
		t.Errorf("EffectiveAuthMethod() = %q, want %q", cfg.EffectiveAuthMethod(), AuthXOAuth2)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/imap/ -run TestConfigAuthMethod -v`
Expected: FAIL — `AuthPassword`, `AuthXOAuth2`, `EffectiveAuthMethod` undefined

**Step 3: Write minimal implementation**

Add to `internal/imap/config.go`:

```go
// AuthMethod specifies how the IMAP client authenticates.
type AuthMethod string

const (
	// AuthPassword uses traditional LOGIN (username + password).
	AuthPassword AuthMethod = "password"
	// AuthXOAuth2 uses XOAUTH2 SASL mechanism (OAuth2 bearer token).
	AuthXOAuth2 AuthMethod = "xoauth2"
)
```

Add `AuthMethod` field to `Config` struct:

```go
type Config struct {
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	TLS        bool       `json:"tls"`
	STARTTLS   bool       `json:"starttls"`
	Username   string     `json:"username"`
	AuthMethod AuthMethod `json:"auth_method,omitempty"`
}
```

Add helper method:

```go
// EffectiveAuthMethod returns the auth method, defaulting to password
// when the field is empty (backward compatibility with existing configs).
func (c *Config) EffectiveAuthMethod() AuthMethod {
	if c.AuthMethod == "" {
		return AuthPassword
	}
	return c.AuthMethod
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/imap/ -run TestConfigAuthMethod -v`
Expected: PASS

**Step 5: Commit**

```
git add internal/imap/config.go internal/imap/config_test.go
git commit -m "feat: add AuthMethod field to IMAP config for XOAUTH2 support"
```

---

### Task 3: Add TokenSource to IMAP Client and Branch connect()

Add `WithTokenSource` option and branch `connect()` between password and XOAUTH2.

**Files:**
- Modify: `internal/imap/client.go`

**Step 1: Write the failing test**

Create `internal/imap/client_xoauth2_test.go`:

```go
package imap

import (
	"context"
	"testing"
)

func TestNewClient_WithTokenSource(t *testing.T) {
	cfg := &Config{
		Host:       "outlook.office365.com",
		Port:       993,
		TLS:        true,
		Username:   "user@company.com",
		AuthMethod: AuthXOAuth2,
	}
	called := false
	ts := func(ctx context.Context) (string, error) {
		called = true
		return "test-token", nil
	}
	c := NewClient(cfg, "", WithTokenSource(ts))
	if c.tokenSource == nil {
		t.Fatal("tokenSource should be set")
	}
	// Verify the token source is callable
	token, err := c.tokenSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "test-token" {
		t.Errorf("token = %q, want %q", token, "test-token")
	}
	if !called {
		t.Error("token source was not called")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/imap/ -run TestNewClient_WithTokenSource -v`
Expected: FAIL — `WithTokenSource`, `tokenSource` field undefined

**Step 3: Write implementation**

In `internal/imap/client.go`, add `tokenSource` field to `Client` struct:

```go
type Client struct {
	config      *Config
	password    string
	tokenSource func(ctx context.Context) (string, error) // XOAUTH2 token callback
	logger      *slog.Logger
	// ... existing fields unchanged
}
```

Add `WithTokenSource` option:

```go
// WithTokenSource sets a callback that provides OAuth2 access tokens
// for XOAUTH2 SASL authentication. Required when Config.AuthMethod is AuthXOAuth2.
func WithTokenSource(fn func(ctx context.Context) (string, error)) Option {
	return func(c *Client) { c.tokenSource = fn }
}
```

Modify `connect()` to branch on auth method (lines 89-93 of `client.go`):

Replace the existing login block:

```go
if err := conn.Login(c.config.Username, c.password).Wait(); err != nil {
	_ = conn.Close()
	return fmt.Errorf("IMAP login: %w", err)
}
```

With:

```go
switch c.config.EffectiveAuthMethod() {
case AuthXOAuth2:
	if c.tokenSource == nil {
		_ = conn.Close()
		return fmt.Errorf("XOAUTH2 auth requires a token source (use WithTokenSource)")
	}
	token, err := c.tokenSource(ctx)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("get XOAUTH2 token: %w", err)
	}
	saslClient := NewXOAuth2Client(c.config.Username, token)
	if err := conn.Authenticate(saslClient); err != nil {
		_ = conn.Close()
		return fmt.Errorf("XOAUTH2 authenticate: %w", err)
	}
default:
	if err := conn.Login(c.config.Username, c.password).Wait(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("IMAP login: %w", err)
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/imap/ -v`
Expected: PASS (all existing tests + new test)

**Step 5: Commit**

```
git add internal/imap/client.go internal/imap/client_xoauth2_test.go
git commit -m "feat: add WithTokenSource and XOAUTH2 auth branch in IMAP connect()"
```

---

### Task 4: Microsoft Config Section

Add `[microsoft]` config section to `internal/config/config.go`.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestMicrosoftConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configContent := `
[microsoft]
client_id = "test-client-id-123"
tenant_id = "my-tenant"
`
	configPath := filepath.Join(tmpDir, "config.toml")
	os.WriteFile(configPath, []byte(configContent), 0644)

	cfg, err := Load(configPath, tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Microsoft.ClientID != "test-client-id-123" {
		t.Errorf("Microsoft.ClientID = %q, want %q", cfg.Microsoft.ClientID, "test-client-id-123")
	}
	if cfg.Microsoft.TenantID != "my-tenant" {
		t.Errorf("Microsoft.TenantID = %q, want %q", cfg.Microsoft.TenantID, "my-tenant")
	}
}

func TestMicrosoftConfig_DefaultTenant(t *testing.T) {
	cfg := NewDefaultConfig()
	if cfg.Microsoft.EffectiveTenantID() != "common" {
		t.Errorf("EffectiveTenantID() = %q, want %q", cfg.Microsoft.EffectiveTenantID(), "common")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestMicrosoftConfig -v`
Expected: FAIL — `Microsoft` field undefined

**Step 3: Write implementation**

Add to `internal/config/config.go`:

```go
// MicrosoftConfig holds Microsoft 365 / Azure AD OAuth configuration.
type MicrosoftConfig struct {
	ClientID string `toml:"client_id"`
	TenantID string `toml:"tenant_id"`
}

// EffectiveTenantID returns the tenant ID, defaulting to "common"
// (multi-tenant, works for personal + org accounts).
func (c *MicrosoftConfig) EffectiveTenantID() string {
	if c.TenantID == "" {
		return "common"
	}
	return c.TenantID
}
```

Add field to `Config` struct:

```go
type Config struct {
	Data      DataConfig        `toml:"data"`
	OAuth     OAuthConfig       `toml:"oauth"`
	Microsoft MicrosoftConfig   `toml:"microsoft"`
	Sync      SyncConfig        `toml:"sync"`
	// ... rest unchanged
}
```

**Step 4: Run tests**

Run: `go test ./internal/config/ -v`
Expected: PASS

**Step 5: Commit**

```
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add [microsoft] config section for Azure AD OAuth"
```

---

### Task 5: Microsoft OAuth2 Manager

The main OAuth2 provider for Azure AD. Handles browser flow, PKCE, token validation via MS Graph, and token storage.

**Files:**
- Create: `internal/microsoft/oauth.go`
- Test: `internal/microsoft/oauth_test.go`

**Step 1: Write tests for token storage and email validation**

```go
// internal/microsoft/oauth_test.go
package microsoft

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

func TestTokenPath(t *testing.T) {
	m := &Manager{tokensDir: "/tmp/tokens"}
	path := m.TokenPath("user@example.com")
	want := "/tmp/tokens/microsoft_user@example.com.json"
	if path != want {
		t.Errorf("TokenPath = %q, want %q", path, want)
	}
}

func TestSaveAndLoadToken(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{tokensDir: dir}
	token := &oauth2.Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
	}
	scopes := []string{"IMAP.AccessAsUser.All", "offline_access"}

	if err := m.saveToken("user@example.com", token, scopes); err != nil {
		t.Fatal(err)
	}

	loaded, err := m.loadTokenFile("user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessToken != "access-123" {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, "access-123")
	}
	if loaded.RefreshToken != "refresh-456" {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, "refresh-456")
	}
	if len(loaded.Scopes) != 2 {
		t.Errorf("Scopes len = %d, want 2", len(loaded.Scopes))
	}

	// Verify file permissions
	path := m.TokenPath("user@example.com")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestHasToken(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{tokensDir: dir}

	if m.HasToken("nobody@example.com") {
		t.Error("HasToken should be false for non-existent token")
	}

	// Write a token file
	token := &oauth2.Token{AccessToken: "test"}
	if err := m.saveToken("user@example.com", token, nil); err != nil {
		t.Fatal(err)
	}
	if !m.HasToken("user@example.com") {
		t.Error("HasToken should be true after save")
	}
}

func TestDeleteToken(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{tokensDir: dir}

	token := &oauth2.Token{AccessToken: "test"}
	if err := m.saveToken("user@example.com", token, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteToken("user@example.com"); err != nil {
		t.Fatal(err)
	}
	if m.HasToken("user@example.com") {
		t.Error("HasToken should be false after delete")
	}
	// Delete non-existent should not error
	if err := m.DeleteToken("nobody@example.com"); err != nil {
		t.Errorf("DeleteToken non-existent: %v", err)
	}
}

func TestSanitizeEmail(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user@example.com", "user@example.com"},
		{"../evil", "_.._evil"},
		{"user/../../etc/passwd", "user_.._.._.._etc_passwd"},
	}
	for _, tt := range tests {
		got := sanitizeEmail(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/microsoft/ -v`
Expected: FAIL — package doesn't exist

**Step 3: Write the Microsoft OAuth manager**

```go
// internal/microsoft/oauth.go
package microsoft

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wesm/msgvault/internal/fileutil"
	"golang.org/x/oauth2"
)

const (
	// DefaultTenant allows both personal Microsoft accounts and org accounts.
	DefaultTenant = "common"

	// IMAP scope for reading mail via IMAP.
	ScopeIMAP = "https://outlook.office365.com/IMAP.AccessAsUser.All"

	redirectPort    = "8089"
	callbackPath    = "/callback/microsoft"
	graphMeEndpoint = "https://graph.microsoft.com/v1.0/me"
)

// Scopes for Microsoft OAuth2 IMAP access.
var Scopes = []string{
	ScopeIMAP,
	"offline_access",
	"openid",
	"email",
}

// TokenMismatchError is returned when the authorized Microsoft account
// does not match the expected email.
type TokenMismatchError struct {
	Expected string
	Actual   string
}

func (e *TokenMismatchError) Error() string {
	return fmt.Sprintf(
		"token mismatch: expected %s but authorized as %s",
		e.Expected, e.Actual,
	)
}

// Manager handles Microsoft OAuth2 token acquisition and storage.
type Manager struct {
	clientID   string
	tenantID   string
	tokensDir  string
	logger     *slog.Logger
	graphURL   string // override for testing

	// browserFlowFn overrides browserFlow in tests.
	browserFlowFn func(ctx context.Context, email string) (*oauth2.Token, error)
}

// NewManager creates a Microsoft OAuth manager.
func NewManager(clientID, tenantID, tokensDir string, logger *slog.Logger) *Manager {
	if tenantID == "" {
		tenantID = DefaultTenant
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		clientID:  clientID,
		tenantID:  tenantID,
		tokensDir: tokensDir,
		logger:    logger,
	}
}

func (m *Manager) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID: m.clientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:  fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/authorize", m.tenantID),
			TokenURL: fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", m.tenantID),
		},
		RedirectURL: "http://localhost:" + redirectPort + callbackPath,
		Scopes:      Scopes,
	}
}

// Authorize runs the browser OAuth flow and validates the token.
func (m *Manager) Authorize(ctx context.Context, email string) error {
	flow := m.browserFlow
	if m.browserFlowFn != nil {
		flow = m.browserFlowFn
	}
	token, err := flow(ctx, email)
	if err != nil {
		return err
	}

	if _, err := m.resolveTokenEmail(ctx, email, token); err != nil {
		return err
	}

	return m.saveToken(email, token, Scopes)
}

// TokenSource returns a function that provides fresh access tokens.
// Suitable for passing to imap.WithTokenSource.
func (m *Manager) TokenSource(ctx context.Context, email string) (func(context.Context) (string, error), error) {
	tf, err := m.loadTokenFile(email)
	if err != nil {
		return nil, fmt.Errorf("no valid token for %s: %w", email, err)
	}

	cfg := m.oauthConfig()
	ts := cfg.TokenSource(ctx, &tf.Token)

	return func(callCtx context.Context) (string, error) {
		tok, err := ts.Token()
		if err != nil {
			return "", fmt.Errorf("refresh Microsoft token: %w", err)
		}
		// Save if refreshed
		if tok.AccessToken != tf.Token.AccessToken {
			if saveErr := m.saveToken(email, tok, tf.Scopes); saveErr != nil {
				m.logger.Warn("failed to save refreshed token", "email", email, "error", saveErr)
			}
			tf.Token = *tok
		}
		return tok.AccessToken, nil
	}, nil
}

func (m *Manager) browserFlow(ctx context.Context, email string) (*oauth2.Token, error) {
	cfg := m.oauthConfig()

	// Generate PKCE verifier + challenge (required by Azure AD for public clients)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challengeHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	// Generate state for CSRF protection
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}
	state := base64.URLEncoding.EncodeToString(stateBytes)

	// Start callback server
	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			errChan <- fmt.Errorf("state mismatch: possible CSRF attack")
			fmt.Fprintf(w, "Error: state mismatch")
			return
		}
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			errChan <- fmt.Errorf("Microsoft OAuth error: %s: %s", errMsg, desc)
			fmt.Fprintf(w, "Error: %s", desc)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errChan <- fmt.Errorf("no code in callback")
			fmt.Fprintf(w, "Error: no authorization code received")
			return
		}
		codeChan <- code
		fmt.Fprintf(w, "Authorization successful! You can close this window.")
	})

	server := &http.Server{Addr: "localhost:" + redirectPort, Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- err
		}
	}()
	defer func() { _ = server.Shutdown(ctx) }()

	// Build auth URL with PKCE
	authURL := cfg.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("login_hint", email),
	)

	fmt.Printf("Opening browser for Microsoft authorization...\n")
	fmt.Printf("If browser doesn't open, visit:\n%s\n\n", authURL)
	if err := openBrowser(authURL); err != nil {
		m.logger.Warn("failed to open browser", "error", err)
	}

	// Wait for callback
	select {
	case code := <-codeChan:
		return cfg.Exchange(ctx, code,
			oauth2.SetAuthURLParam("code_verifier", verifier),
		)
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

const resolveTimeout = 10 * time.Second

// resolveTokenEmail validates the token by calling MS Graph /me.
func (m *Manager) resolveTokenEmail(
	ctx context.Context, email string, token *oauth2.Token,
) (string, error) {
	valCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	cfg := m.oauthConfig()
	ts := cfg.TokenSource(valCtx, token)
	client := oauth2.NewClient(valCtx, ts)

	graphURL := m.graphURL
	if graphURL == "" {
		graphURL = graphMeEndpoint
	}
	req, err := http.NewRequestWithContext(valCtx, "GET", graphURL, nil)
	if err != nil {
		return "", fmt.Errorf("create graph request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("verify Microsoft account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("MS Graph returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var profile struct {
		Mail                string `json:"mail"`
		UserPrincipalName   string `json:"userPrincipalName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return "", fmt.Errorf("parse MS Graph profile: %w", err)
	}

	actual := profile.Mail
	if actual == "" {
		actual = profile.UserPrincipalName
	}
	if !strings.EqualFold(actual, email) {
		return "", &TokenMismatchError{Expected: email, Actual: actual}
	}

	return actual, nil
}

// --- Token storage ---

type tokenFile struct {
	oauth2.Token
	Scopes []string `json:"scopes,omitempty"`
}

// TokenPath returns the token file path for an email.
func (m *Manager) TokenPath(email string) string {
	safe := sanitizeEmail(email)
	return filepath.Join(m.tokensDir, "microsoft_"+safe+".json")
}

func (m *Manager) saveToken(email string, token *oauth2.Token, scopes []string) error {
	if err := fileutil.SecureMkdirAll(m.tokensDir, 0700); err != nil {
		return err
	}

	tf := tokenFile{Token: *token, Scopes: scopes}
	data, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return err
	}

	path := m.TokenPath(email)
	tmpFile, err := os.CreateTemp(m.tokensDir, ".ms-token-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp token file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp token file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp token file: %w", err)
	}
	if err := fileutil.SecureChmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp token file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp token file: %w", err)
	}
	return nil
}

func (m *Manager) loadTokenFile(email string) (*tokenFile, error) {
	path := m.TokenPath(email)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tf tokenFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, err
	}
	return &tf, nil
}

// HasToken checks if a token exists for the given email.
func (m *Manager) HasToken(email string) bool {
	_, err := os.Stat(m.TokenPath(email))
	return err == nil
}

// DeleteToken removes the token file for the given email.
func (m *Manager) DeleteToken(email string) error {
	err := os.Remove(m.TokenPath(email))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func sanitizeEmail(email string) string {
	safe := strings.ReplaceAll(email, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	safe = strings.ReplaceAll(safe, "..", "_..") // prevent path traversal
	return safe
}

// openBrowser opens the default browser (same pattern as internal/oauth).
func openBrowser(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("refused to open URL with scheme %q", parsed.Scheme)
	}

	switch runtime.GOOS {
	case "darwin":
		return exec_Command("open", rawURL).Start()
	case "linux":
		return exec_Command("xdg-open", rawURL).Start()
	case "windows":
		return exec_Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
```

Note: `openBrowser` needs `import "os/exec"` and should use `exec.Command`. The `exec_Command` above is a placeholder — use `exec.Command` in the actual implementation.

**Step 4: Run tests**

Run: `go test ./internal/microsoft/ -v`
Expected: PASS

**Step 5: Commit**

```
git add internal/microsoft/oauth.go internal/microsoft/oauth_test.go
git commit -m "feat: add Microsoft OAuth2 manager for Azure AD IMAP auth"
```

---

### Task 6: add-o365 CLI Command

Standalone command that handles the entire Microsoft 365 account setup flow.

**Files:**
- Create: `cmd/msgvault/cmd/addo365.go`

**Step 1: Write the command**

```go
// cmd/msgvault/cmd/addo365.go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	imapclient "github.com/wesm/msgvault/internal/imap"
	"github.com/wesm/msgvault/internal/microsoft"
	"github.com/wesm/msgvault/internal/store"
)

var o365TenantID string

var addO365Cmd = &cobra.Command{
	Use:   "add-o365 <email>",
	Short: "Add a Microsoft 365 account via OAuth",
	Long: `Add a Microsoft 365 / Outlook.com email account using OAuth2 authentication.

This opens a browser for Microsoft authorization, then configures IMAP access
to outlook.office365.com automatically using the XOAUTH2 SASL mechanism.

Requires a [microsoft] section in config.toml with your Azure AD app's client_id.
See the docs for Azure AD app registration setup.

Examples:
  msgvault add-o365 user@outlook.com
  msgvault add-o365 user@company.com --tenant my-tenant-id`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		email := args[0]

		if cfg.Microsoft.ClientID == "" {
			return fmt.Errorf("Microsoft OAuth not configured.\n\n" +
				"Add to your config.toml:\n\n" +
				"  [microsoft]\n" +
				"  client_id = \"your-azure-app-client-id\"\n\n" +
				"See docs for Azure AD app registration setup.")
		}

		tenantID := cfg.Microsoft.EffectiveTenantID()
		if o365TenantID != "" {
			tenantID = o365TenantID
		}

		// Create Microsoft OAuth manager
		msMgr := microsoft.NewManager(
			cfg.Microsoft.ClientID,
			tenantID,
			cfg.TokensDir(),
			logger,
		)

		// Run authorization
		fmt.Printf("Authorizing %s with Microsoft...\n", email)
		if err := msMgr.Authorize(cmd.Context(), email); err != nil {
			return fmt.Errorf("authorization failed: %w", err)
		}

		// Auto-configure IMAP for outlook.office365.com
		imapCfg := &imapclient.Config{
			Host:       "outlook.office365.com",
			Port:       993,
			TLS:        true,
			Username:   email,
			AuthMethod: imapclient.AuthXOAuth2,
		}

		// Open database
		dbPath := cfg.DatabaseDSN()
		s, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer s.Close()

		if err := s.InitSchema(); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}

		// Create source record (uses IMAP identifier format)
		identifier := imapCfg.Identifier()
		source, err := s.GetOrCreateSource("imap", identifier)
		if err != nil {
			return fmt.Errorf("create source: %w", err)
		}

		cfgJSON, err := imapCfg.ToJSON()
		if err != nil {
			return fmt.Errorf("serialize config: %w", err)
		}
		if err := s.UpdateSourceSyncConfig(source.ID, cfgJSON); err != nil {
			return fmt.Errorf("store config: %w", err)
		}
		if err := s.UpdateSourceDisplayName(source.ID, email); err != nil {
			return fmt.Errorf("set display name: %w", err)
		}

		fmt.Printf("\nMicrosoft 365 account added successfully!\n")
		fmt.Printf("  Email:      %s\n", email)
		fmt.Printf("  Identifier: %s\n", identifier)
		fmt.Println()
		fmt.Println("You can now run:")
		fmt.Printf("  msgvault sync-full %s\n", identifier)

		return nil
	},
}

func init() {
	addO365Cmd.Flags().StringVar(&o365TenantID, "tenant", "",
		"Azure AD tenant ID (default: \"common\" for multi-tenant)")
	rootCmd.AddCommand(addO365Cmd)
}
```

**Step 2: Build and verify**

Run: `go build ./cmd/msgvault/ && ./msgvault add-o365 --help`
Expected: Shows help text with usage, flags, and examples.

**Step 3: Commit**

```
git add cmd/msgvault/cmd/addo365.go
git commit -m "feat: add add-o365 command for Microsoft 365 account setup"
```

---

### Task 7: Sync Routing for XOAUTH2 IMAP Sources

Update `buildAPIClient()` to handle XOAUTH2 IMAP configs by loading Microsoft tokens.

**Files:**
- Modify: `cmd/msgvault/cmd/syncfull.go` — `buildAPIClient()` function (lines 200-232)
- Modify: `cmd/msgvault/cmd/sync.go` — IMAP credential check (line 148)

**Step 1: Update buildAPIClient in syncfull.go**

Replace the existing `case "imap":` block in `buildAPIClient()` (lines 215-227):

```go
case "imap":
	if !src.SyncConfig.Valid || src.SyncConfig.String == "" {
		return nil, fmt.Errorf("IMAP source %s has no config (run 'add-imap' first)", src.Identifier)
	}
	imapCfg, err := imaplib.ConfigFromJSON(src.SyncConfig.String)
	if err != nil {
		return nil, fmt.Errorf("parse IMAP config: %w", err)
	}

	var opts []imaplib.Option
	opts = append(opts, imaplib.WithLogger(logger))

	switch imapCfg.EffectiveAuthMethod() {
	case imaplib.AuthXOAuth2:
		msMgr := microsoft.NewManager(
			cfg.Microsoft.ClientID,
			cfg.Microsoft.EffectiveTenantID(),
			cfg.TokensDir(),
			logger,
		)
		tokenFn, err := msMgr.TokenSource(ctx, imapCfg.Username)
		if err != nil {
			return nil, fmt.Errorf("load Microsoft token: %w (run 'add-o365' first)", err)
		}
		opts = append(opts, imaplib.WithTokenSource(tokenFn))
		return imaplib.NewClient(imapCfg, "", opts...), nil

	default:
		password, err := imaplib.LoadCredentials(cfg.TokensDir(), src.Identifier)
		if err != nil {
			return nil, fmt.Errorf("load IMAP credentials: %w (run 'add-imap' first)", err)
		}
		return imaplib.NewClient(imapCfg, password, opts...), nil
	}
```

This requires adding `"github.com/wesm/msgvault/internal/microsoft"` to the imports.

**Step 2: Update IMAP credential check in sync.go**

The incremental sync command (line 148 of `sync.go`) checks `imaplib.HasCredentials()` before syncing IMAP sources. For XOAUTH2 sources, it should check for a Microsoft token instead.

Replace the IMAP credential check block:

```go
case "imap":
	hasAuth := imaplib.HasCredentials(cfg.TokensDir(), src.Identifier)
	if !hasAuth && src.SyncConfig.Valid {
		// Check if this is an XOAUTH2 source with a Microsoft token
		imapCfg, parseErr := imaplib.ConfigFromJSON(src.SyncConfig.String)
		if parseErr == nil && imapCfg.EffectiveAuthMethod() == imaplib.AuthXOAuth2 {
			msMgr := microsoft.NewManager(
				cfg.Microsoft.ClientID,
				cfg.Microsoft.EffectiveTenantID(),
				cfg.TokensDir(),
				logger,
			)
			hasAuth = msMgr.HasToken(imapCfg.Username)
		}
	}
	if !hasAuth {
		fmt.Printf("Skipping %s (no credentials - run 'add-imap' or 'add-o365' first)\n", src.Identifier)
		continue
	}
	imapTargets = append(imapTargets, src)
```

Also update the same check in `syncfull.go` (around line 136):

```go
case "imap":
	hasAuth := imaplib.HasCredentials(cfg.TokensDir(), src.Identifier)
	if !hasAuth && src.SyncConfig.Valid && src.SyncConfig.String != "" {
		imapCfg, parseErr := imaplib.ConfigFromJSON(src.SyncConfig.String)
		if parseErr == nil && imapCfg.EffectiveAuthMethod() == imaplib.AuthXOAuth2 {
			msMgr := microsoft.NewManager(
				cfg.Microsoft.ClientID,
				cfg.Microsoft.EffectiveTenantID(),
				cfg.TokensDir(),
				logger,
			)
			hasAuth = msMgr.HasToken(imapCfg.Username)
		}
	}
	if !hasAuth {
		fmt.Printf("Skipping %s (no credentials - run 'add-imap' or 'add-o365' first)\n", src.Identifier)
		continue
	}
```

**Step 3: Build and verify**

Run: `go build ./cmd/msgvault/`
Expected: Compiles without errors.

**Step 4: Commit**

```
git add cmd/msgvault/cmd/syncfull.go cmd/msgvault/cmd/sync.go
git commit -m "feat: route XOAUTH2 IMAP sources through Microsoft token source"
```

---

### Task 8: Update remove-account for Microsoft Token Cleanup

When removing an IMAP source that uses XOAUTH2, also delete the Microsoft token file.

**Files:**
- Modify: `cmd/msgvault/cmd/remove_account.go` — credential cleanup (lines 121-142)

**Step 1: Update the IMAP cleanup branch**

Replace the `case "imap":` block in credential cleanup:

```go
case "imap":
	credPath := imaplib.CredentialsPath(
		cfg.TokensDir(), source.Identifier,
	)
	if err := os.Remove(credPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr,
			"Warning: could not remove credentials file %s: %v\n",
			credPath, err,
		)
	}
	// Also clean up Microsoft OAuth token if this was an XOAUTH2 source
	if source.SyncConfig.Valid && source.SyncConfig.String != "" {
		imapCfg, parseErr := imaplib.ConfigFromJSON(source.SyncConfig.String)
		if parseErr == nil && imapCfg.EffectiveAuthMethod() == imaplib.AuthXOAuth2 {
			msMgr := microsoft.NewManager(
				cfg.Microsoft.ClientID,
				cfg.Microsoft.EffectiveTenantID(),
				cfg.TokensDir(),
				logger,
			)
			if err := msMgr.DeleteToken(imapCfg.Username); err != nil {
				fmt.Fprintf(os.Stderr,
					"Warning: could not remove Microsoft token: %v\n", err,
				)
			}
		}
	}
```

**Step 2: Build and verify**

Run: `go build ./cmd/msgvault/`
Expected: Compiles without errors.

**Step 3: Commit**

```
git add cmd/msgvault/cmd/remove_account.go
git commit -m "feat: clean up Microsoft OAuth token on IMAP account removal"
```

---

### Task 9: Promote go-sasl to Direct Dependency

The `go-sasl` package is currently an indirect dependency (via `go-imap`). Since we now import it directly for the XOAUTH2 SASL client, promote it.

**Files:**
- Modify: `go.mod`

**Step 1: Run go mod tidy**

Run: `go mod tidy`

This will promote `github.com/emersion/go-sasl` from `// indirect` to a direct dependency since `internal/imap/xoauth2.go` imports it.

**Step 2: Verify**

Run: `grep go-sasl go.mod`
Expected: `github.com/emersion/go-sasl v0.0.0-...` (without `// indirect`)

**Step 3: Commit**

```
git add go.mod go.sum
git commit -m "chore: promote go-sasl to direct dependency for XOAUTH2 SASL"
```

---

### Task 10: Final Build + Test + Format

Run the full build, test suite, formatter, and linter to verify everything works.

**Step 1: Format and vet**

Run: `go fmt ./... && go vet ./...`

**Step 2: Run all tests**

Run: `make test`
Expected: All existing tests pass, new tests pass.

**Step 3: Run linter**

Run: `make lint`
Expected: No new lint errors.

**Step 4: Build the binary**

Run: `make build`
Expected: Clean build.

**Step 5: Smoke test CLI**

Run: `./msgvault add-o365 --help`
Expected: Shows help text.

Run: `./msgvault add-o365 test@example.com`
Expected: Error about `[microsoft]` config not set — confirms the validation path works.

**Step 6: Commit any formatting changes**

```
git add -A
git commit -m "chore: format and lint fixes for Microsoft 365 IMAP support"
```
