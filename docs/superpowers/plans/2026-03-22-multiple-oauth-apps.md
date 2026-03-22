# Multiple OAuth Apps Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support multiple named OAuth client secrets so accounts across different Google Workspace orgs can each use their org's OAuth app.

**Architecture:** Backward-compatible config extension with named `[oauth.apps.<name>]` sections. Each Gmail account binds to an OAuth app name (or the default) via a new `sources.oauth_app` column. At sync time, a lazy manager cache resolves the correct `oauth.Manager` per account.

**Tech Stack:** Go, TOML config (BurntSushi/toml), SQLite (mattn/go-sqlite3), Cobra CLI

**Spec:** `docs/superpowers/specs/2026-03-22-multiple-oauth-apps-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/config/config.go:92-95` | Expand `OAuthConfig` struct, add `OAuthApp` type, `ClientSecretsFor`, `HasAnyConfig`, path normalization in `Load()` |
| Modify | `internal/config/config_test.go` | Tests for new config methods and TOML parsing |
| Modify | `internal/store/store.go:232-243` | Add `oauth_app` migration |
| Modify | `internal/store/sync.go:74-101,256-268,270-306,318-358,360-367,380-398` | Add `OAuthApp` to `Source` struct, update `scanSource`, all queries |
| Modify | `internal/store/sources.go:14-21` | Update `GetSourcesByIdentifier` query |
| Create | `internal/store/sources_oauthapp.go` | `UpdateSourceOAuthApp` and `GetSourceOAuthApp` methods |
| Modify | `internal/store/store_test.go` | Test `oauth_app` column round-trips |
| Modify | `internal/oauth/oauth.go:112-143` | Update `PrintHeadlessInstructions` signature to accept oauth app name |
| Modify | `cmd/msgvault/cmd/root.go:80-103` | Update `oauthSetupHint`, `errOAuthNotConfigured` for multi-app |
| Modify | `cmd/msgvault/cmd/addaccount.go` | Add `--oauth-app` flag, binding change logic |
| Modify | `cmd/msgvault/cmd/syncfull.go:69-83,120-133` | Lazy manager cache with app name |
| Modify | `cmd/msgvault/cmd/sync.go:71-84,127-131` | Lazy manager cache with app name |
| Modify | `cmd/msgvault/cmd/serve.go:62-65,114-123,272-284` | Resolver function, source lookup, fallback |
| Modify | `cmd/msgvault/cmd/verify.go:39-55` | Deferred OAuth check, source lookup for app name |
| Modify | `cmd/msgvault/cmd/deletions.go:312,384,406,672` | Resolve `client_secrets` path per account |

---

### Task 1: Config — OAuthConfig struct expansion and helpers

**Files:**
- Modify: `internal/config/config.go:92-95`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests for `ClientSecretsFor` and `HasAnyConfig`**

Add to `internal/config/config_test.go`:

```go
func TestOAuthConfig_ClientSecretsFor(t *testing.T) {
	tests := []struct {
		name      string
		config    OAuthConfig
		appName   string
		want      string
		wantErr   bool
	}{
		{
			name:    "empty name returns default",
			config:  OAuthConfig{ClientSecrets: "/path/to/default.json"},
			appName: "",
			want:    "/path/to/default.json",
		},
		{
			name:    "empty name with no default returns error",
			config:  OAuthConfig{},
			appName: "",
			wantErr: true,
		},
		{
			name: "named app returns its path",
			config: OAuthConfig{
				ClientSecrets: "/path/to/default.json",
				Apps: map[string]OAuthApp{
					"acme": {ClientSecrets: "/path/to/acme.json"},
				},
			},
			appName: "acme",
			want:    "/path/to/acme.json",
		},
		{
			name: "named app not found returns error",
			config: OAuthConfig{
				ClientSecrets: "/path/to/default.json",
				Apps: map[string]OAuthApp{
					"acme": {ClientSecrets: "/path/to/acme.json"},
				},
			},
			appName: "missing",
			wantErr: true,
		},
		{
			name: "named app with empty path returns error",
			config: OAuthConfig{
				Apps: map[string]OAuthApp{
					"acme": {ClientSecrets: ""},
				},
			},
			appName: "acme",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.config.ClientSecretsFor(tt.appName)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ClientSecretsFor(%q) error = nil, want error", tt.appName)
				}
				return
			}
			if err != nil {
				t.Errorf("ClientSecretsFor(%q) error = %v", tt.appName, err)
				return
			}
			if got != tt.want {
				t.Errorf("ClientSecretsFor(%q) = %q, want %q", tt.appName, got, tt.want)
			}
		})
	}
}

func TestOAuthConfig_HasAnyConfig(t *testing.T) {
	tests := []struct {
		name   string
		config OAuthConfig
		want   bool
	}{
		{
			name:   "empty config",
			config: OAuthConfig{},
			want:   false,
		},
		{
			name:   "default only",
			config: OAuthConfig{ClientSecrets: "/path/to/default.json"},
			want:   true,
		},
		{
			name: "named app only",
			config: OAuthConfig{
				Apps: map[string]OAuthApp{
					"acme": {ClientSecrets: "/path/to/acme.json"},
				},
			},
			want: true,
		},
		{
			name: "named app with empty path",
			config: OAuthConfig{
				Apps: map[string]OAuthApp{
					"acme": {ClientSecrets: ""},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.HasAnyConfig()
			if got != tt.want {
				t.Errorf("HasAnyConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test ./internal/config/ -run 'TestOAuthConfig' -v`
Expected: FAIL — `ClientSecretsFor` and `HasAnyConfig` not defined

- [ ] **Step 3: Implement `OAuthApp` type, `ClientSecretsFor`, `HasAnyConfig`**

In `internal/config/config.go`, replace the `OAuthConfig` struct (lines 92-95):

```go
// OAuthApp holds configuration for a named OAuth application.
type OAuthApp struct {
	ClientSecrets string `toml:"client_secrets"`
}

// OAuthConfig holds OAuth configuration.
type OAuthConfig struct {
	ClientSecrets string              `toml:"client_secrets"`
	Apps          map[string]OAuthApp `toml:"apps"`
}

// ClientSecretsFor returns the client secrets path for the given app name.
// Empty name returns the default. Non-empty name looks up Apps[name].
func (o *OAuthConfig) ClientSecretsFor(name string) (string, error) {
	if name == "" {
		if o.ClientSecrets == "" {
			return "", fmt.Errorf("OAuth client secrets not configured.\n\n" +
				"Set [oauth] client_secrets in config.toml, or use --oauth-app <name>")
		}
		return o.ClientSecrets, nil
	}
	app, ok := o.Apps[name]
	if !ok {
		return "", fmt.Errorf("OAuth app %q not configured. Add it to config.toml:\n\n"+
			"  [oauth.apps.%s]\n"+
			"  client_secrets = \"/path/to/client_secret.json\"", name, name)
	}
	if app.ClientSecrets == "" {
		return "", fmt.Errorf("OAuth app %q has no client_secrets path configured", name)
	}
	return app.ClientSecrets, nil
}

// HasAnyConfig returns true if any OAuth configuration exists
// (default or named apps).
func (o *OAuthConfig) HasAnyConfig() bool {
	if o.ClientSecrets != "" {
		return true
	}
	for _, app := range o.Apps {
		if app.ClientSecrets != "" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test ./internal/config/ -run 'TestOAuthConfig' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add OAuthConfig.ClientSecretsFor and HasAnyConfig helpers"
```

---

### Task 2: Config — TOML parsing and path normalization for named apps

**Files:**
- Modify: `internal/config/config.go:194-203` (Load function)
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write failing test for TOML parsing with named apps**

Add to `internal/config/config_test.go`:

```go
func TestLoadWithNamedOAuthApps(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MSGVAULT_HOME", tmpDir)

	configContent := `
[oauth]
client_secrets = "~/secrets/default.json"

[oauth.apps.acme]
client_secrets = "~/secrets/acme.json"

[oauth.apps.personal]
client_secrets = "/absolute/personal.json"
`
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	// Default should be expanded
	expectedDefault := filepath.Join(home, "secrets/default.json")
	if cfg.OAuth.ClientSecrets != expectedDefault {
		t.Errorf("ClientSecrets = %q, want %q", cfg.OAuth.ClientSecrets, expectedDefault)
	}

	// Named apps should be expanded
	expectedAcme := filepath.Join(home, "secrets/acme.json")
	acme, ok := cfg.OAuth.Apps["acme"]
	if !ok {
		t.Fatal("Apps[acme] not found")
	}
	if acme.ClientSecrets != expectedAcme {
		t.Errorf("Apps[acme].ClientSecrets = %q, want %q", acme.ClientSecrets, expectedAcme)
	}

	// Absolute paths should be unchanged
	personal, ok := cfg.OAuth.Apps["personal"]
	if !ok {
		t.Fatal("Apps[personal] not found")
	}
	if personal.ClientSecrets != "/absolute/personal.json" {
		t.Errorf("Apps[personal].ClientSecrets = %q, want /absolute/personal.json", personal.ClientSecrets)
	}

	// HasAnyConfig should be true
	if !cfg.OAuth.HasAnyConfig() {
		t.Error("HasAnyConfig() = false, want true")
	}
}

func TestLoadWithNamedOAuthApps_RelativePaths(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `
[oauth.apps.acme]
client_secrets = "secrets/acme.json"
`
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	// Use explicit --config so relative paths resolve against config dir
	cfg, err := Load(configPath, "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	expectedAcme := filepath.Join(tmpDir, "secrets/acme.json")
	acme, ok := cfg.OAuth.Apps["acme"]
	if !ok {
		t.Fatal("Apps[acme] not found")
	}
	if acme.ClientSecrets != expectedAcme {
		t.Errorf("Apps[acme].ClientSecrets = %q, want %q", acme.ClientSecrets, expectedAcme)
	}
}

func TestLoadNamedAppsOnly_NoDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MSGVAULT_HOME", tmpDir)

	configContent := `
[oauth.apps.acme]
client_secrets = "/path/to/acme.json"
`
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Default should be empty
	if cfg.OAuth.ClientSecrets != "" {
		t.Errorf("ClientSecrets = %q, want empty", cfg.OAuth.ClientSecrets)
	}

	// HasAnyConfig should still be true
	if !cfg.OAuth.HasAnyConfig() {
		t.Error("HasAnyConfig() = false, want true")
	}

	// ClientSecretsFor("") should fail
	_, err = cfg.OAuth.ClientSecretsFor("")
	if err == nil {
		t.Error("ClientSecretsFor(\"\") should error with no default")
	}

	// ClientSecretsFor("acme") should work
	path, err := cfg.OAuth.ClientSecretsFor("acme")
	if err != nil {
		t.Errorf("ClientSecretsFor(acme) error = %v", err)
	}
	if path != "/path/to/acme.json" {
		t.Errorf("ClientSecretsFor(acme) = %q, want /path/to/acme.json", path)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test ./internal/config/ -run 'TestLoadWithNamedOAuthApps|TestLoadNamedAppsOnly' -v`
Expected: FAIL — named apps not parsed, path normalization missing

- [ ] **Step 3: Add path normalization for named apps in `Load()`**

In `internal/config/config.go`, after line 196 (`cfg.OAuth.ClientSecrets = expandPath(...)`) add normalization for apps:

```go
	// Expand ~ in paths
	cfg.Data.DataDir = expandPath(cfg.Data.DataDir)
	cfg.OAuth.ClientSecrets = expandPath(cfg.OAuth.ClientSecrets)
	for name, app := range cfg.OAuth.Apps {
		app.ClientSecrets = expandPath(app.ClientSecrets)
		cfg.OAuth.Apps[name] = app
	}

	// When --config is used, resolve relative paths against the config file's
	// directory so behavior doesn't depend on the working directory.
	if explicit {
		cfg.Data.DataDir = resolveRelative(cfg.Data.DataDir, cfg.HomeDir)
		cfg.OAuth.ClientSecrets = resolveRelative(cfg.OAuth.ClientSecrets, cfg.HomeDir)
		for name, app := range cfg.OAuth.Apps {
			app.ClientSecrets = resolveRelative(app.ClientSecrets, cfg.HomeDir)
			cfg.OAuth.Apps[name] = app
		}
	}
```

This replaces the existing lines 194-203.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test ./internal/config/ -run 'TestLoadWithNamedOAuthApps|TestLoadNamedAppsOnly' -v`
Expected: PASS

- [ ] **Step 5: Run full config test suite**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test ./internal/config/ -v`
Expected: All tests PASS (no regressions)

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: parse named OAuth apps from config, normalize paths"
```

---

### Task 3: Store — schema migration and Source struct update

**Files:**
- Modify: `internal/store/store.go:232-237`
- Modify: `internal/store/sync.go:74-101,256-268,270-306,318-358,360-367,380-398`
- Modify: `internal/store/sources.go:14-21`
- Create: `internal/store/sources_oauthapp.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write failing tests for `oauth_app` column**

Add to `internal/store/store_test.go`:

```go
func TestStore_OAuthAppColumn(t *testing.T) {
	f := testutil.NewFixture(t)

	// Default source should have null oauth_app
	if f.Source.OAuthApp.Valid {
		t.Errorf("new source OAuthApp should be null, got %q", f.Source.OAuthApp.String)
	}

	// Update oauth_app
	err := f.Store.UpdateSourceOAuthApp(f.Source.ID, sql.NullString{String: "acme", Valid: true})
	testutil.MustNoErr(t, err, "UpdateSourceOAuthApp")

	// Read it back via ListSources
	sources, err := f.Store.ListSources("")
	testutil.MustNoErr(t, err, "ListSources")

	found := false
	for _, src := range sources {
		if src.ID == f.Source.ID {
			found = true
			if !src.OAuthApp.Valid || src.OAuthApp.String != "acme" {
				t.Errorf("OAuthApp = %v, want {acme, true}", src.OAuthApp)
			}
		}
	}
	if !found {
		t.Error("source not found in ListSources")
	}
}

func TestStore_OAuthAppColumn_NullRoundTrip(t *testing.T) {
	f := testutil.NewFixture(t)

	// Set to acme
	err := f.Store.UpdateSourceOAuthApp(f.Source.ID, sql.NullString{String: "acme", Valid: true})
	testutil.MustNoErr(t, err, "UpdateSourceOAuthApp(acme)")

	// Set back to null
	err = f.Store.UpdateSourceOAuthApp(f.Source.ID, sql.NullString{})
	testutil.MustNoErr(t, err, "UpdateSourceOAuthApp(null)")

	// Verify via GetSourcesByIdentifier
	sources, err := f.Store.GetSourcesByIdentifier(f.Source.Identifier)
	testutil.MustNoErr(t, err, "GetSourcesByIdentifier")

	if len(sources) == 0 {
		t.Fatal("no sources found")
	}
	if sources[0].OAuthApp.Valid {
		t.Errorf("OAuthApp should be null after clearing, got %q", sources[0].OAuthApp.String)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test -tags fts5 ./internal/store/ -run 'TestStore_OAuthApp' -v`
Expected: FAIL — `UpdateSourceOAuthApp` not defined, `OAuthApp` field not on `Source`

- [ ] **Step 3: Add migration in `store.go`**

In `internal/store/store.go`, add to the migrations slice (after line 237):

```go
		{`ALTER TABLE sources ADD COLUMN oauth_app TEXT`, "oauth_app"},
```

- [ ] **Step 4: Add `OAuthApp` field to `Source` struct and update `scanSource`**

In `internal/store/sync.go`, add field to `Source` struct (after line 265, `SyncConfig`):

```go
	OAuthApp   sql.NullString // named OAuth app binding (NULL = default)
```

Update `scanSource` (line 78-81) to include `oauth_app`:

```go
	err := sc.Scan(
		&source.ID, &source.SourceType, &source.Identifier, &source.DisplayName,
		&source.GoogleUserID, &lastSyncAt, &source.SyncCursor, &source.SyncConfig,
		&source.OAuthApp, &createdAt, &updatedAt,
	)
```

- [ ] **Step 5: Update all source queries to include `oauth_app`**

Every SQL query that selects from `sources` and uses `scanSource` must add `oauth_app` to the column list. There are 6 queries:

1. `GetOrCreateSource` in `sync.go:273-278` — add `oauth_app` after `sync_config`
2. `ListSources` (filtered) in `sync.go:325-331` — add `oauth_app` after `sync_config`
3. `ListSources` (unfiltered) in `sync.go:333-338` — add `oauth_app` after `sync_config`
4. `GetSourceByIdentifier` in `sync.go:382-387` — add `oauth_app` after `sync_config`
5. `GetSourcesByIdentifier` in `sources.go:14-21` — add `oauth_app` after `sync_config`
6. `GetLatestSyncRun` in `sync.go` (line ~385) — verify this doesn't scan sources (it scans `sync_runs`, so no change needed)

Each query's SELECT list changes from:
```sql
SELECT id, source_type, identifier, display_name, google_user_id,
       last_sync_at, sync_cursor, sync_config, created_at, updated_at
```
to:
```sql
SELECT id, source_type, identifier, display_name, google_user_id,
       last_sync_at, sync_cursor, sync_config, oauth_app,
       created_at, updated_at
```

- [ ] **Step 6: Create `UpdateSourceOAuthApp` method**

Create `internal/store/sources_oauthapp.go`:

```go
package store

import "database/sql"

// UpdateSourceOAuthApp updates the OAuth app binding for a source.
// Pass a null NullString to clear the binding (use default app).
func (s *Store) UpdateSourceOAuthApp(sourceID int64, oauthApp sql.NullString) error {
	_, err := s.db.Exec(`
		UPDATE sources
		SET oauth_app = ?, updated_at = datetime('now')
		WHERE id = ?
	`, oauthApp, sourceID)
	return err
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test -tags fts5 ./internal/store/ -run 'TestStore_OAuthApp' -v`
Expected: PASS

- [ ] **Step 8: Run full store test suite**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test -tags fts5 ./internal/store/ -v`
Expected: All tests PASS (no regressions — existing tests must work with the new column)

- [ ] **Step 9: Commit**

```bash
git add internal/store/store.go internal/store/sync.go internal/store/sources.go internal/store/sources_oauthapp.go internal/store/store_test.go
git commit -m "feat: add oauth_app column to sources table"
```

---

### Task 4: OAuth — update `PrintHeadlessInstructions` for named apps

**Files:**
- Modify: `internal/oauth/oauth.go:112-143`
- Modify: `cmd/msgvault/cmd/addaccount.go:46` (caller)

- [ ] **Step 1: Update `PrintHeadlessInstructions` signature**

In `internal/oauth/oauth.go`, change the function signature (line 115) from:

```go
func PrintHeadlessInstructions(email, tokensDir string) {
```

to:

```go
func PrintHeadlessInstructions(email, tokensDir, oauthApp string) {
```

Update the printed commands to include `--oauth-app` when `oauthApp` is non-empty. Replace the two `msgvault add-account` lines (lines 129 and 138):

```go
	addCmd := fmt.Sprintf("    msgvault add-account %s", email)
	if oauthApp != "" {
		addCmd += fmt.Sprintf(" --oauth-app %s", oauthApp)
	}
```

Then use `addCmd` in both Step 1 and Step 3 print statements instead of the hardcoded strings.

- [ ] **Step 2: Update caller in `addaccount.go`**

In `cmd/msgvault/cmd/addaccount.go` line 46, update the call from:

```go
			oauth.PrintHeadlessInstructions(email, cfg.TokensDir())
```

to:

```go
			oauth.PrintHeadlessInstructions(email, cfg.TokensDir(), oauthAppName)
```

(The `oauthAppName` variable will be wired up in Task 5.)

- [ ] **Step 3: Verify it compiles**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go build ./...`

Note: This will fail until Task 5 adds the `oauthAppName` variable. That's expected — we'll complete the wiring in the next task and commit both together.

- [ ] **Step 4: Commit (combined with Task 5)**

Deferred to Task 5.

---

### Task 5: CLI — `add-account --oauth-app` flag and binding change logic

**Files:**
- Modify: `cmd/msgvault/cmd/addaccount.go`
- Modify: `cmd/msgvault/cmd/root.go:80-103`

- [ ] **Step 1: Add `--oauth-app` flag and update `root.go` helpers**

In `cmd/msgvault/cmd/addaccount.go`, add the flag variable (near line 14):

```go
var oauthAppName string
```

Register the flag in `init()` (after line 170):

```go
	addAccountCmd.Flags().StringVar(&oauthAppName, "oauth-app", "", "Named OAuth app from config (for Google Workspace orgs)")
```

- [ ] **Step 2: Rewrite `add-account` RunE with binding change logic**

Replace the existing `RunE` function body. The new flow:

1. Resolve which client secrets to use (`--oauth-app` flag or default).
2. Open DB, look up existing source.
3. If source exists and `--oauth-app` changes the binding, delete token and re-auth.
4. If source exists and binding is unchanged, check `HasToken` to short-circuit.
5. If `--force`, delete token regardless.
6. Authorize and save.

```go
	RunE: func(cmd *cobra.Command, args []string) error {
		email := args[0]

		if headless && forceReauth {
			return fmt.Errorf("--headless and --force cannot be used together: --force requires browser-based OAuth which is not available in headless mode")
		}

		// For --headless, just show instructions (no OAuth config needed)
		if headless {
			oauth.PrintHeadlessInstructions(email, cfg.TokensDir(), oauthAppName)
			return nil
		}

		// Resolve which client secrets to use
		resolvedApp := oauthAppName
		var clientSecretsPath string

		// Initialize database (in case it's new)
		dbPath := cfg.DatabaseDSN()
		s, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer s.Close()

		if err := s.InitSchema(); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}

		// Look up existing source to detect binding changes
		existingSource, err := findGmailSource(s, email)
		if err != nil {
			return fmt.Errorf("look up existing source: %w", err)
		}

		// For --force without --oauth-app, inherit existing binding
		if forceReauth && resolvedApp == "" && existingSource != nil && existingSource.OAuthApp.Valid {
			resolvedApp = existingSource.OAuthApp.String
		}

		// Detect binding change
		bindingChanged := false
		if existingSource != nil && oauthAppName != "" {
			currentApp := ""
			if existingSource.OAuthApp.Valid {
				currentApp = existingSource.OAuthApp.String
			}
			if currentApp != oauthAppName {
				bindingChanged = true
			}
		}

		// Resolve client secrets path
		clientSecretsPath, err = cfg.OAuth.ClientSecretsFor(resolvedApp)
		if err != nil {
			if !cfg.OAuth.HasAnyConfig() {
				return errOAuthNotConfigured()
			}
			return err
		}

		// Create OAuth manager
		oauthMgr, err := oauth.NewManager(clientSecretsPath, cfg.TokensDir(), logger)
		if err != nil {
			return wrapOAuthError(fmt.Errorf("create oauth manager: %w", err))
		}

		// Handle binding change: delete old token and re-auth
		if bindingChanged {
			fmt.Printf("Switching OAuth app for %s to %q. Re-authorizing...\n", email, oauthAppName)
			if oauthMgr.HasToken(email) {
				if err := oauthMgr.DeleteToken(email); err != nil {
					return fmt.Errorf("delete existing token: %w", err)
				}
			}
			// Update binding in DB
			newApp := sql.NullString{String: oauthAppName, Valid: true}
			if err := s.UpdateSourceOAuthApp(existingSource.ID, newApp); err != nil {
				return fmt.Errorf("update oauth app binding: %w", err)
			}
		}

		// If --force, delete existing token so we re-authorize
		if forceReauth && !bindingChanged {
			if oauthMgr.HasToken(email) {
				fmt.Printf("Removing existing token for %s...\n", email)
				if err := oauthMgr.DeleteToken(email); err != nil {
					return fmt.Errorf("delete existing token: %w", err)
				}
			} else {
				fmt.Printf("No existing token found for %s, proceeding with authorization.\n", email)
			}
		}

		// Check if already authorized (skip if binding just changed)
		if !bindingChanged && oauthMgr.HasToken(email) {
			source, err := s.GetOrCreateSource("gmail", email)
			if err != nil {
				return fmt.Errorf("create source: %w", err)
			}
			// Set oauth_app on new source if specified
			if oauthAppName != "" && !source.OAuthApp.Valid {
				newApp := sql.NullString{String: oauthAppName, Valid: true}
				if err := s.UpdateSourceOAuthApp(source.ID, newApp); err != nil {
					return fmt.Errorf("update oauth app binding: %w", err)
				}
			}
			if accountDisplayName != "" {
				if err := s.UpdateSourceDisplayName(source.ID, accountDisplayName); err != nil {
					return fmt.Errorf("set display name: %w", err)
				}
			}
			fmt.Printf("Account %s is already authorized.\n", email)
			fmt.Println("Next step: msgvault sync-full", email)
			return nil
		}

		// Perform authorization
		fmt.Println("Starting browser authorization...")

		if err := oauthMgr.Authorize(cmd.Context(), email); err != nil {
			var mismatch *oauth.TokenMismatchError
			if errors.As(err, &mismatch) {
				existing, lookupErr := findGmailSource(s, email)
				if lookupErr != nil {
					return fmt.Errorf("authorization failed: %w (also: %v)", err, lookupErr)
				}
				if existing == nil {
					return fmt.Errorf(
						"%w\nIf %s is the primary address, re-add with:\n"+
							"  msgvault add-account %s",
						err, mismatch.Actual, mismatch.Actual,
					)
				}
			}
			return fmt.Errorf("authorization failed: %w", err)
		}

		// Create source record in database
		source, err := s.GetOrCreateSource("gmail", email)
		if err != nil {
			return fmt.Errorf("create source: %w", err)
		}

		// Set oauth_app binding
		if oauthAppName != "" {
			newApp := sql.NullString{String: oauthAppName, Valid: true}
			if err := s.UpdateSourceOAuthApp(source.ID, newApp); err != nil {
				return fmt.Errorf("update oauth app binding: %w", err)
			}
		}

		if accountDisplayName != "" {
			if err := s.UpdateSourceDisplayName(source.ID, accountDisplayName); err != nil {
				return fmt.Errorf("set display name: %w", err)
			}
		}

		fmt.Printf("\nAccount %s authorized successfully!\n", email)
		fmt.Println("You can now run: msgvault sync-full", email)

		return nil
	},
```

Add `"database/sql"` to the import list.

- [ ] **Step 3: Update `add-account` help text**

Update the `Long` description to mention `--oauth-app`:

```go
	Long: `Add a Gmail account by completing the OAuth2 authorization flow.

By default, opens a browser for authorization. Use --headless to see instructions
for authorizing on headless servers (Google does not support Gmail in device flow).

If a token already exists, the command skips authorization. Use --force to delete
the existing token and start a fresh OAuth flow.

For Google Workspace orgs that require their own OAuth app, use --oauth-app to
specify a named app from config.toml.

Examples:
  msgvault add-account you@gmail.com
  msgvault add-account you@gmail.com --headless
  msgvault add-account you@gmail.com --force
  msgvault add-account you@acme.com --oauth-app acme
  msgvault add-account you@gmail.com --display-name "Work Account"`,
```

- [ ] **Step 4: Verify it compiles and format**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go fmt ./... && go vet ./...`
Expected: Clean

- [ ] **Step 5: Commit (includes Task 4 changes)**

```bash
git add internal/oauth/oauth.go cmd/msgvault/cmd/addaccount.go cmd/msgvault/cmd/root.go
git commit -m "feat: add --oauth-app flag to add-account command

Supports named OAuth apps for Google Workspace orgs. Detects
binding changes and re-authorizes when switching apps.
Headless instructions include --oauth-app when specified."
```

---

### Task 6: Sync commands — lazy manager cache with per-account resolution

**Files:**
- Modify: `cmd/msgvault/cmd/syncfull.go:69-83,120-133`
- Modify: `cmd/msgvault/cmd/sync.go:71-84,127-131`

- [ ] **Step 1: Create shared `oauthManagerCache` helper in `root.go`**

Add to `cmd/msgvault/cmd/root.go`:

```go
// oauthManagerCache returns a resolver function that lazily creates and
// caches oauth.Manager instances keyed by app name.
func oauthManagerCache() func(appName string) (*oauth.Manager, error) {
	managers := map[string]*oauth.Manager{}
	return func(appName string) (*oauth.Manager, error) {
		if mgr, ok := managers[appName]; ok {
			return mgr, nil
		}
		secretsPath, err := cfg.OAuth.ClientSecretsFor(appName)
		if err != nil {
			return nil, err
		}
		mgr, err := oauth.NewManager(secretsPath, cfg.TokensDir(), logger)
		if err != nil {
			return nil, wrapOAuthError(fmt.Errorf("create oauth manager: %w", err))
		}
		managers[appName] = mgr
		return mgr, nil
	}
}

// sourceOAuthApp extracts the oauth app name from a Source, returning ""
// for the default app.
func sourceOAuthApp(src *store.Source) string {
	if src != nil && src.OAuthApp.Valid {
		return src.OAuthApp.String
	}
	return ""
}
```

- [ ] **Step 2: Update `syncfull.go`**

Replace the `getOAuthMgr` closure (lines 69-83) with:

```go
		getOAuthMgr := oauthManagerCache()
```

Replace the eager `cfg.OAuth.ClientSecrets == ""` guard (line 74 and line 122) with `cfg.OAuth.HasAnyConfig()`:

Where the code currently has:
```go
				if cfg.OAuth.ClientSecrets == "" {
					fmt.Printf("Skipping %s (OAuth not configured)\n", src.Identifier)
					continue
				}
```

Replace with:
```go
				if !cfg.OAuth.HasAnyConfig() {
					fmt.Printf("Skipping %s (OAuth not configured)\n", src.Identifier)
					continue
				}
```

Where callers pass `getOAuthMgr()`, change to `getOAuthMgr(sourceOAuthApp(src))`.

Where the single-email bootstrap path creates a default source (line 108):
```go
sources = []*store.Source{{SourceType: "gmail", Identifier: args[0]}}
```
This source will have a zero-value `OAuthApp` (null), which correctly falls back to the default.

Update `runFullSync` to look up the correct manager using the source's app name. Currently at line 179:
```go
if err := runFullSync(ctx, s, oauthMgr, src); err != nil {
```

The `oauthMgr` variable no longer exists as a single instance. Instead, `buildAPIClient` needs the resolver. Change `runFullSync` and `buildAPIClient` signatures to accept the resolver:

```go
func buildAPIClient(ctx context.Context, src *store.Source, getOAuthMgr func(string) (*oauth.Manager, error)) (gmail.API, error) {
```

Inside `buildAPIClient`, replace the current `oauthMgr` usage:

```go
	case "gmail", "":
		oauthMgr, err := getOAuthMgr(sourceOAuthApp(src))
		if err != nil {
			return nil, err
		}
```

And `runFullSync`:
```go
func runFullSync(ctx context.Context, s *store.Store, getOAuthMgr func(string) (*oauth.Manager, error), src *store.Source) error {
	apiClient, err := buildAPIClient(ctx, src, getOAuthMgr)
```

**Important:** In `sync.go`, the IMAP full-sync call at line 172 currently passes `nil` as the OAuth manager:
```go
if err := runFullSync(ctx, s, nil, src); err != nil {
```
With the new function signature, `nil` is valid for `func(string) (*oauth.Manager, error)` — but it would panic if accidentally called. Since the IMAP branch in `buildAPIClient` never calls `getOAuthMgr`, this is safe. However, for defensive coding, pass the actual resolver here too (which is available from the outer scope). Change this call to:
```go
if err := runFullSync(ctx, s, getOAuthMgr, src); err != nil {
```

- [ ] **Step 3: Update `sync.go`**

Same pattern: replace `getOAuthMgr` closure, replace `cfg.OAuth.ClientSecrets == ""` guards with `cfg.OAuth.HasAnyConfig()`, pass `sourceOAuthApp(src)` to the resolver.

The `runIncrementalSync` function currently takes `oauthMgr *oauth.Manager`. Change its signature:

```go
func runIncrementalSync(ctx context.Context, s *store.Store, getOAuthMgr func(string) (*oauth.Manager, error), source *store.Source) error {
```

Inside, resolve the manager:
```go
	oauthMgr, err := getOAuthMgr(sourceOAuthApp(source))
	if err != nil {
		return err
	}
```

- [ ] **Step 4: Verify it compiles and format**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go fmt ./... && go vet ./...`
Expected: Clean

- [ ] **Step 5: Run existing tests**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go test -tags fts5 ./cmd/msgvault/cmd/ -v -count=1`
Expected: All existing tests PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/msgvault/cmd/root.go cmd/msgvault/cmd/syncfull.go cmd/msgvault/cmd/sync.go
git commit -m "feat: per-account OAuth manager resolution in sync commands

Replace single-manager pattern with lazy cache keyed by app name.
Sync commands now resolve the correct OAuth credentials per account."
```

---

### Task 7: Serve command — resolver function and source lookup

**Files:**
- Modify: `cmd/msgvault/cmd/serve.go:62-65,114-123,272-284`

- [ ] **Step 1: Replace eager OAuth guard and single manager**

Replace lines 62-65 (the eager OAuth check):
```go
	if cfg.OAuth.ClientSecrets == "" {
		return errOAuthNotConfigured()
	}
```

with:
```go
	if !cfg.OAuth.HasAnyConfig() {
		return errOAuthNotConfigured()
	}
```

Replace lines 114-118 (single `oauthMgr` creation):
```go
	oauthMgr, err := oauth.NewManager(cfg.OAuth.ClientSecrets, cfg.TokensDir(), logger)
	if err != nil {
		return wrapOAuthError(fmt.Errorf("create oauth manager: %w", err))
	}
```

with:
```go
	getOAuthMgr := oauthManagerCache()
```

- [ ] **Step 2: Update `syncFunc` and `runScheduledSync`**

Change the `syncFunc` closure (lines 121-123):
```go
	syncFunc := func(ctx context.Context, email string) error {
		return runScheduledSync(ctx, email, s, getOAuthMgr)
	}
```

Update `runScheduledSync` signature (line 273):
```go
func runScheduledSync(ctx context.Context, email string, s *store.Store, getOAuthMgr func(string) (*oauth.Manager, error)) error {
```

Inside `runScheduledSync`, look up the source to get the app name, with fallback:

```go
	// Look up source to get OAuth app binding. Fall back to default
	// if no source row exists (token-first workflow).
	appName := ""
	if src, _ := findGmailSource(s, email); src != nil {
		appName = sourceOAuthApp(src)
	}

	oauthMgr, err := getOAuthMgr(appName)
	if err != nil {
		return fmt.Errorf("resolve OAuth credentials for %s: %w", email, err)
	}
```

Replace the existing `oauthMgr.TokenSource` / `oauthMgr.HasToken` calls to use this local `oauthMgr`.

- [ ] **Step 3: Verify it compiles and format**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go fmt ./... && go vet ./...`
Expected: Clean

- [ ] **Step 4: Commit**

```bash
git add cmd/msgvault/cmd/serve.go
git commit -m "feat: per-account OAuth resolution in serve command

Look up source's oauth_app binding for each scheduled sync.
Fall back to default app when no source row exists."
```

---

### Task 8: Verify and deletions commands — per-account resolution

**Files:**
- Modify: `cmd/msgvault/cmd/verify.go:39-55`
- Modify: `cmd/msgvault/cmd/deletions.go:312,384,406,672`

- [ ] **Step 1: Update `verify.go`**

Move the OAuth check below the DB open and source lookup. Currently the flow is:
1. Check `cfg.OAuth.ClientSecrets` (line 40)
2. Open DB (line 45)
3. Create manager (line 53)

New flow:
1. Open DB
2. Look up source for email to get app name
3. Resolve client secrets for that app
4. Create manager

```go
		// Open database
		dbPath := cfg.DatabaseDSN()
		s, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer s.Close()

		// Look up source to get OAuth app binding
		appName := ""
		if src, _ := findGmailSource(s, email); src != nil {
			appName = sourceOAuthApp(src)
		}

		// Resolve OAuth credentials
		clientSecretsPath, err := cfg.OAuth.ClientSecretsFor(appName)
		if err != nil {
			if !cfg.OAuth.HasAnyConfig() {
				return errOAuthNotConfigured()
			}
			return err
		}

		// Create OAuth manager and get token source
		oauthMgr, err := oauth.NewManager(clientSecretsPath, cfg.TokensDir(), logger)
		if err != nil {
			return wrapOAuthError(fmt.Errorf("create oauth manager: %w", err))
		}
```

- [ ] **Step 2: Update `deletions.go`**

The deletions command determines `account` late in the flow (line 325-346, after user confirmation) and opens the DB even later (line 349). The changes must respect this ordering:

**a)** Replace the early OAuth guard (line 312) with a `HasAnyConfig` check:
```go
		if !cfg.OAuth.HasAnyConfig() {
			return errOAuthNotConfigured()
		}
```

**b)** After the DB is opened (line 349) and `account` is determined (line 325), resolve the client secrets path. Insert after `s.InitSchema()` (around line 359):
```go
		// Resolve OAuth credentials for this account
		appName := ""
		if src, _ := findGmailSource(s, account); src != nil {
			appName = sourceOAuthApp(src)
		}
		clientSecretsPath, err := cfg.OAuth.ClientSecretsFor(appName)
		if err != nil {
			return err
		}
```

**c)** Replace all `cfg.OAuth.ClientSecrets` references in the file (lines 384, 406, 672) with `clientSecretsPath`. Note: the variable `account` (not `email`) is the correct identifier in this function.

**d)** In `promptScopeEscalation` (around line 672), the function needs access to the resolved path. Either pass `clientSecretsPath` as a parameter, or move the resolution inside the function using the same `findGmailSource` pattern (since `promptScopeEscalation` receives `email` as a parameter).

- [ ] **Step 3: Update `errOAuthNotConfigured` and `oauthSetupHint` in `root.go`**

Update `oauthSetupHint()` to mention named apps when `cfg.OAuth.Apps` has entries. After the existing hint text, add:

```go
	if cfg != nil && len(cfg.OAuth.Apps) > 0 {
		hint += "\n\nNamed OAuth apps are configured. Use --oauth-app <name> to specify one."
	}
```

- [ ] **Step 4: Verify it compiles and format**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go fmt ./... && go vet ./...`
Expected: Clean

- [ ] **Step 5: Commit**

```bash
git add cmd/msgvault/cmd/verify.go cmd/msgvault/cmd/deletions.go cmd/msgvault/cmd/root.go
git commit -m "feat: per-account OAuth resolution in verify and deletions

Both commands now look up the source's oauth_app binding before
creating an OAuth manager. Error messages mention named apps
when configured."
```

---

### Task 9: Documentation updates

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add multi-org OAuth section to README.md**

Find the existing OAuth setup section in README.md and add a subsection:

```markdown
### Multiple OAuth Apps (Google Workspace)

Some Google Workspace organizations require OAuth apps within their org.
To use multiple OAuth apps, add named apps to `config.toml`:

```toml
[oauth]
client_secrets = "/path/to/default_secret.json"   # for personal Gmail

[oauth.apps.acme]
client_secrets = "/path/to/acme_workspace_secret.json"
```

Then specify the app when adding accounts:

```bash
msgvault add-account you@acme.com --oauth-app acme
msgvault add-account personal@gmail.com              # uses default
```

To switch an existing account to a different OAuth app:

```bash
msgvault add-account you@acme.com --oauth-app acme   # re-authorizes
```
```

- [ ] **Step 2: Update CLAUDE.md config example**

Update the Configuration section to show the `[oauth.apps]` option.

- [ ] **Step 3: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: add multi-org OAuth setup documentation"
```

---

### Task 10: Full integration test

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && make test`
Expected: All tests PASS

- [ ] **Step 2: Run linter**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && make lint`
Expected: Clean

- [ ] **Step 3: Run vet**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && go vet ./...`
Expected: Clean

- [ ] **Step 4: Build**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && make build`
Expected: Binary builds successfully

- [ ] **Step 5: Smoke test CLI help**

Run: `cd /Users/wesm/.superset/worktrees/msgvault/multiple-oauth-apps && ./msgvault add-account --help`
Expected: Shows `--oauth-app` flag in help output

- [ ] **Step 6: Fix any issues found, commit fixes**
