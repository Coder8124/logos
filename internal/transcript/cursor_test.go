package transcript_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Coder8124/logos/internal/transcript"
	_ "modernc.org/sqlite"
)

// cursorStorage writes the fixture Cursor keeps its chats in: one SQLite file,
// a composerData row per conversation listing its messages by id, and a
// bubbleId row per message. Built here rather than committed as a binary blob
// so the shape this reader depends on is legible in the test that asserts it.
func cursorStorage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.vscdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	put := func(k string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO cursorDiskKV VALUES (?, ?)`, k, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	const id = "f099c688-516f-4b51-bba7-a732591ec3dd"
	put("composerData:"+id, map[string]any{
		"composerId": id,
		"name":       "Linking conventions to cursor rules",
		"createdAt":  1775961496534,
		"fullConversationHeadersOnly": []map[string]any{
			{"bubbleId": "b1", "type": 1},
			{"bubbleId": "b2", "type": 2},
			{"bubbleId": "gone", "type": 1},
		},
	})
	put(fmt.Sprintf("bubbleId:%s:b1", id), map[string]any{"type": 1, "text": "link CONVENTIONS.md to cursor rules"})
	put(fmt.Sprintf("bubbleId:%s:b2", id), map[string]any{"type": 2, "text": "Both files now point at each other."})
	return dir
}

// The migration this exists for is Claude Code → Cursor, in either direction,
// and it was the one ingest could not serve: there was no Cursor reader, and
// the interchange path skipped with "install txcript", a binary a migrating
// user does not have.
func TestACursorChatParsesIntoOrderedTurns(t *testing.T) {
	t.Setenv(transcript.LogosCursorStorageEnv, cursorStorage(t))

	paths, err := transcript.Sessions("cursor")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("discovered %d chats, want 1: %v", len(paths), paths)
	}
	s, err := transcript.ReadFile("cursor", paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(s.Turns) != 2 {
		t.Fatalf("turns = %d, want 2: %+v", len(s.Turns), s.Turns)
	}
	if s.Turns[0].Role != "user" || s.Turns[1].Role != "assistant" {
		t.Errorf("roles = %q, %q, want user then assistant", s.Turns[0].Role, s.Turns[1].Role)
	}
	if s.Turns[0].Text != "link CONVENTIONS.md to cursor rules" {
		t.Errorf("first turn text = %q", s.Turns[0].Text)
	}
	// A message the chat lists but whose row is gone is stepped over and
	// counted, never silently dropped (invariant 4).
	if s.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 for the message with no row", s.Skipped)
	}
	if s.Harness != "cursor" {
		t.Errorf("harness = %q", s.Harness)
	}
}

// Every chat lives in the same SQLite file, so hashing the file would give all
// of them one hash and the ingest queue would take the first and skip the rest
// as already seen.
func TestEachCursorChatHashesToItsOwnContent(t *testing.T) {
	root := cursorStorage(t)
	t.Setenv(transcript.LogosCursorStorageEnv, root)

	paths, err := transcript.Sessions("cursor")
	if err != nil {
		t.Fatal(err)
	}
	s, err := transcript.ReadFile("cursor", paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.Hash == "" {
		t.Fatal("no hash, so ingest cannot tell this chat from any other in the same file")
	}
	fi, err := os.ReadFile(filepath.Join(root, "state.vscdb"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Hash == hashOf(fi) {
		t.Error("the chat hashed to the whole storage file, so every chat in it shares one hash")
	}
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cursorStorageWithTools is the same fixture shape plus the bubbles that carry
// a tool call and no text, which is what nearly half of a real Cursor chat is
// made of. The rawArgs/params pair, and the spelling of each field, are copied
// from Cursor's own storage.
func cursorStorageWithTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.vscdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	put := func(k string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO cursorDiskKV VALUES (?, ?)`, k, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	const id = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	put("composerData:"+id, map[string]any{
		"composerId": id,
		"createdAt":  1775961496534,
		"fullConversationHeadersOnly": []map[string]any{
			{"bubbleId": "b1", "type": 1},
			{"bubbleId": "b2", "type": 2},
			{"bubbleId": "b3", "type": 2},
			{"bubbleId": "b4", "type": 2},
			{"bubbleId": "b5", "type": 2},
		},
	})
	put(fmt.Sprintf("bubbleId:%s:b1", id), map[string]any{"type": 1, "text": "move fleetbuilder out of .venv"})
	// Text and a call in one bubble: the model says what it is about to do and
	// the call is recorded alongside it.
	put(fmt.Sprintf("bubbleId:%s:b2", id), map[string]any{
		"type": 2, "text": "Moving it now.",
		"toolFormerData": map[string]any{
			"name": "run_terminal_cmd", "status": "completed",
			"rawArgs": `{"command": "mv .venv/lib/fleetbuilder.py .", "is_background": "False"}`,
			"params":  `{"command":"mv .venv/lib/fleetbuilder.py .","requireUserApproval":true}`,
		},
	})
	// No text at all — the bubble that used to be dropped.
	put(fmt.Sprintf("bubbleId:%s:b3", id), map[string]any{
		"type": 2,
		"toolFormerData": map[string]any{
			"name": "search_replace", "status": "completed",
			"rawArgs": `{"file_path": "src/com/company/Recur.java", "old_string": "a", "new_string": "b"}`,
			"params":  `{"relativeWorkspacePath":"src/com/company/Recur.java","blocking":true}`,
		},
	})
	// A call the user cancelled. It must not read as something that ran.
	put(fmt.Sprintf("bubbleId:%s:b4", id), map[string]any{
		"type": 2,
		"toolFormerData": map[string]any{
			"name": "run_terminal_cmd", "status": "cancelled",
			"rawArgs": `{"command": "rm -rf build"}`,
		},
	})
	// No status, no result, no error — 27% of the calls in a real database.
	// Nothing recorded what became of it, so nothing may be claimed about it.
	put(fmt.Sprintf("bubbleId:%s:b5", id), map[string]any{
		"type": 2,
		"toolFormerData": map[string]any{
			"name":    "run_terminal_cmd",
			"rawArgs": `{"command": "go test ./..."}`,
		},
	})
	return dir
}

// Every Cursor session on a real machine harvested as one where nothing ran:
// the reader kept only bubbles with text, and a tool call is stored in a bubble
// that usually has none. 65 of 65 chats came back with no commands and no
// files, and the harvest reported that as success.
func TestACursorToolCallIsReadAsAToolTurn(t *testing.T) {
	t.Setenv(transcript.LogosCursorStorageEnv, cursorStorageWithTools(t))

	paths, err := transcript.Sessions("cursor")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	s, err := transcript.ReadFile("cursor", paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var tools []transcript.Turn
	for _, turn := range s.Turns {
		if turn.Role == "tool" {
			tools = append(tools, turn)
		}
	}
	if len(tools) != 4 {
		t.Fatalf("tool turns = %d, want 4: %+v", len(tools), s.Turns)
	}
	// The command comes from params where both spellings carry it.
	if tools[0].Tool != "run_terminal_cmd" || tools[0].Input != "mv .venv/lib/fleetbuilder.py ." {
		t.Errorf("first tool turn = %+v", tools[0])
	}
	if tools[0].Status != "ok" {
		t.Errorf("a completed call should read ok, got %q", tools[0].Status)
	}
	// The text-less bubble, whose path is only in params/rawArgs.
	if tools[1].Tool != "search_replace" || tools[1].Input != "src/com/company/Recur.java" {
		t.Errorf("second tool turn = %+v", tools[1])
	}
	// Cancelled is not success: a harvest that lists it as a command that ran
	// tells the next agent something was tried when it never was.
	if tools[2].Status != "error" {
		t.Errorf("a cancelled call must not read as ok, got %q", tools[2].Status)
	}
	// The bubble that had both text and a call contributes both.
	if s.Turns[1].Role != "assistant" || s.Turns[1].Text != "Moving it now." {
		t.Errorf("a bubble with text and a call should keep its text too: %+v", s.Turns)
	}
}

// Cursor stamps its chats in milliseconds and every other reader produces
// seconds. Passing the raw value through made a Cursor session's clock run a
// thousand times fast: unsaved.go's "did this transcript run while the server
// was alive" guard compared 1.7e12 against 1.7e9 and was always true, so a chat
// from months ago would be harvested as this session's work, and a promoted
// checkpoint's TS sorted above every real one forever.
func TestACursorChatsTimestampsAreInSecondsLikeEveryOtherReader(t *testing.T) {
	t.Setenv(transcript.LogosCursorStorageEnv, cursorStorage(t))

	paths, err := transcript.Sessions("cursor")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	s, err := transcript.ReadFile("cursor", paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	const want = 1775961496 // the fixture's createdAt, 1775961496534ms, in seconds
	if s.Started != want {
		t.Errorf("Started = %d, want %d", s.Started, want)
	}
	if s.Ended != want {
		t.Errorf("Ended = %d, want %d", s.Ended, want)
	}
}

// A tool call Cursor recorded no outcome for must not be reported as one that
// failed. harvest marks an errored command "— failed", and failed is the field
// the next agent trusts most: defaulting unknown to error would file a quarter
// of this machine's Cursor history as approaches that were tried and ruled out
// when nothing ever observed them. "" is the same unknown codexStatus returns.
func TestACursorToolCallWithNoRecordedOutcomeIsNotReportedAsFailed(t *testing.T) {
	t.Setenv(transcript.LogosCursorStorageEnv, cursorStorageWithTools(t))

	paths, err := transcript.Sessions("cursor")
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	s, err := transcript.ReadFile("cursor", paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, turn := range s.Turns {
		if turn.Input == "go test ./..." {
			if turn.Status != "" {
				t.Fatalf("a call with no recorded outcome reported status %q, want unknown", turn.Status)
			}
			return
		}
	}
	t.Fatal("the status-less tool call was not read at all")
}
