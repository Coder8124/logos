//go:build chaos

package chaos

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests cover every write path, not just checkpoint: a store that is
// durable on its most-tested command and silently lossy on `note` or `tried`
// has the same promise broken, just less often looked at.

// quiet runs the binary with embeddings off. Each index otherwise probes and
// embeds against whatever local runtime the developer has running, which
// makes the tier minutes slower and its timing depend on the machine, without
// testing anything about durability.
func quiet(t *testing.T, vault, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LOGOS_VAULT="+vault, "LOGOS_AGENT=chaos", "LOGOS_EMBED=off")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func mustRun(t *testing.T, vault string, args ...string) string {
	t.Helper()
	out, err := quiet(t, vault, "", args...)
	if err != nil {
		t.Fatalf("logos %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// writePaths is every CLI command that writes markdown, each given a text
// that later proves whether that write survived.
var writePaths = []struct {
	name string
	args func(text string) []string
}{
	{"checkpoint", func(s string) []string { return []string{"checkpoint", "kestrel", "--next", s} }},
	{"note", func(s string) []string { return []string{"note", "kestrel", s} }},
	{"tried", func(s string) []string {
		return []string{"tried", s, "--project", "kestrel", "--ruled-out", "it did not hold"}
	}},
	{"memory add", func(s string) []string { return []string{"memory", "add", s} }},
}

// markdown returns every vault file outside .logos, keyed by its
// vault-relative path. That is the part of the vault that is truth; .logos is
// the cache and may legitimately change.
func markdown(t *testing.T, vault string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(vault, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(vault, p)
		if d.IsDir() {
			if rel == ".logos" {
				return filepath.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// tempLitter is any WriteAtomic temp file left behind. A failed write that
// leaves one has half-written the vault, even if nothing reads it yet: the
// next sync client uploads it.
func tempLitter(t *testing.T, vault string) []string {
	t.Helper()
	var left []string
	filepath.WalkDir(vault, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".tmp") {
			left = append(left, p)
		}
		return nil
	})
	return left
}

// countIn reports how many markdown files outside .logos contain text.
func countIn(files map[string]string, text string) int {
	n := 0
	for _, body := range files {
		if strings.Contains(body, text) {
			n++
		}
	}
	return n
}

// chmodTree makes every directory under root 0555 and every file 0444,
// skipping skip (when non-empty), and returns the undo. Only the leaves being
// read-only is not a read-only vault: an existing writable subdirectory is a
// way in, which is how TestReadOnlyVault once proved nothing.
func chmodTree(t *testing.T, root, skip string) func() {
	t.Helper()
	type entry struct {
		path string
		mode os.FileMode
	}
	var changed []entry
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip != "" && p == skip {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if d.IsDir() {
			mode = 0o555
		}
		changed = append(changed, entry{p, info.Mode().Perm()})
		return os.Chmod(p, mode)
	})
	return func() {
		// A 0555 directory can still be traversed, so its children can be
		// restored in any order.
		for _, e := range changed {
			os.Chmod(e.path, e.mode)
		}
	}
}

func TestEveryWriteToAReadOnlyVaultReportsItsFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, where permissions do not stop a write")
	}
	cases := []struct {
		name string
		// cacheWritable is the case where only the markdown is read-only: the
		// index write succeeds and the vault write fails, which is the
		// ordering that can return a success-shaped result if anything does.
		cacheWritable bool
	}{
		{"the markdown read-only, the cache writable", true},
		{"the whole vault read-only", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vault := newVault(t)
			mustRun(t, vault, "checkpoint", "kestrel", "--next", "seed checkpoint")
			mustRun(t, vault, "note", "kestrel", "seed note")
			for i := 1; i <= 3; i++ {
				mustRun(t, vault, "memory", "add", fmt.Sprintf("seed fact %d", i))
			}
			before := markdown(t, vault)

			skip := ""
			if c.cacheWritable {
				skip = filepath.Join(vault, ".logos")
			}
			undo := chmodTree(t, vault, skip)
			attempts := [][]string{
				{"checkpoint", "kestrel", "--next", "read-only checkpoint"},
				{"note", "kestrel", "read-only note"},
				{"tried", "read-only route", "--project", "kestrel", "--ruled-out", "the disk said no"},
				{"memory", "add", "read-only fact"},
				{"memory", "pin", "1"},
				{"memory", "correct", "2", "a corrected seed fact"},
				{"memory", "forget", "3"},
			}
			for _, args := range attempts {
				out, err := quiet(t, vault, "", args...)
				if err == nil {
					t.Errorf("logos %s on a read-only vault reported success:\n%s", strings.Join(args, " "), out)
				}
				if strings.Contains(out, "panic") {
					t.Errorf("logos %s panicked on a read-only vault:\n%s", strings.Join(args, " "), out)
				}
			}
			undo()

			if left := tempLitter(t, vault); len(left) > 0 {
				t.Errorf("a refused write left temp files in the vault: %v", left)
			}
			after := markdown(t, vault)
			for path, body := range before {
				if after[path] != body {
					t.Errorf("%s changed although every write was refused", path)
				}
			}
			for path := range after {
				if _, ok := before[path]; !ok {
					t.Errorf("%s appeared although every write was refused", path)
				}
			}

			out := mustRun(t, vault, "index")
			if c.cacheWritable {
				// The cache took the note and the memory the vault refused.
				// The command said so and exited non-zero; the next index is
				// what keeps that from being a loss, and it must say it did.
				files := markdown(t, vault)
				for _, text := range []string{"read-only note", "read-only fact"} {
					if countIn(files, text) == 0 {
						t.Errorf("%q reached the cache but the next index did not write it to the vault:\n%s", text, out)
					}
				}
				if !strings.Contains(out, "only in the index") && !strings.Contains(out, "restored") {
					t.Errorf("index wrote cache-only work back to the vault without saying so:\n%s", out)
				}
			}
		})
	}
}

func TestAFullDiskFailsEveryWriteOutLoud(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("uses hdiutil; macOS only")
	}
	mount, cleanup := smallDisk(t, 8)
	defer cleanup()
	vault := filepath.Join(mount, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, vault, "checkpoint", "kestrel", "--next", "seed checkpoint")
	mustRun(t, vault, "note", "kestrel", "seed note")
	mustRun(t, vault, "memory", "add", "seed fact")
	before := markdown(t, vault)

	// Same fill as TestFullDisk, and for the same reason: stopping at 1MB
	// blocks leaves room for a small markdown file and measures nothing.
	filler := filepath.Join(mount, "filler")
	f, err := os.Create(filler)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1 << 20, 4096, 512} {
		block := bytes.Repeat([]byte("x"), size)
		for {
			if _, err := f.Write(block); err != nil {
				break
			}
		}
	}
	f.Sync()
	f.Close()

	// memory add on a full disk is TestFullDisk; the rest are here.
	for _, w := range writePaths[:3] {
		args := w.args("written to a full disk by " + w.name)
		out, err := quiet(t, vault, "", args...)
		if err == nil {
			t.Errorf("logos %s on a full disk reported success:\n%s", w.name, out)
		} else {
			t.Logf("%s on a full disk: %s", w.name, strings.TrimSpace(out))
		}
	}

	os.Remove(filler)
	if left := tempLitter(t, vault); len(left) > 0 {
		t.Errorf("a write that ran out of space left temp files in the vault: %v", left)
	}
	after := markdown(t, vault)
	for path, body := range before {
		if !strings.HasPrefix(after[path], strings.TrimRight(body, "\n")) {
			t.Errorf("%s lost what it held before the disk filled", path)
		}
	}
	mustRun(t, vault, "index")
	if out := mustRun(t, vault, "show", "kestrel", "--history"); !strings.Contains(out, "seed checkpoint") {
		t.Errorf("the seed checkpoint is gone after a full disk:\n%s", out)
	}
}

func TestSyncConflictCopiesAreSetAsideAndNamed(t *testing.T) {
	vault := newVault(t)
	mustRun(t, vault, "checkpoint", "kestrel", "--next", "the only checkpoint")
	mustRun(t, vault, "tried", "the only route", "--project", "kestrel", "--ruled-out", "it failed once")
	mustRun(t, vault, "memory", "add", "the only fact")

	// Each sync client names its copy differently, and a copy of a checkpoint
	// is byte-identical, so a reader that walks the directory counts it twice.
	var checkpoint string
	filepath.WalkDir(filepath.Join(vault, "sessions", "kestrel"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, "-chaos.md") {
			checkpoint = p
		}
		return nil
	})
	if checkpoint == "" {
		t.Fatal("the seed checkpoint was not written where expected")
	}
	raw, err := os.ReadFile(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	stem := strings.TrimSuffix(checkpoint, ".md")
	for _, copyPath := range []string{
		stem + " 2.md", // iCloud
		stem + " (Pragun's MacBook's conflicted copy 2026-10-28).md", // Dropbox
	} {
		if err := os.WriteFile(copyPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	notes := filepath.Join(vault, "sessions", "kestrel", "uncommitted.md")
	if raw, err := os.ReadFile(notes); err == nil {
		os.WriteFile(filepath.Join(vault, "sessions", "kestrel", "uncommitted 2.md"), raw, 0o644)
	}
	var memory string
	filepath.WalkDir(filepath.Join(vault, "memories"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(filepath.Dir(p)) != "memories" {
			memory = p
		}
		return nil
	})
	if memory == "" {
		t.Fatal("the seed memory has no per-memory file")
	}
	raw, err = os.ReadFile(memory)
	if err != nil {
		t.Fatal(err)
	}
	// The copy is edited, so a reader that lets it win is visible.
	edited := strings.Replace(string(raw), "the only fact", "the edited copy", 1)
	os.WriteFile(strings.TrimSuffix(memory, ".md")+".sync-conflict-20261028-101500-ABC1234.md", []byte(edited), 0o644)

	os.RemoveAll(filepath.Join(vault, ".logos"))
	out := mustRun(t, vault, "index")
	if !strings.Contains(out, "set aside 4 sync conflict copies") {
		t.Errorf("index did not name the four conflict copies it set aside:\n%s", out)
	}

	if out := mustRun(t, vault, "show", "kestrel", "--history"); strings.Count(out, "the only checkpoint") != 1 {
		t.Errorf("a conflict copy is listed as a second checkpoint:\n%s", out)
	}
	if out := mustRun(t, vault, "tried", "the only route", "--project", "kestrel"); !strings.Contains(out, "One recorded dead end") {
		t.Errorf("a conflict copy is cited as a second dead end:\n%s", out)
	}
	out = mustRun(t, vault, "memory")
	if !strings.Contains(out, "1 memories") && !strings.Contains(out, "1 memory") {
		t.Errorf("a conflict copy is counted as a second memory:\n%s", out)
	}
	if strings.Contains(out, "the edited copy") || !strings.Contains(out, "the only fact") {
		t.Errorf("a conflict copy overrode the memory it copied:\n%s", out)
	}
	if out, err := quiet(t, vault, "", "resume", "kestrel"); err != nil || strings.Contains(out, "panic") {
		t.Errorf("resume fails on a vault holding conflict copies: %v\n%s", err, out)
	}
}

func TestTwoWorktreesCheckpointingAtOnceLoseNothing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	main := filepath.Join(root, "kestrel")
	linked := filepath.Join(root, "kestrel-feature")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=chaos", "-c", "user.email=chaos@localhost"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	os.MkdirAll(main, 0o755)
	git(main, "init", "-q")
	os.WriteFile(filepath.Join(main, "README"), []byte("kestrel\n"), 0o644)
	git(main, "add", "README")
	git(main, "commit", "-qm", "seed")
	git(main, "worktree", "add", "-q", linked, "-b", "feature")

	vault := newVault(t)
	// Several writers per worktree finishing inside the same second is the
	// case that matters: checkpoint files are named to the second, so two
	// sessions ending together race for one filename.
	const perWriter = 3
	var (
		mu     sync.Mutex
		acked  []string
		failed []string
		wg     sync.WaitGroup
	)
	for _, dir := range []string{main, linked} {
		for w := 0; w < 3; w++ {
			wg.Add(1)
			go func(dir string, w int) {
				defer wg.Done()
				for j := 0; j < perWriter; j++ {
					text := fmt.Sprintf("%s writer %d checkpoint %d", filepath.Base(dir), w, j)
					out, err := quiet(t, vault, dir, "checkpoint", "--next", text)
					mu.Lock()
					if err != nil {
						failed = append(failed, fmt.Sprintf("%s: %v: %s", text, err, strings.TrimSpace(out)))
					} else {
						acked = append(acked, text)
					}
					mu.Unlock()
				}
			}(dir, w)
		}
	}
	wg.Wait()
	for _, f := range failed {
		t.Errorf("a concurrent checkpoint failed: %s", f)
	}

	files := markdown(t, vault)
	for _, text := range acked {
		// Matched with the line ending so "checkpoint 1" does not also count
		// a hypothetical "checkpoint 10".
		if n := countIn(files, text+"\n"); n != 1 {
			t.Errorf("acknowledged checkpoint %q is in %d files, want exactly 1", text, n)
		}
	}
	checkSessions(t, vault)
	if left := tempLitter(t, vault); len(left) > 0 {
		t.Errorf("racing checkpoints left temp files: %v", left)
	}
}

func TestAKilledWriteOnAnyPathIsRecoveredByTheNextIndex(t *testing.T) {
	for _, w := range writePaths {
		t.Run(w.name, func(t *testing.T) {
			vault := newVault(t)
			var acked []string
			for i := 0; i < 12; i++ {
				text := fmt.Sprintf("%s acknowledged %d.", w.name, i)
				mustRun(t, vault, w.args(text)...)
				acked = append(acked, text)

				// The kill lands at a different point each time: before the
				// cache write, between the cache and the vault, inside the
				// rename. Which one is hit is up to the scheduler; across a
				// dozen delays most of them are.
				cmd := exec.Command(bin(t), w.args(fmt.Sprintf("%s killed %d.", w.name, i))...)
				cmd.Env = append(os.Environ(), "LOGOS_VAULT="+vault, "LOGOS_AGENT=chaos", "LOGOS_EMBED=off")
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Duration(i*3) * time.Millisecond)
				cmd.Process.Kill()
				cmd.Wait()
			}

			// A kill inside WriteAtomic leaves its temp file. Index only sweeps
			// ones old enough that no live write can own them, so they are aged
			// here to stand for a crash from an earlier day.
			litter := tempLitter(t, vault)
			long := time.Now().Add(-2 * time.Hour) // past vault.StaleTempAge
			for _, p := range litter {
				os.Chtimes(p, long, long)
			}

			// Recover twice: once over the existing cache, once from nothing,
			// which is the promise every document makes.
			for _, rebuild := range []bool{false, true} {
				if rebuild {
					os.RemoveAll(filepath.Join(vault, ".logos"))
				}
				out, err := quiet(t, vault, "", "index")
				if err != nil || strings.Contains(out, "panic") {
					t.Fatalf("index after killed writes (rebuild=%v): %v\n%s", rebuild, err, out)
				}
				if !rebuild && len(litter) > 0 && !strings.Contains(out, fmt.Sprintf("removed %d temp file", len(litter))) {
					t.Errorf("%d temp files were left by killed writes and index did not say it removed them:\n%s", len(litter), out)
				}
				files := markdown(t, vault)
				for _, text := range acked {
					if countIn(files, text) == 0 {
						t.Errorf("acknowledged %q is gone after killed writes and an index (rebuild=%v)", text, rebuild)
					}
				}
			}
			if w.name == "memory add" {
				out := mustRun(t, vault, "memory")
				for _, text := range acked {
					if !strings.Contains(out, text) {
						t.Errorf("acknowledged memory %q is not listed after a rebuild:\n%s", text, out)
					}
				}
			}
			if left := tempLitter(t, vault); len(left) > 0 {
				t.Errorf("temp files from killed writes survived the index: %v", left)
			}
			checkSessions(t, vault)
		})
	}
}

// checkSessions is checkFiles over every session directory, minus the one
// markdown file there that has no frontmatter by design: uncommitted.md is a
// heading and a list, so it is held to starting with its heading instead.
func checkSessions(t *testing.T, vault string) {
	t.Helper()
	filepath.WalkDir(filepath.Join(vault, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("unreadable: %s: %v", p, err)
			return nil
		}
		want := "---\n"
		if d.Name() == "uncommitted.md" {
			want = "# uncommitted"
		}
		if !strings.HasPrefix(string(raw), want) {
			t.Errorf("%s does not start with %q — a torn write is visible", p, want)
		}
		return nil
	})
}
