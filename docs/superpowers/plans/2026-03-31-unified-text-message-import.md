# Unified Text Message Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge WhatsApp, iMessage, and Google Voice import into a coherent system with shared phone-based participants, proper schema usage, and a dedicated TUI Texts mode.

**Architecture:** Five sequential phases: (1) shared store/utility foundation, (2) importer refactoring to use store methods directly, (3) Parquet cache extension + TextEngine query interface, (4) TUI Texts mode, (5) CLI command renaming. Each phase builds on the previous.

**Tech Stack:** Go, SQLite (mattn/go-sqlite3), DuckDB (go-duckdb), Bubble Tea TUI, Parquet/Arrow

**Spec:** `docs/superpowers/specs/2026-03-31-unified-text-message-import-design.md`

---

## Phase 1: Foundation — Shared Utilities & Store Methods

### Task 1: NormalizePhone Utility

**Files:**
- Create: `internal/textimport/phone.go`
- Create: `internal/textimport/phone_test.go`

- [ ] **Step 1: Write tests for NormalizePhone**

```go
// internal/textimport/phone_test.go
package textimport

import "testing"

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		// Valid E.164
		{"+15551234567", "+15551234567", false},
		// Strip formatting
		{"+1 (555) 123-4567", "+15551234567", false},
		{"+1-555-123-4567", "+15551234567", false},
		{"1-555-123-4567", "+15551234567", false},
		// International
		{"+447700900000", "+447700900000", false},
		{"+44 7700 900000", "+447700900000", false},
		// No country code — assume US
		{"5551234567", "+15551234567", false},
		{"(555) 123-4567", "+15551234567", false},
		// Email — not a phone
		{"alice@icloud.com", "", true},
		// Short code
		{"12345", "", true},
		// Empty
		{"", "", true},
		// System identifier
		{"status@broadcast", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizePhone(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NormalizePhone(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("NormalizePhone(%q) error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("NormalizePhone(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/textimport/ -run TestNormalizePhone -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement NormalizePhone**

```go
// internal/textimport/phone.go
package textimport

import (
	"fmt"
	"strings"
	"unicode"
)

// NormalizePhone normalizes a phone number to E.164 format.
// Returns an error for inputs that are not phone numbers (emails,
// short codes, system identifiers).
func NormalizePhone(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty input")
	}
	// Reject email addresses
	if strings.Contains(raw, "@") {
		return "", fmt.Errorf("not a phone number: %q", raw)
	}

	// Strip all non-digit and non-plus characters
	var b strings.Builder
	for _, r := range raw {
		if r == '+' || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	digits := b.String()

	// Must start with + or be all digits
	if digits == "" {
		return "", fmt.Errorf("no digits in input: %q", raw)
	}

	// Strip leading + for length check
	justDigits := strings.TrimPrefix(digits, "+")
	if len(justDigits) < 7 {
		return "", fmt.Errorf("too short for phone number: %q", raw)
	}

	// Ensure + prefix
	if !strings.HasPrefix(digits, "+") {
		// Assume US country code if 10 digits
		if len(justDigits) == 10 {
			digits = "+1" + justDigits
		} else if len(justDigits) == 11 && justDigits[0] == '1' {
			digits = "+" + justDigits
		} else {
			digits = "+" + justDigits
		}
	}

	return digits, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/textimport/ -run TestNormalizePhone -v`
Expected: PASS

- [ ] **Step 5: Run fmt/vet, commit**

```bash
go fmt ./internal/textimport/...
go vet ./internal/textimport/...
git add internal/textimport/
git commit -m "Add shared NormalizePhone utility for text importers"
```

### Task 2: Generalize EnsureParticipantByPhone

**Files:**
- Modify: `internal/store/messages.go:910-960` (EnsureParticipantByPhone)
- Modify: `internal/whatsapp/importer.go` (callers)
- Create: `internal/store/messages_test.go` (test for new signature)

The current `EnsureParticipantByPhone` hardcodes `identifier_type = 'whatsapp'` in its `participant_identifiers` INSERT. Generalize to accept `identifierType` as a parameter.

- [ ] **Step 1: Write test for generalized EnsureParticipantByPhone**

```go
// Add to internal/store/messages_test.go (create if needed)
func TestEnsureParticipantByPhone_IdentifierType(t *testing.T) {
	s := setupTestStore(t)
	defer func() { _ = s.Close() }()

	// Create participant via WhatsApp
	id1, err := s.EnsureParticipantByPhone("+15551234567", "Alice", "whatsapp")
	if err != nil {
		t.Fatal(err)
	}

	// Same phone via iMessage — should return same participant
	id2, err := s.EnsureParticipantByPhone("+15551234567", "Alice", "imessage")
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("same phone different source got different IDs: %d vs %d", id1, id2)
	}

	// Check both identifiers exist
	var count int
	err = s.DB().QueryRow(
		"SELECT COUNT(*) FROM participant_identifiers WHERE participant_id = ?", id1,
	).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 identifier rows, got %d", count)
	}
}
```

This test needs a `setupTestStore` helper — use an in-memory SQLite DB with `InitSchema()`. Check if one already exists in the test file; if not, add:

```go
func setupTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	return s
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test -tags fts5 ./internal/store/ -run TestEnsureParticipantByPhone_IdentifierType -v`
Expected: FAIL — wrong number of arguments

- [ ] **Step 3: Update EnsureParticipantByPhone signature**

In `internal/store/messages.go:910`, change:

```go
func (s *Store) EnsureParticipantByPhone(phone, displayName string) (int64, error) {
```
to:
```go
func (s *Store) EnsureParticipantByPhone(phone, displayName, identifierType string) (int64, error) {
```

Find the hardcoded `'whatsapp'` in the INSERT into `participant_identifiers` (around line 945) and replace with the `identifierType` parameter.

- [ ] **Step 4: Update all callers in whatsapp package**

In `internal/whatsapp/importer.go`, find every call to `EnsureParticipantByPhone(phone, name)` and add `"whatsapp"` as the third argument. Use `grep -rn "EnsureParticipantByPhone"` to find all call sites.

- [ ] **Step 5: Run tests**

Run: `go test -tags fts5 ./internal/store/ -run TestEnsureParticipantByPhone -v && go test ./internal/whatsapp/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/messages.go internal/store/messages_test.go internal/whatsapp/
git commit -m "Generalize EnsureParticipantByPhone to accept identifierType"
```

### Task 3: RecomputeConversationStats Store Method

**Files:**
- Modify: `internal/store/messages.go` (add method)
- Modify: `internal/whatsapp/importer.go:498-514` (replace inline SQL)

- [ ] **Step 1: Write test**

```go
func TestRecomputeConversationStats(t *testing.T) {
	s := setupTestStore(t)
	defer func() { _ = s.Close() }()

	// Create a source
	sourceID, err := s.GetOrCreateSource("test_source", "whatsapp", "")
	if err != nil {
		t.Fatal(err)
	}

	// Create a conversation
	convID, err := s.EnsureConversationWithType(sourceID, "conv1", "direct_chat", "Test Chat")
	if err != nil {
		t.Fatal(err)
	}

	// Insert two messages
	for i, snippet := range []string{"hello", "world"} {
		_, err := s.UpsertMessage(&Message{
			SourceID:             sourceID,
			SourceMessageID:      fmt.Sprintf("msg%d", i),
			ConversationID:       convID,
			Snippet:              snippet,
			SentAt:               sql.NullTime{Time: time.Now().Add(time.Duration(i) * time.Hour), Valid: true},
			MessageType:          "whatsapp",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Stats should be zero before recompute
	var msgCount int64
	_ = s.DB().QueryRow("SELECT message_count FROM conversations WHERE id = ?", convID).Scan(&msgCount)
	if msgCount != 0 {
		t.Errorf("before recompute: message_count = %d, want 0", msgCount)
	}

	// Recompute
	if err := s.RecomputeConversationStats(sourceID); err != nil {
		t.Fatal(err)
	}

	// Verify
	_ = s.DB().QueryRow("SELECT message_count FROM conversations WHERE id = ?", convID).Scan(&msgCount)
	if msgCount != 2 {
		t.Errorf("after recompute: message_count = %d, want 2", msgCount)
	}

	// Running again should be idempotent
	if err := s.RecomputeConversationStats(sourceID); err != nil {
		t.Fatal(err)
	}
	_ = s.DB().QueryRow("SELECT message_count FROM conversations WHERE id = ?", convID).Scan(&msgCount)
	if msgCount != 2 {
		t.Errorf("after second recompute: message_count = %d, want 2", msgCount)
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test -tags fts5 ./internal/store/ -run TestRecomputeConversationStats -v`
Expected: FAIL — method not found

- [ ] **Step 3: Implement RecomputeConversationStats**

Add to `internal/store/messages.go`:

```go
// RecomputeConversationStats recomputes denormalized stats
// (message_count, participant_count, last_message_at,
// last_message_preview) for all conversations belonging to sourceID.
// This is idempotent — safe to call after any import or re-import.
func (s *Store) RecomputeConversationStats(sourceID int64) error {
	_, err := s.db.Exec(`
		UPDATE conversations SET
			message_count = (
				SELECT COUNT(*) FROM messages
				WHERE conversation_id = conversations.id
			),
			participant_count = (
				SELECT COUNT(*) FROM conversation_participants
				WHERE conversation_id = conversations.id
			),
			last_message_at = (
				SELECT MAX(COALESCE(sent_at, received_at, internal_date))
				FROM messages
				WHERE conversation_id = conversations.id
			),
			last_message_preview = (
				SELECT snippet FROM messages
				WHERE conversation_id = conversations.id
				ORDER BY COALESCE(sent_at, received_at, internal_date) DESC
				LIMIT 1
			)
		WHERE source_id = ?
	`, sourceID)
	if err != nil {
		return fmt.Errorf("recompute conversation stats: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -tags fts5 ./internal/store/ -run TestRecomputeConversationStats -v`
Expected: PASS

- [ ] **Step 5: Replace WhatsApp inline SQL with shared method**

In `internal/whatsapp/importer.go:498-514`, replace the inline `UPDATE conversations SET ...` with:

```go
if err := imp.store.RecomputeConversationStats(source.ID); err != nil {
	imp.log("Warning: failed to recompute conversation stats: %v", err)
}
```

- [ ] **Step 6: Run WhatsApp tests, commit**

```bash
go test ./internal/whatsapp/ -v
go fmt ./...
git add internal/store/messages.go internal/store/messages_test.go internal/whatsapp/importer.go
git commit -m "Add shared RecomputeConversationStats store method"
```

### Task 4: Add LinkMessageLabel Store Method

**Files:**
- Modify: `internal/store/messages.go` (add method)

The spec calls for `LinkMessageLabel(messageID, labelID)`. The store has `AddMessageLabels(messageID int64, labelIDs []int64)` at line 570 which does `INSERT OR IGNORE` for a slice. Add a convenience single-label wrapper.

- [ ] **Step 1: Add LinkMessageLabel**

```go
// LinkMessageLabel links a single label to a message.
// Uses INSERT OR IGNORE — safe to call multiple times.
func (s *Store) LinkMessageLabel(messageID, labelID int64) error {
	return s.AddMessageLabels(messageID, []int64{labelID})
}
```

- [ ] **Step 2: Run fmt/vet, commit**

```bash
go fmt ./internal/store/...
go vet ./internal/store/...
git add internal/store/messages.go
git commit -m "Add LinkMessageLabel convenience method"
```

---

## Phase 2: Importer Refactoring

### Task 5: Refactor iMessage Importer

**Files:**
- Rewrite: `internal/imessage/client.go` — drop gmail.API, use store methods
- Modify: `internal/imessage/parser.go` — use shared NormalizePhone
- Modify: `internal/imessage/models.go` — update types if needed
- Rewrite: `cmd/msgvault/cmd/sync_imessage.go` → `cmd/msgvault/cmd/import_imessage.go`
- Update: `internal/imessage/parser_test.go`

This is the largest refactoring task. The key changes:

1. `Client` no longer implements `gmail.API`
2. `Client` takes a `*store.Store` and writes directly
3. New `Import(ctx, store, opts)` method replaces the sync pipeline
4. `normalizeIdentifier` uses shared `textimport.NormalizePhone` with fallback to email path
5. No more synthetic MIME — body goes to `message_bodies`, raw to `message_raw`

- [ ] **Step 1: Update parser.go to use shared NormalizePhone**

Replace the `normalizeIdentifier` function in `internal/imessage/parser.go` to use `textimport.NormalizePhone`:

```go
import "github.com/wesm/msgvault/internal/textimport"

// resolveHandle categorizes an iMessage handle as phone or email.
// Returns (phone, email, displayName). Exactly one of phone/email
// will be non-empty.
func resolveHandle(handleID string) (phone, email, displayName string) {
	if handleID == "" {
		return "", "", ""
	}
	// Try phone normalization first
	normalized, err := textimport.NormalizePhone(handleID)
	if err == nil {
		return normalized, "", normalized
	}
	// Fall back to email
	if strings.Contains(handleID, "@") {
		return "", strings.ToLower(handleID), ""
	}
	// Neither — raw handle
	return "", "", handleID
}
```

Remove the old `normalizeIdentifier`, `normalizePhone`, `buildMIME`, `formatMIMEAddress` functions — they're no longer needed.

- [ ] **Step 2: Rewrite Client to use store methods directly**

Replace the `gmail.API` implementation in `internal/imessage/client.go`. The new `Client` struct holds a `*sql.DB` (read-only handle to chat.db) and exposes an `Import` method:

```go
type Client struct {
	db              *sql.DB
	myHandle        string // owner's phone or email
	afterDate       *time.Time
	beforeDate      *time.Time
	limit           int
	useNanoseconds  bool
	logger          *slog.Logger
}

// Import reads iMessage history from chat.db and writes to the
// msgvault store. Returns a summary of what was imported.
func (c *Client) Import(ctx context.Context, s *store.Store, opts ImportOptions) (*ImportSummary, error) {
	// 1. GetOrCreateSource with source_type="apple_messages"
	// 2. Ensure labels ("iMessage", "SMS")
	// 3. Query chat.db for conversations (chats)
	// 4. For each chat:
	//    a. EnsureConversationWithType (group vs direct)
	//    b. Resolve participants via resolveHandle → EnsureParticipantByPhone or EnsureParticipant
	//    c. EnsureConversationParticipant for each
	//    d. Query messages for this chat
	//    e. For each message: UpsertMessage with message_type, sender_id
	//    f. UpsertMessageBody with body text
	//    g. LinkMessageLabel
	// 5. RecomputeConversationStats
	// 6. Return summary
}
```

Remove the `gmail.API` interface assertion and all gmail.API methods (`GetProfile`, `ListLabels`, `ListMessages`, `GetMessageRaw`, `GetMessagesRawBatch`, `ListHistory`, `TrashMessage`, `DeleteMessage`, `BatchDeleteMessages`).

Keep the `chat.db` reading logic (SQL queries, timestamp handling, `detectTimestampFormat`). The SQL queries that read from chat.db stay the same — only the output path changes.

- [ ] **Step 3: Rewrite CLI command**

Move `cmd/msgvault/cmd/sync_imessage.go` → `cmd/msgvault/cmd/import_imessage.go`. Replace the `sync-imessage` cobra command with `import-imessage`. Remove all `sync.Syncer` usage — call `client.Import(ctx, store, opts)` directly.

Register the new command in `root.go`.

- [ ] **Step 4: Update tests**

Update `internal/imessage/parser_test.go`:
- Replace tests for `normalizeIdentifier` with tests for `resolveHandle`
- Remove tests for `buildMIME` / `formatMIMEAddress`
- Add tests for phone/email/raw-handle resolution

- [ ] **Step 5: Run all tests**

```bash
go test ./internal/imessage/ -v
go test ./internal/store/ -v
go vet ./...
```

- [ ] **Step 6: Commit**

```bash
git add internal/imessage/ cmd/msgvault/cmd/
git commit -m "Refactor iMessage to use store methods directly

Drop gmail.API adapter and synthetic MIME. iMessage now writes to
the store using EnsureParticipantByPhone, EnsureConversationWithType,
and proper message_type/sender_id/conversation_type fields."
```

### Task 6: Refactor Google Voice Importer

**Files:**
- Rewrite: `internal/gvoice/client.go` — drop gmail.API, use store methods
- Modify: `internal/gvoice/parser.go` — use shared NormalizePhone
- Modify: `internal/gvoice/models.go` — add message_type mapping
- Rewrite: `cmd/msgvault/cmd/sync_gvoice.go` → `cmd/msgvault/cmd/import_gvoice.go`
- Update: `internal/gvoice/parser_test.go`

Same pattern as Task 5. Key differences:

1. GVoice reads from a Takeout directory (HTML files), not a database
2. Three message_type values: `google_voice_text`, `google_voice_call`, `google_voice_voicemail`
3. All participants are phone-based (no email fallback needed)
4. `normalizeIdentifier` in parser.go replaced with `textimport.NormalizePhone`

- [ ] **Step 1: Update parser.go to use shared NormalizePhone**

Replace `normalizeIdentifier` and `normalizePhone` in `internal/gvoice/parser.go` with calls to `textimport.NormalizePhone`. Remove `buildMIME` and `formatMIMEAddress`.

- [ ] **Step 2: Add message_type mapping to models.go**

```go
// MessageTypeForFileType returns the messages.message_type value
// for a Google Voice file type.
func MessageTypeForFileType(ft fileType) string {
	switch ft {
	case fileTypeText, fileTypeGroup:
		return "google_voice_text"
	case fileTypeReceived, fileTypePlaced, fileTypeMissed:
		return "google_voice_call"
	case fileTypeVoicemail:
		return "google_voice_voicemail"
	default:
		return "google_voice_text"
	}
}
```

- [ ] **Step 3: Rewrite Client to use store methods directly**

Same approach as iMessage — new `Import` method, remove all gmail.API methods and interface assertion. The HTML parsing stays the same; the output path changes to store methods.

For each indexed entry:
1. Resolve phone via `textimport.NormalizePhone`
2. `EnsureParticipantByPhone(phone, name, "google_voice")`
3. `EnsureConversationWithType` with thread ID
4. `UpsertMessage` with `message_type = MessageTypeForFileType(entry.FileType)`
5. `UpsertMessageBody` with body text
6. `UpsertMessageRawWithFormat` with raw HTML as `gvoice_html`
7. `EnsureLabel` + `LinkMessageLabel` for each label
8. After all entries: `RecomputeConversationStats`

- [ ] **Step 4: Rewrite CLI command**

Move `sync_gvoice.go` → `import_gvoice.go`. Replace `sync-gvoice` with `import-gvoice`. Remove `sync.Syncer` usage.

- [ ] **Step 5: Update tests, run all**

Update parser_test.go to test `textimport.NormalizePhone` integration. Remove MIME-related test assertions.

```bash
go test ./internal/gvoice/ -v
go vet ./...
```

- [ ] **Step 6: Commit**

```bash
git add internal/gvoice/ cmd/msgvault/cmd/
git commit -m "Refactor Google Voice to use store methods directly

Drop gmail.API adapter and synthetic MIME. Google Voice now writes
to the store with proper message_type (google_voice_text/call/
voicemail), phone-based participants, and labels."
```

### Task 7: WhatsApp Cleanup

**Files:**
- Modify: `internal/whatsapp/importer.go` — use shared NormalizePhone
- Modify: `internal/whatsapp/contacts.go` — use shared NormalizePhone

- [ ] **Step 1: Replace internal normalizePhone with shared utility**

Find all calls to the internal `normalizePhone` in the whatsapp package and replace with `textimport.NormalizePhone`. The internal function is in `internal/whatsapp/mapping.go` or `contacts.go`. Since the shared version returns an error, callers need to handle it (skip participants that don't normalize).

- [ ] **Step 2: Update EnsureParticipantByPhone calls**

All calls in the whatsapp package already pass `"whatsapp"` after Task 2. Verify.

- [ ] **Step 3: Run tests, commit**

```bash
go test ./internal/whatsapp/ -v
go fmt ./...
git add internal/whatsapp/
git commit -m "WhatsApp: use shared NormalizePhone and RecomputeConversationStats"
```

### Task 8: Rename CLI Commands and Register

**Files:**
- Rename: `cmd/msgvault/cmd/import.go` → verify naming
- Modify: `cmd/msgvault/cmd/root.go` — register new commands, remove old

- [ ] **Step 1: Ensure all three import commands are registered**

The WhatsApp import command is currently `import --type whatsapp` (in `cmd/msgvault/cmd/import.go`). Rename to `import-whatsapp`. The iMessage and GVoice commands were already renamed in Tasks 5-6.

Update `root.go` to register: `importWhatsappCmd`, `importImessageCmd`, `importGvoiceCmd`. Remove any old `syncImessageCmd`, `syncGvoiceCmd` references.

- [ ] **Step 2: Verify all commands work**

```bash
go build -tags fts5 -o msgvault ./cmd/msgvault
./msgvault import-whatsapp --help
./msgvault import-imessage --help
./msgvault import-gvoice --help
```

- [ ] **Step 3: Commit**

```bash
git add cmd/msgvault/
git commit -m "Rename import CLI commands for consistency

import-whatsapp, import-imessage, import-gvoice"
```

---

## Phase 3: Parquet Cache & TextEngine

### Task 9: Extend Parquet Cache for Text Messages

**Files:**
- Modify: `cmd/msgvault/cmd/build_cache.go` — add columns to export queries
- Modify: `internal/query/duckdb.go` — probe new columns

The existing `build_cache.go` exports `messages`, `participants`, `conversations`, etc. to Parquet. We need to ensure the export includes the columns required for Texts mode queries.

- [ ] **Step 1: Add conversation_type to conversations export**

In `build_cache.go`, find the conversations export query (around line 460) and add `conversation_type` to the SELECT. The schema already has this column.

- [ ] **Step 2: Add message_type and sender_id to messages export**

The messages export (around line 300) already includes `message_type` and `sender_id` (added by the WhatsApp PR). Verify they're present. If not, add them.

- [ ] **Step 3: Bump cache schema version**

Change `cacheSchemaVersion` from 4 to 5. This forces a full rebuild when users upgrade, ensuring new columns are present.

- [ ] **Step 4: Update DuckDB column probing**

In `internal/query/duckdb.go`, the `probeParquetColumns` method checks for optional columns. Ensure `conversation_type` is probed for the conversations table.

- [ ] **Step 5: Add email-only filter to existing Engine queries**

In `DuckDBEngine.Aggregate`, `DuckDBEngine.ListMessages`, etc., add a `WHERE message_type = 'email' OR message_type IS NULL` filter so email-mode queries exclude text messages. The `IS NULL` handles old data without the column.

This is a targeted change in `buildFilterConditions` (line 803) — add it as a default condition when no explicit message_type filter is set.

- [ ] **Step 6: Run tests, commit**

```bash
go test -tags fts5 ./internal/query/ -v
go test -tags fts5 ./cmd/msgvault/cmd/ -v
git add cmd/msgvault/cmd/build_cache.go internal/query/duckdb.go
git commit -m "Extend Parquet cache with text message columns

Add conversation_type to exports, bump cache schema to v5,
filter email queries to exclude text messages."
```

### Task 10: TextEngine Interface and Types

**Files:**
- Create: `internal/query/text_engine.go`
- Create: `internal/query/text_models.go`

- [ ] **Step 1: Define TextEngine types**

```go
// internal/query/text_models.go
package query

import "time"

// TextViewType represents the type of view in Texts mode.
type TextViewType int

const (
	TextViewConversations TextViewType = iota
	TextViewContacts
	TextViewContactNames
	TextViewSources
	TextViewLabels
	TextViewTime
	TextViewTypeCount
)

func (v TextViewType) String() string {
	switch v {
	case TextViewConversations:
		return "Conversations"
	case TextViewContacts:
		return "Contacts"
	case TextViewContactNames:
		return "Contact Names"
	case TextViewSources:
		return "Sources"
	case TextViewLabels:
		return "Labels"
	case TextViewTime:
		return "Time"
	default:
		return "Unknown"
	}
}

// ConversationRow represents a conversation in the Conversations view.
type ConversationRow struct {
	ConversationID   int64
	Title            string
	SourceType       string
	MessageCount     int64
	ParticipantCount int64
	LastMessageAt    time.Time
	LastPreview      string
}

// TextFilter specifies which text messages to retrieve.
type TextFilter struct {
	SourceID       *int64
	ConversationID *int64
	ContactPhone   string
	ContactName    string
	SourceType     string
	Label          string
	TimeRange      TimeRange
	After          *time.Time
	Before         *time.Time
	Pagination     Pagination
	SortField      SortField
	SortDirection  SortDirection
}

// TextAggregateOptions configures a text aggregate query.
type TextAggregateOptions struct {
	SourceID        *int64
	After           *time.Time
	Before          *time.Time
	SortField       SortField
	SortDirection   SortDirection
	Limit           int
	TimeGranularity TimeGranularity
	SearchQuery     string
}

// TextStatsOptions configures a text stats query.
type TextStatsOptions struct {
	SourceID    *int64
	SearchQuery string
}

// TextMessageTypes lists the message_type values included in Texts mode.
var TextMessageTypes = []string{
	"whatsapp", "imessage", "sms", "google_voice_text",
}

// IsTextMessageType returns true if the given type is a text message type.
func IsTextMessageType(mt string) bool {
	for _, t := range TextMessageTypes {
		if t == mt {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Define TextEngine interface**

```go
// internal/query/text_engine.go
package query

import "context"

// TextEngine provides query operations for text message data.
// This is a separate interface from Engine to avoid rippling text
// query methods through remote/API/MCP/mock layers.
// DuckDBEngine and SQLiteEngine implement both Engine and TextEngine.
type TextEngine interface {
	// ListConversations returns conversations matching the filter.
	ListConversations(ctx context.Context,
		filter TextFilter) ([]ConversationRow, error)

	// TextAggregate aggregates text messages by the given view type.
	TextAggregate(ctx context.Context, viewType TextViewType,
		opts TextAggregateOptions) ([]AggregateRow, error)

	// ListConversationMessages returns messages within a conversation.
	ListConversationMessages(ctx context.Context, convID int64,
		filter TextFilter) ([]MessageSummary, error)

	// TextSearch performs plain full-text search over text messages.
	TextSearch(ctx context.Context, query string,
		limit, offset int) ([]MessageSummary, error)

	// GetTextStats returns aggregate stats for text messages.
	GetTextStats(ctx context.Context,
		opts TextStatsOptions) (*TotalStats, error)
}
```

- [ ] **Step 3: Run fmt/vet, commit**

```bash
go fmt ./internal/query/...
go vet ./internal/query/...
git add internal/query/text_engine.go internal/query/text_models.go
git commit -m "Add TextEngine interface and text query types"
```

### Task 11: DuckDB TextEngine Implementation

**Files:**
- Create: `internal/query/duckdb_text.go`
- Create: `internal/query/duckdb_text_test.go`

Implement `TextEngine` methods on `DuckDBEngine`. These query the same Parquet files as email queries but filter to text message types and use different grouping columns.

- [ ] **Step 1: Implement ListConversations**

```go
// internal/query/duckdb_text.go
package query

// ... imports ...

// textTypeFilter returns a SQL IN clause for text message types.
func textTypeFilter() string {
	return "message_type IN ('whatsapp','imessage','sms','google_voice_text')"
}

func (e *DuckDBEngine) ListConversations(ctx context.Context,
	filter TextFilter) ([]ConversationRow, error) {
	// Query conversations table joined with message stats
	// from the Parquet messages, filtered to text message types.
	// Uses denormalized stats from conversations table (via SQLite
	// scanner or conversations Parquet).
	// Sort by last_message_at DESC by default.
	// Apply filter: SourceID, After/Before, Pagination.
	// ...
}
```

The implementation queries the `conversations` Parquet table joined with `sources` to get `source_type`, filtered to text source types (`'whatsapp'`, `'apple_messages'`, `'google_voice'`).

- [ ] **Step 2: Implement TextAggregate**

Aggregation by view type:
- `TextViewContacts`: GROUP BY `phone_number`, `display_name`
- `TextViewContactNames`: GROUP BY `display_name`
- `TextViewSources`: GROUP BY `source_type`
- `TextViewLabels`: GROUP BY label name (JOIN message_labels + labels)
- `TextViewTime`: GROUP BY time period

All queries include `WHERE textTypeFilter()`.

- [ ] **Step 3: Implement ListConversationMessages**

Query messages from Parquet where `conversation_id = convID` and `textTypeFilter()`, ordered by `sent_at ASC` (chronological for chat timeline).

- [ ] **Step 4: Implement TextSearch**

Plain FTS query against `messages_fts` via the SQLite scanner, filtered to text message types. No Gmail-style operator parsing — pass the query string directly to FTS5 MATCH.

- [ ] **Step 5: Implement GetTextStats**

Aggregate stats (message count, total size, etc.) filtered to text message types.

- [ ] **Step 6: Add interface assertion**

```go
var _ TextEngine = (*DuckDBEngine)(nil)
```

- [ ] **Step 7: Write tests**

Create `internal/query/duckdb_text_test.go` with test fixtures that include text message data. Test `ListConversations`, `TextAggregate` for each view type, `ListConversationMessages`, and `GetTextStats`.

Use the existing test fixture pattern from `internal/query/testfixtures_test.go` — extend it to include text message data with proper `message_type`, `sender_id`, and `conversation_type` values.

- [ ] **Step 8: Run tests, commit**

```bash
go test -tags fts5 ./internal/query/ -run TestText -v
git add internal/query/duckdb_text.go internal/query/duckdb_text_test.go
git commit -m "Implement TextEngine on DuckDBEngine

ListConversations, TextAggregate, ListConversationMessages,
TextSearch, GetTextStats — all querying Parquet with text
message type filters."
```

### Task 12: SQLite TextEngine Fallback

**Files:**
- Create: `internal/query/sqlite_text.go`

Implement `TextEngine` on `SQLiteEngine` as a fallback for when Parquet cache is not built. Same logic as DuckDB but querying SQLite directly.

- [ ] **Step 1: Implement all five TextEngine methods**

Same patterns as DuckDB but using SQLite SQL. Key difference: joins go to real tables instead of Parquet files.

- [ ] **Step 2: Add interface assertion**

```go
var _ TextEngine = (*SQLiteEngine)(nil)
```

- [ ] **Step 3: Run tests, commit**

```bash
go test -tags fts5 ./internal/query/ -v
git add internal/query/sqlite_text.go
git commit -m "Implement TextEngine on SQLiteEngine as fallback"
```

### Task 13: Update FTS Backfill for Text Messages

**Files:**
- Modify: `internal/store/messages.go` (FTS backfill query)

The current FTS backfill populates `from_addr` from `message_recipients` where `recipient_type = 'from'`. Text messages use `sender_id` instead. Update the backfill to handle both paths.

- [ ] **Step 1: Find the FTS backfill query**

In `internal/store/messages.go`, find the `BackfillFTS` or similar method that populates `messages_fts`. Look for the INSERT INTO `messages_fts` query.

- [ ] **Step 2: Update the from_addr population**

Change the `from_addr` subquery to use COALESCE:

```sql
COALESCE(
    (SELECT COALESCE(p.phone_number, p.email_address)
     FROM participants p WHERE p.id = m.sender_id),
    (SELECT p.email_address FROM message_recipients mr
     JOIN participants p ON p.id = mr.participant_id
     WHERE mr.message_id = m.id AND mr.recipient_type = 'from'
     LIMIT 1)
) as from_addr
```

This checks `sender_id` first (for text messages), falls back to `message_recipients` (for email).

- [ ] **Step 3: Run FTS tests, commit**

```bash
go test -tags fts5 ./internal/store/ -v
git add internal/store/messages.go
git commit -m "Update FTS backfill to handle phone-based text senders"
```

---

## Phase 4: TUI Texts Mode

### Task 14: TUI Model State for Texts Mode

**Files:**
- Modify: `internal/tui/model.go` — add text mode state
- Create: `internal/tui/text_state.go` — text-specific state types

- [ ] **Step 1: Add text mode types and state**

```go
// internal/tui/text_state.go
package tui

import "github.com/wesm/msgvault/internal/query"

// tuiMode distinguishes Email mode from Texts mode.
type tuiMode int

const (
	modeEmail tuiMode = iota
	modeTexts
)

// textViewLevel tracks navigation depth in Texts mode.
type textViewLevel int

const (
	textLevelConversations textViewLevel = iota
	textLevelAggregate
	textLevelDrillConversations // conversations filtered by aggregate key
	textLevelTimeline           // messages within a conversation
)

// textState holds all state specific to Texts mode.
type textState struct {
	viewType      query.TextViewType
	level         textViewLevel
	conversations []query.ConversationRow
	aggregateRows []query.AggregateRow
	messages      []query.MessageSummary
	cursor        int
	scrollOffset  int
	selectedConvID int64

	// Filter state
	filter query.TextFilter

	// Stats
	stats *query.TotalStats

	// Breadcrumbs for back navigation
	breadcrumbs []textNavSnapshot
}

type textNavSnapshot struct {
	level         textViewLevel
	viewType      query.TextViewType
	cursor        int
	scrollOffset  int
	filter        query.TextFilter
	selectedConvID int64
}
```

- [ ] **Step 2: Add mode and textState to Model**

In `internal/tui/model.go`, add to the `Model` struct:

```go
mode       tuiMode
textEngine query.TextEngine // nil if not available
textState  textState
```

In the `New` constructor, check if the engine implements `TextEngine`:

```go
if te, ok := engine.(query.TextEngine); ok {
    m.textEngine = te
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/tui/text_state.go internal/tui/model.go
git commit -m "Add Texts mode state types to TUI model"
```

### Task 15: Mode Switching (m key)

**Files:**
- Modify: `internal/tui/keys.go` — add `m` key handler
- Modify: `internal/tui/model.go` — route Update based on mode

- [ ] **Step 1: Add m key to handleGlobalKeys**

In `internal/tui/keys.go`, in `handleGlobalKeys` (around line 86), add:

```go
case "m":
	if m.textEngine == nil {
		return m, nil, true // no text engine, ignore
	}
	if m.mode == modeEmail {
		m.mode = modeTexts
		// Load text conversations
		return m, m.loadTextConversations(), true
	}
	m.mode = modeEmail
	return m, m.loadData(), true
```

- [ ] **Step 2: Route key handling by mode in Update**

In `model.go`'s `Update` method, after global key handling, branch on `m.mode`:

```go
if m.mode == modeTexts {
    return m.handleTextKeyPress(msg)
}
// ... existing email key handling
```

- [ ] **Step 3: Commit**

```bash
git add internal/tui/keys.go internal/tui/model.go
git commit -m "Add mode switching between Email and Texts (m key)"
```

### Task 16: Texts Mode Key Handling

**Files:**
- Create: `internal/tui/text_keys.go`

- [ ] **Step 1: Implement text mode key dispatch**

```go
// internal/tui/text_keys.go
package tui

import tea "github.com/charmbracelet/bubbletea"

func (m Model) handleTextKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Disabled keys in Texts mode
	switch key {
	case " ", "S", "d", "D", "x":
		return m, nil // read-only mode
	}

	switch m.textState.level {
	case textLevelConversations, textLevelAggregate,
		textLevelDrillConversations:
		return m.handleTextListKeys(msg)
	case textLevelTimeline:
		return m.handleTextTimelineKeys(msg)
	}
	return m, nil
}

func (m Model) handleTextListKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "tab", "Tab":
		m.cycleTextViewType(true)
		return m, m.loadTextData()
	case "shift+tab":
		m.cycleTextViewType(false)
		return m, m.loadTextData()
	case "enter":
		return m.textDrillDown()
	case "esc", "backspace":
		return m.textGoBack()
	case "j", "down":
		m.textState.cursor++
		m.clampTextCursor()
		return m, nil
	case "k", "up":
		m.textState.cursor--
		m.clampTextCursor()
		return m, nil
	case "s":
		m.cycleTextSortField()
		return m, m.loadTextData()
	case "r":
		m.toggleTextSortDirection()
		return m, m.loadTextData()
	case "t":
		m.textState.viewType = query.TextViewTime
		m.textState.level = textLevelAggregate
		return m, m.loadTextData()
	case "a":
		// Reset to conversations
		m.textState = textState{viewType: query.TextViewConversations}
		return m, m.loadTextConversations()
	case "A":
		m.openAccountSelector()
		return m, nil
	}
	return m, nil
}
```

- [ ] **Step 2: Implement helper methods**

Add `cycleTextViewType`, `clampTextCursor`, `textDrillDown`, `textGoBack`, `loadTextData`, `loadTextConversations` methods. These follow the same patterns as the email equivalents but operate on `textState`.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/text_keys.go
git commit -m "Add Texts mode key handling"
```

### Task 17: Texts Mode Views

**Files:**
- Create: `internal/tui/text_view.go`

- [ ] **Step 1: Implement text conversations view**

```go
// internal/tui/text_view.go
package tui

// textConversationsView renders the Conversations list.
func (m Model) textConversationsView() string {
	// Header: Name | Source | Messages | Participants | Last Message
	// Rows from m.textState.conversations
	// Same styling patterns as aggregateTableView
}
```

- [ ] **Step 2: Implement text aggregate view**

```go
// textAggregateView renders aggregate views (Contacts, Sources, etc.)
func (m Model) textAggregateView() string {
	// Same shape as email aggregate view
	// Rows from m.textState.aggregateRows
}
```

- [ ] **Step 3: Implement text timeline view**

```go
// textTimelineView renders a conversation's message timeline.
func (m Model) textTimelineView() string {
	// Compact chat style: timestamp | sender | body snippet
	// Rows from m.textState.messages
	// Chronological order (oldest first)
}
```

- [ ] **Step 4: Wire into renderView**

In the main `renderView()` switch (internal/tui/view.go), add a mode check:

```go
if m.mode == modeTexts {
    return m.renderTextView()
}
```

Implement `renderTextView()` in text_view.go, switching on `m.textState.level`.

- [ ] **Step 5: Update footer for Texts mode**

In `footerView()`, add a Texts mode branch that shows the correct keybindings for the current text view level.

- [ ] **Step 6: Add mode indicator to header**

In `buildTitleBar()`, show "Email" or "Texts" mode indicator. Show "m: switch mode" in the title bar.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/text_view.go internal/tui/view.go
git commit -m "Add Texts mode views: conversations, aggregates, timeline"
```

### Task 18: Texts Mode Search

**Files:**
- Modify: `internal/tui/text_keys.go` — add `/` handler
- Create: `internal/tui/text_search.go` — text search state management

- [ ] **Step 1: Add search handling**

In `handleTextListKeys`, the `/` key enters search mode. In Texts mode, search uses plain FTS (no Gmail operators):

```go
case "/":
	m.searchMode = true
	m.searchInput = ""
	return m, nil
```

When search is submitted, call `m.textEngine.TextSearch(ctx, query, limit, 0)` instead of the email search path.

- [ ] **Step 2: Display search results**

Search results in Texts mode show as a message list (same as timeline view). Pressing Esc exits search.

- [ ] **Step 3: Commit**

```bash
git add internal/tui/text_keys.go internal/tui/text_search.go
git commit -m "Add plain full-text search in Texts mode"
```

### Task 19: Data Loading Commands for Texts Mode

**Files:**
- Create: `internal/tui/text_commands.go`

- [ ] **Step 1: Implement async data loading commands**

Following the Bubble Tea pattern, each data load returns a `tea.Cmd` that runs asynchronously and sends a message when done:

```go
// internal/tui/text_commands.go
package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/wesm/msgvault/internal/query"
)

// Message types for async text data loading
type textConversationsLoadedMsg struct {
	conversations []query.ConversationRow
	err           error
}

type textAggregateLoadedMsg struct {
	rows []query.AggregateRow
	err  error
}

type textMessagesLoadedMsg struct {
	messages []query.MessageSummary
	err      error
}

type textStatsLoadedMsg struct {
	stats *query.TotalStats
	err   error
}

func (m Model) loadTextConversations() tea.Cmd {
	return func() tea.Msg {
		convs, err := m.textEngine.ListConversations(
			context.Background(), m.textState.filter)
		return textConversationsLoadedMsg{convs, err}
	}
}

func (m Model) loadTextAggregate() tea.Cmd {
	return func() tea.Msg {
		rows, err := m.textEngine.TextAggregate(
			context.Background(),
			m.textState.viewType,
			query.TextAggregateOptions{
				SourceID:      m.textState.filter.SourceID,
				After:         m.textState.filter.After,
				Before:        m.textState.filter.Before,
				SortField:     m.textState.filter.SortField,
				SortDirection: m.textState.filter.SortDirection,
				Limit:         m.aggregateLimit,
			})
		return textAggregateLoadedMsg{rows, err}
	}
}

func (m Model) loadTextMessages() tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.textEngine.ListConversationMessages(
			context.Background(),
			m.textState.selectedConvID,
			m.textState.filter)
		return textMessagesLoadedMsg{msgs, err}
	}
}

func (m Model) loadTextData() tea.Cmd {
	switch m.textState.viewType {
	case query.TextViewConversations:
		return m.loadTextConversations()
	default:
		return m.loadTextAggregate()
	}
}
```

- [ ] **Step 2: Handle loaded messages in Update**

In `model.go`'s `Update` method, add cases for the new message types:

```go
case textConversationsLoadedMsg:
    m.textState.conversations = msg.conversations
    m.loading = false
    // ...
case textAggregateLoadedMsg:
    m.textState.aggregateRows = msg.rows
    m.loading = false
    // ...
case textMessagesLoadedMsg:
    m.textState.messages = msg.messages
    m.loading = false
    // ...
```

- [ ] **Step 3: Commit**

```bash
git add internal/tui/text_commands.go internal/tui/model.go
git commit -m "Add async data loading for Texts mode"
```

---

## Phase 5: Integration & Polish

### Task 20: Wire TUI Init to Load Text Engine

**Files:**
- Modify: `cmd/msgvault/cmd/tui.go`

- [ ] **Step 1: Pass TextEngine to TUI**

In `tui.go`, after creating the query engine, check if it implements `TextEngine` and pass it through `tui.Options`:

```go
type Options struct {
	DataDir    string
	Version    string
	IsRemote   bool
	TextEngine query.TextEngine // nil if not available
}
```

In the TUI command's `RunE`, after engine creation:

```go
var textEngine query.TextEngine
if te, ok := engine.(query.TextEngine); ok {
    textEngine = te
}
opts := tui.Options{
    DataDir:    dataDir,
    Version:    version,
    IsRemote:   isRemote,
    TextEngine: textEngine,
}
```

- [ ] **Step 2: Commit**

```bash
git add cmd/msgvault/cmd/tui.go internal/tui/model.go
git commit -m "Wire TextEngine into TUI initialization"
```

### Task 21: End-to-End Integration Test

**Files:**
- Create: `internal/textimport/integration_test.go`

- [ ] **Step 1: Write integration test**

Create a test that:
1. Creates an in-memory store
2. Simulates importing messages from two different sources using store methods directly (no actual chat.db/Takeout needed)
3. Verifies participant deduplication by phone number
4. Verifies conversation stats after RecomputeConversationStats
5. Verifies labels are linked
6. Creates a SQLiteEngine and verifies TextEngine methods return correct results

This test exercises the full pipeline without needing real source data.

- [ ] **Step 2: Run tests, commit**

```bash
go test -tags fts5 ./internal/textimport/ -run TestIntegration -v
git add internal/textimport/integration_test.go
git commit -m "Add end-to-end integration test for text message import"
```

### Task 22: Build and Smoke Test

- [ ] **Step 1: Build**

```bash
make build
```

- [ ] **Step 2: Run full test suite**

```bash
make test
```

- [ ] **Step 3: Run linter**

```bash
make lint
```

- [ ] **Step 4: Fix any issues and commit**

### Task 23: Final Commit — Remove Dead Code

- [ ] **Step 1: Remove old sync command registrations**

Check `root.go` for any remaining references to `syncImessageCmd`, `syncGvoiceCmd`. Remove them.

- [ ] **Step 2: Remove unused gmail.API imports from imessage/gvoice packages**

After refactoring, `internal/imessage/` and `internal/gvoice/` should no longer import `gmail` package. Verify and clean up.

- [ ] **Step 3: Remove the design plan doc from WhatsApp PR**

`docs/plans/2026-02-17-multi-source-messaging.md` was included in the WhatsApp PR as a planning doc. It's superseded by the spec. Remove it.

- [ ] **Step 4: Run full test suite and linter one final time**

```bash
make test && make lint
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "Clean up dead code and superseded planning docs"
```
