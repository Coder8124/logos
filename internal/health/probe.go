package health

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/session"
)

// Integration proves the wiring end to end, by being the host.
//
// Registering a server and having a working integration are not the same thing,
// and until now nothing checked the difference: setup wrote config files, said
// four hosts were connected, and the first evidence to the contrary arrived
// inside Claude or Cursor as an unhelpful connection error.
//
// The obvious probe — initialize, then tools/list — is not enough. A server
// pointed at the *wrong vault* passes both perfectly while knowing nothing
// about the user's work, and that is the failure most worth catching, because
// it looks exactly like brain being useless rather than brain being
// misconfigured. So this writes a disposable checkpoint, reads it back through
// resume, and confirms the file landed in the vault that was configured — then
// removes it.
//
// Needs no model: every tool it exercises is markdown and SQL.
func Integration(bin, vault string) []Check {
	var checks []Check
	fail := func(name, detail, fix string) []Check {
		return append(checks, Check{Name: name, State: Failed, Detail: detail, Fix: fix})
	}

	// A name no real project would collide with, carrying the pid so two probes
	// at once cannot tread on each other.
	project := fmt.Sprintf("brain-selftest-%d", os.Getpid())

	cmd := exec.Command(bin, "mcp", "serve")
	cmd.Env = append(os.Environ(), "BRAIN_VAULT="+vault)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fail("launch", err.Error(), "")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail("launch", err.Error(), "")
	}
	// stderr is where the server announces a missing runtime; letting it inherit
	// would corrupt nothing but would confuse the report, so it is discarded.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fail("launch", "could not start "+bin+": "+err.Error(),
			"check the path in your host config")
	}
	defer func() {
		stdin.Close()
		// The server exits when stdin closes; kill only if it does not.
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
		}
	}()

	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	send := func(v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = stdin.Write(append(raw, '\n'))
		return err
	}
	await := func(id int, d time.Duration) (map[string]any, bool) {
		deadline := time.After(d)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					return nil, false
				}
				var msg map[string]any
				if json.Unmarshal([]byte(line), &msg) != nil {
					continue
				}
				if n, ok := msg["id"].(float64); !ok || int(n) != id {
					continue
				}
				return msg, true
			case <-deadline:
				return nil, false
			}
		}
	}
	call := func(id int, tool string, args map[string]any) (string, bool) {
		if err := send(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": args},
		}); err != nil {
			return "", false
		}
		msg, ok := await(id, 15*time.Second)
		if !ok {
			return "", false
		}
		raw, _ := json.Marshal(msg)
		return string(raw), true
	}

	// 1. The handshake.
	if err := send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "brain-doctor", "version": "1"},
		},
	}); err != nil {
		return fail("handshake", err.Error(), "")
	}
	if _, ok := await(1, 10*time.Second); !ok {
		return fail("handshake", "no response to initialize",
			"run `"+bin+" mcp serve` by hand to see why")
	}
	checks = append(checks, Check{Name: "handshake", State: OK, Detail: "server answered initialize"})

	// 2. The tools a handoff needs must actually be advertised.
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}); err != nil {
		return fail("tools", err.Error(), "")
	}
	msg, ok := await(2, 10*time.Second)
	if !ok {
		return fail("tools", "no response to tools/list", "")
	}
	listed, _ := json.Marshal(msg)
	var missing []string
	for _, want := range []string{"checkpoint", "resume", "before_you_try", "context"} {
		if !strings.Contains(string(listed), `"`+want+`"`) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return fail("tools", "not advertised: "+strings.Join(missing, ", "),
			"the binary may be an older build")
	}
	checks = append(checks, Check{Name: "tools", State: OK, Detail: "continuity tools advertised"})

	// 3. A real write, and a real read-back. This is the part that catches a
	//    server pointed at somebody else's vault.
	marker := "selftest ruled this out at " + time.Now().Format(time.RFC3339)
	if _, ok := call(3, "checkpoint", map[string]any{
		"project": project,
		"task":    "verifying the brain integration",
		"state":   "probe in progress",
		"failed":  []string{marker},
		"next":    "delete this checkpoint",
		"agent":   "brain-doctor",
	}); !ok {
		return fail("write", "checkpoint did not complete",
			"the vault may not be writable by the host's user")
	}

	body, ok := call(4, "resume", map[string]any{"project": project})
	if !ok {
		return fail("read-back", "resume did not complete", "")
	}
	if !strings.Contains(body, "selftest ruled this out") {
		return fail("read-back", "resume did not return what checkpoint just wrote",
			"the server may be reading a different vault than it writes")
	}
	checks = append(checks, Check{Name: "round trip", State: OK,
		Detail: "checkpoint written and recovered through resume"})

	// 4. And it must be on disk, in the vault that was configured — not merely
	//    in some database the server happened to open.
	found := findCheckpoint(vault, project)
	if found == "" {
		return fail("vault", "the checkpoint is not in "+vault,
			"the host is pointed at a different BRAIN_VAULT than you think")
	}
	if err := cleanUp(vault, project, found); err != nil {
		return fail("vault", "written to "+vault+", but the probe could not clean up after itself: "+err.Error(),
			"delete "+filepath.Join(vault, session.CheckpointDir, project)+" by hand")
	}
	checks = append(checks, Check{Name: "vault", State: OK,
		Detail: "written to " + vault + ", and cleaned up"})

	return checks
}

// cleanUp removes the probe's checkpoint and the project directory it was
// filed under.
//
// Removing only the file is not enough, and the difference is not cosmetic. A
// project is a directory under sessions/, so an empty brain-selftest-<pid>
// directory is a project as far as `brain continuity` and `brain projects` are
// concerned — one that has never checkpointed and never will. Every run of
// `brain doctor --integration` added another, permanently, to the one report
// whose job is to say which projects have gone quiet. Three of them were in the
// author's own vault before anyone noticed, under a check that said "cleaned
// up".
func cleanUp(vault, project, checkpoint string) error {
	if err := os.Remove(checkpoint); err != nil {
		return err
	}
	dir := filepath.Join(vault, session.CheckpointDir, project)
	// Anything else in here was not written by the probe, so the directory
	// stays and the caller is told — losing somebody's notes to a health check
	// is far worse than leaving a stray directory behind.
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// findCheckpoint walks rather than lists: checkpoints are filed under
// sessions/<project>/<id>.md, so reading only the top level of sessions/ finds
// nothing and concludes the vault is wrong.
func findCheckpoint(vault, project string) string {
	var found string
	filepath.WalkDir(filepath.Join(vault, session.CheckpointDir),
		func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), project) {
				found = path
				return filepath.SkipAll
			}
			return nil
		})
	return found
}
