package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Coder8124/logos/internal/deadend"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/procedure"
	"github.com/Coder8124/logos/internal/session"
	usagepkg "github.com/Coder8124/logos/internal/usage"
)

// A dead end handed back before a retry was counted, and nothing said whether
// the session then stayed away from it. The receipt claims only what Logos can
// see: a ruling it was shown, naming a file the session then never edited. A
// ruling whose file it edited anyway is not claimed, and a second end of the
// same session claims nothing twice.
func TestASessionShownARulingThatNeverTouchedItsFilesMayHaveAvoidedIt(t *testing.T) {
	standIn(t, "elsewhere")
	commitRulings(t, &session.Checkpoint{
		Project: "shop", Agent: "codex", Task: "speed up checkout", TS: time.Now().Add(-time.Hour).Unix(),
		Failed: []string{
			"caching the cart — internal/cart/cache.go evicts on every write, so it never hits",
			"inlining the tax table — tax/rates.go is generated and the next build overwrites it",
			"a bigger connection pool — the database was never the bottleneck",
		},
	})
	rulings, err := deadend.Collect(vaultPath(), nil, "shop")
	if err != nil || len(rulings) != 3 {
		t.Fatalf("collect: %v, %d rulings", err, len(rulings))
	}
	// What before_you_try records while the tool runs, then the row the hook
	// writes as it returns.
	if err := usagepkg.Record(vaultPath(), usagepkg.Event{
		Kind: usagepkg.KindDeadEnd, Via: "mcp:before_you_try", Project: "shop",
		Rulings: len(rulings), Refs: usagepkg.RulingRefs(rulings),
	}); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", `{"session_id":"s-receipt","tool_name":"mcp__plugin_logos_logos__before_you_try","tool_input":{"approach":"cache the cart"}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-receipt","tool_name":"Edit","tool_input":{"file_path":"/work/shop/tax/rates.go"}}`)

	out := endSession(t, "s-receipt")
	if !strings.Contains(out, "may have avoided: caching the cart") || !strings.Contains(out, "ruled out by codex") {
		t.Errorf("the ruling the session stayed away from is not on the receipt:\n%s", out)
	}
	if strings.Contains(out, "inlining the tax table") {
		t.Errorf("a ruling whose file the session edited was claimed as avoided:\n%s", out)
	}
	if strings.Contains(out, "connection pool") {
		t.Errorf("a ruling that names no file was claimed as avoided:\n%s", out)
	}
	if !strings.HasPrefix(out, "auto-checkpointed") {
		t.Errorf("the receipt came before the line the hook sorts its outcome by:\n%s", out)
	}

	if again := endSession(t, "s-receipt"); strings.Contains(again, "may have avoided") {
		t.Errorf("a session ended twice claimed the same ruling twice:\n%s", again)
	}
	events, _, err := usagepkg.Read(vaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := usagepkg.Sum(events, "").Avoided; got != 1 {
		t.Errorf("the ledger counts %d avoided, want 1", got)
	}
}

// The ledger is the vault's, not the session's: `logos tried` run in another
// terminal at the same moment lands in it too. Only a check this session's own
// tool call made is on its receipt.
func TestACheckThisSessionNeverMadeIsNotOnItsReceipt(t *testing.T) {
	standIn(t, "elsewhere")
	commitRulings(t, &session.Checkpoint{
		Project: "shop", Agent: "codex", Task: "speed up checkout", TS: time.Now().Add(-time.Hour).Unix(),
		Failed: []string{"caching the cart — internal/cart/cache.go evicts on every write, so it never hits"},
	})
	rulings, _ := deadend.Collect(vaultPath(), nil, "shop")
	if err := usagepkg.Record(vaultPath(), usagepkg.Event{
		Kind: usagepkg.KindDeadEnd, Via: "cli:tried", Project: "shop",
		Rulings: len(rulings), Refs: usagepkg.RulingRefs(rulings),
	}); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", `{"session_id":"s-other","tool_name":"Bash","tool_input":{"command":"go test ./cart"}}`)

	if out := endSession(t, "s-other"); strings.Contains(out, "may have avoided") {
		t.Errorf("a check another session made is on this one's receipt:\n%s", out)
	}
}

func commitRulings(t *testing.T, c *session.Checkpoint) {
	t.Helper()
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if err := session.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ix.DB, vaultPath(), c); err != nil {
		t.Fatal(err)
	}
}

// The receipt keys what a resume handed over on the session that read it, and
// a resume run as "cli" is never counted as a handoff. The hook is the only
// thing that knows both, so it has to pass them on.
func TestTheSessionStartHookTellsResumeWhichSessionAndAgentItIs(t *testing.T) {
	hook, err := filepath.Abs("../../plugin/hooks/session-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin, seen := t.TempDir(), filepath.Join(t.TempDir(), "seen")
	fakeProgram(t, bin, "logos", `case "$1" in
--version) echo "logos 0.4.99" ;;
project-name) echo kestrel-one ;;
resume) printf '%s %s' "${LOGOS_SESSION-unset}" "${LOGOS_AGENT-unset}" > '`+seen+`'; echo "## Where we left off" ;;
esac`)
	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir(), noFixedInstallDirs}
	cmd.Stdin = strings.NewReader(`{"session_id": "6f1c2a9e-3b7d", "source": "startup"}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the hook never ran resume: %v", err)
	}
	if string(got) != "6f1c2a9e-3b7d claude-code" {
		t.Errorf("resume ran with session and agent %q", got)
	}
}

// Reading the payload is new, and a host that never closes the hook's stdin
// must not turn it into a session start that waits forever.
func TestTheSessionStartHookDoesNotWaitOnAStdinNobodyCloses(t *testing.T) {
	hook, err := filepath.Abs("../../plugin/hooks/session-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin := fakeLogosForHook(t, "true", "", "## Where we left off\nLast checkpoint by **cli**.")
	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir(), noFixedInstallDirs}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the hook waited %s on an open stdin", took)
	}
	if !strings.Contains(string(out), "Where we left off") {
		t.Errorf("the hook lost the handoff:\n%s", out)
	}
}

// A receipt printed into a pipe the hook throws away is a receipt nobody
// gets; the hook says it on stderr, where the user sees the session end.
func TestTheSessionEndHookSaysTheReceipt(t *testing.T) {
	hook, err := filepath.Abs("../../plugin/hooks/session-end.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	fakeProgram(t, bin, "logos", `case "$1" in
--version) echo "logos 0.4.99" ;;
project-name) echo shop ;;
note) echo "auto-checkpointed — sessions/shop/x"; echo "may have avoided: caching the cart (ruled out by codex, 1 Oct 2026)" ;;
esac`)
	cmd := exec.Command("/bin/bash", hook)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CLAUDE_PROJECT_DIR=" + t.TempDir(), noFixedInstallDirs}
	cmd.Stdin = strings.NewReader(`{"session_id":"s-hook"}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "automatic checkpoint") {
		t.Errorf("the receipt line broke the outcome the hook sorts by:\n%s", out)
	}
	if !strings.Contains(string(out), "Logos: may have avoided: caching the cart (ruled out by codex") {
		t.Errorf("the hook did not say the receipt:\n%s", out)
	}
}

// The activity row keeps 160 characters of a tool call, so a long path is cut
// before the file's name. A cut path that could not be matched read as the
// session never touching the file, and the receipt claimed the ruling avoided
// in a session that edited exactly that file.
func TestAnEditWhosePathWasCutShortIsNotTakenForStayingAway(t *testing.T) {
	standIn(t, "elsewhere")
	checkCart(t, "shop")
	hookEvent(t, "PostToolUse", `{"session_id":"s-long","tool_name":"mcp__plugin_logos_logos__before_you_try","tool_input":{"approach":"cache the cart"}}`)
	long := "/work/shop/" + strings.Repeat("vendored/", 20) + "internal/cart/cache.go"
	hookEvent(t, "PostToolUse", `{"session_id":"s-long","tool_name":"Edit","tool_input":{"file_path":"`+long+`"}}`)

	if out := endSession(t, "s-long"); strings.Contains(out, "may have avoided") {
		t.Errorf("an edit whose path was cut short was taken for staying away:\n%s", out)
	}
}

// Two sessions on different projects can run before_you_try in the same
// minute; the ledger line of one is not the other's check.
func TestACheckRecordedForAnotherProjectIsNotOnThisSessionsReceipt(t *testing.T) {
	standIn(t, "elsewhere")
	checkCart(t, "kestrel")
	hookEvent(t, "PostToolUse", `{"session_id":"s-near","tool_name":"mcp__plugin_logos_logos__before_you_try","tool_input":{"approach":"cache the cart"}}`)

	if out := endSession(t, "s-near"); strings.Contains(out, "may have avoided") {
		t.Errorf("a check another project's session made is on this one's receipt:\n%s", out)
	}
}

// The hook says the receipt only beside a session end that worked. One worked
// out before a failed checkpoint was recorded in the ledger and said nowhere,
// and the next end of the session, finding it already credited, never said
// it either.
func TestAReceiptIsNotRecordedWhenTheSessionEndFailed(t *testing.T) {
	standIn(t, "elsewhere")
	checkCart(t, "shop")
	hookEvent(t, "PostToolUse", `{"session_id":"s-fail","tool_name":"mcp__plugin_logos_logos__before_you_try","tool_input":{"approach":"cache the cart"}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-fail","tool_name":"Bash","tool_input":{"command":"go test ./cart"}}`)
	// A file where the project's session directory goes: the checkpoint cannot
	// be written.
	if err := os.WriteFile(filepath.Join(vaultPath(), "sessions", "shop"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOGOS_NOTE_IF_UNCOMMITTED", "1")
	t.Setenv("LOGOS_AUTO_CHECKPOINT", "1")
	restore := setStdin(t, []byte(`{"session_id":"s-fail","reason":"prompt_input_exit"}`))
	out := captureStdout(t, func() {
		if err := runNote([]string{"shop", "claude-code session ended"}); err == nil {
			t.Error("the session end succeeded with no way to write the checkpoint")
		}
	})
	restore()
	if strings.Contains(out, "may have avoided") {
		t.Errorf("a failed session end printed a receipt the hook throws away:\n%s", out)
	}
	events, _, err := usagepkg.Read(vaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := usagepkg.Sum(events, "").Avoided; got != 0 {
		t.Errorf("a failed session end recorded %d avoided in the ledger, want 0", got)
	}
}

// checkCart records one ruling about internal/cart/cache.go, in project
// kestrel, as handed back by a before_you_try run in project.
func checkCart(t *testing.T, project string) {
	t.Helper()
	commitRulings(t, &session.Checkpoint{
		Project: "kestrel", Agent: "codex", Task: "speed up checkout", TS: time.Now().Add(-time.Hour).Unix(),
		Failed: []string{"caching the cart — internal/cart/cache.go evicts on every write, so it never hits"},
	})
	rulings, err := deadend.Collect(vaultPath(), nil, "kestrel")
	if err != nil || len(rulings) != 1 {
		t.Fatalf("collect: %v, %d rulings", err, len(rulings))
	}
	if err := usagepkg.Record(vaultPath(), usagepkg.Event{
		Kind: usagepkg.KindDeadEnd, Via: "mcp:before_you_try", Project: project,
		Rulings: len(rulings), Refs: usagepkg.RulingRefs(rulings),
	}); err != nil {
		t.Fatal(err)
	}
}

// A procedure recorded as working once stayed at once however many sessions
// then followed it and watched its verify pass. The session-end receipt is
// where that is seen: a procedure before_you_try handed back whose verify ran
// and passed afterwards in the same session is proven, once a session, and
// the receipt says so. One whose verify the session never ran is not, and a
// cd in a verify only says where the rest ran.
func TestAProcedureWhoseVerifyPassedAfterItWasShownIsVerified(t *testing.T) {
	standIn(t, "elsewhere")
	t.Setenv("LOGOS_EMBED", "off")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	texts := []string{
		"route: run migrations with the lock flag | trap: two deploys at once corrupt the schema table | verify: cd db && make migrate-check | evidence: once",
		"route: rebuild the search index after a schema change | trap: queries miss new columns | verify: go vet ./search | evidence: once",
	}
	var hits []procedure.Hit
	for _, text := range texts {
		if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{Text: text, Kind: memory.Procedure, Source: "mcp", Project: "shop"}); err != nil {
			t.Fatal(err)
		}
		hits = append(hits, procedure.Hit{Memory: text})
	}
	ix.Close()
	if err := usagepkg.Record(vaultPath(), usagepkg.Event{
		Kind: usagepkg.KindProcedure, Via: "mcp:before_you_try", Project: "shop", Refs: usagepkg.ProcedureRefs(hits),
	}); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", `{"session_id":"s-proven","tool_name":"mcp__plugin_logos_logos__before_you_try","tool_input":{"approach":"run the migrations"}}`)
	hookEvent(t, "PostToolUse", `{"session_id":"s-proven","tool_name":"Bash","tool_input":{"command":"cd db && make migrate-check"}}`)

	out := endSession(t, "s-proven")
	if !strings.Contains(out, "procedure verified: run migrations with the lock flag") || !strings.Contains(out, "once → verified") {
		t.Errorf("the procedure whose verify passed is not on the receipt:\n%s", out)
	}
	if strings.Contains(out, "rebuild the search index") {
		t.Errorf("a procedure whose verify never ran was claimed as verified:\n%s", out)
	}
	if again := endSession(t, "s-proven"); strings.Contains(again, "procedure verified") {
		t.Errorf("a session ended twice proved the same procedure twice:\n%s", again)
	}
	events, _, err := usagepkg.Read(vaultPath())
	if err != nil {
		t.Fatal(err)
	}
	tallies := usagepkg.Tallies(events)
	if got := tallies[usagepkg.Ref(texts[0])]; got.Shown != 1 || got.Proven != 1 {
		t.Errorf("the ledger holds %+v for the proven procedure, want shown 1 and proven 1", got)
	}
	if got := tallies[usagepkg.Ref(texts[1])]; got.Proven != 0 {
		t.Errorf("the ledger holds %+v for the procedure never verified, want proven 0", got)
	}
}

// A verify that failed after the procedure was shown is the opposite of
// evidence for it.
func TestAProcedureWhoseVerifyFailedIsNotVerified(t *testing.T) {
	standIn(t, "elsewhere")
	t.Setenv("LOGOS_EMBED", "off")
	ix, err := openEvents()
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Init(ix.DB); err != nil {
		t.Fatal(err)
	}
	text := "route: run migrations with the lock flag | trap: two deploys at once corrupt the schema table | verify: make migrate-check | evidence: once"
	if _, err := memory.Store(ix.DB, nil, "", &memory.Memory{Text: text, Kind: memory.Procedure, Source: "mcp", Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	if err := usagepkg.Record(vaultPath(), usagepkg.Event{
		Kind: usagepkg.KindProcedure, Via: "mcp:before_you_try", Project: "shop", Refs: usagepkg.ProcedureRefs([]procedure.Hit{{Memory: text}}),
	}); err != nil {
		t.Fatal(err)
	}
	hookEvent(t, "PostToolUse", `{"session_id":"s-red","tool_name":"mcp__plugin_logos_logos__before_you_try","tool_input":{"approach":"run the migrations"}}`)
	hookEvent(t, "PostToolUseFailure", `{"session_id":"s-red","tool_name":"Bash","tool_input":{"command":"make migrate-check"}}`)

	if out := endSession(t, "s-red"); strings.Contains(out, "procedure verified") {
		t.Errorf("a procedure whose verify failed was claimed as verified:\n%s", out)
	}
}
