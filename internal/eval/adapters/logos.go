// Package adapters holds one implementation of eval.Adapter per memory system
// under test.
//
// The adapters are the fair-comparison surface. Each one is written to make its
// system look as good as that system can look: logos gets to use checkpoints
// because it has them, and a store with only add() and search() gets the same
// information flattened into prose rather than withheld. Where a system loses,
// it should lose because of what it is, not because of how it was driven here.
package adapters

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/logos/internal/contextpack"
	"github.com/Coder8124/logos/internal/eval"
	"github.com/Coder8124/logos/internal/gitstate"
	"github.com/Coder8124/logos/internal/index"
	"github.com/Coder8124/logos/internal/memory"
	"github.com/Coder8124/logos/internal/provider"
	"github.com/Coder8124/logos/internal/secretary"
	"github.com/Coder8124/logos/internal/session"
	"github.com/Coder8124/logos/internal/vault"
)

// Logos drives this project through the same entry points the MCP server and
// the CLI use: notes and checkpoints go in, contextpack.Build comes out. No
// benchmark-only retrieval path — if the suite scores well, the shipped product
// scores well.
type Logos struct {
	root string // a scratch vault, never the user's
	// repo is the scenario's code, created by its first commit event. Kept
	// outside the vault, as a project's repository is.
	repo  string
	ix    *index.Index
	embed *provider.Provider
	model string

	dirty bool           // vault has unsynced files
	paths map[string]int // title collisions get a suffix rather than an overwrite
}

// NewLogos builds an adapter over a scratch vault. Pass a nil provider to run
// lexical-and-graph only, which is fast and needs no model loaded.
func NewLogos(embed *provider.Provider, model string) (*Logos, error) {
	b := &Logos{embed: embed, model: model}
	return b, b.Reset()
}

func (b *Logos) Name() string {
	if b.embed == nil {
		return "logos (no embed)"
	}
	return "logos"
}

func (b *Logos) Reset() error {
	if b.ix != nil {
		b.ix.Close()
	}
	if b.root != "" {
		os.RemoveAll(b.root)
	}
	if b.repo != "" {
		os.RemoveAll(b.repo)
		b.repo = ""
	}
	root, err := os.MkdirTemp("", "eval-logos-")
	if err != nil {
		return err
	}
	b.root = root
	b.paths = map[string]int{}
	b.dirty = false
	return b.open()
}

func (b *Logos) open() error {
	ix, err := index.Open(b.root)
	if err != nil {
		return err
	}
	b.ix = ix
	for _, init := range []func() error{
		func() error { return memory.Init(ix.DB) },
		func() error { return session.Init(ix.DB) },
		func() error { return secretary.Init(ix.DB) },
	} {
		if err := init(); err != nil {
			return err
		}
	}
	return nil
}

func (b *Logos) Close() error {
	if b.ix != nil {
		b.ix.Close()
	}
	if b.repo != "" {
		os.RemoveAll(b.repo)
	}
	if b.root != "" {
		return os.RemoveAll(b.root)
	}
	return nil
}

func (b *Logos) Write(ev eval.Event) error {
	switch ev.Kind {
	case eval.KindDoc:
		return b.writeDoc(ev)

	case eval.KindNote:
		_, err := session.AddNoteAt(b.ix.DB, ev.Project, ev.Actor, ev.Text, ev.TS)
		return err

	case eval.KindFact, eval.KindMessage:
		// A stated fact and a user turn both become memories. Created is set
		// from the event so anything reasoning about age has a real clock to
		// read.
		_, err := memory.Store(b.ix.DB, b.embed, b.model, &memory.Memory{
			Text: ev.Text, Kind: memory.Fact, Project: ev.Project,
			Source: "manual", Created: ev.TS,
		})
		return err

	case eval.KindCheckpoint:
		c := &session.Checkpoint{
			Project: ev.Project, Agent: ev.Actor, Task: ev.Task,
			State: ev.Text, Decisions: ev.Decisions, Failed: ev.Failed,
			Questions: ev.Questions, Next: ev.Next, TS: ev.TS,
		}
		// The repository the agent stood in, as checkpoint reads it in use.
		// Left to Commit, it would read whatever directory the benchmark runs
		// from.
		if b.repo != "" {
			c.Git = gitstate.Read(b.repo)
		}
		return session.Commit(b.ix.DB, b.root, c)

	case eval.KindCommit:
		return b.commit(ev)
	}
	return fmt.Errorf("unknown event kind %q", ev.Kind)
}

// commit changes one file in the scenario's repository, dated when the event
// happened so the history reads in the scenario's order.
func (b *Logos) commit(ev eval.Event) error {
	if b.repo == "" {
		repo, err := os.MkdirTemp("", "eval-repo-")
		if err != nil {
			return err
		}
		b.repo = repo
		if err := b.git(ev.TS, "init", "-q"); err != nil {
			return err
		}
	}
	path := filepath.Join(b.repo, filepath.FromSlash(ev.Title))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// The message as the content, so every commit to a file changes it.
	if err := os.WriteFile(path, []byte(ev.Text+"\n"), 0o644); err != nil {
		return err
	}
	if err := b.git(ev.TS, "add", "--", ev.Title); err != nil {
		return err
	}
	return b.git(ev.TS, "commit", "-q", "-m", ev.Text)
}

func (b *Logos) git(ts int64, args ...string) error {
	args = append([]string{"-c", "user.name=eval", "-c", "user.email=eval@localhost", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", args...)
	cmd.Dir = b.repo
	date := fmt.Sprintf("@%d +0000", ts)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

// writeDoc lays a document into the vault the way a person would: the project's
// own page under projects/, everything else under topics/, named by its title
// so that [[wikilinks]] in other notes resolve.
func (b *Logos) writeDoc(ev eval.Event) error {
	slug := slugify(ev.Title)
	dir := "topics"
	if ev.Project != "" && slug == slugify(ev.Project) {
		dir = "projects"
	}
	rel := filepath.Join(dir, slug)
	if n := b.paths[rel]; n > 0 {
		rel = fmt.Sprintf("%s-%d", rel, n+1)
	}
	b.paths[filepath.Join(dir, slug)]++

	kind := "topic"
	if dir == "projects" {
		kind = "project"
	}
	body := fmt.Sprintf("---\ntype: %s\ntitle: %s\nfirst_seen: %s\n---\n%s\n",
		kind, ev.Title, time.Unix(ev.TS, 0).UTC().Format("2006-01-02"), ev.Text)

	b.dirty = true
	return vault.WriteAtomic(filepath.Join(b.root, rel+".md"), []byte(body))
}

func (b *Logos) Read(q eval.Query) (eval.Response, error) {
	if b.dirty {
		if _, err := b.ix.Sync(); err != nil {
			return eval.Response{}, err
		}
		b.dirty = false
	}
	pack, err := contextpack.Build(b.ix, b.embed, b.model, contextpack.Request{
		Task: q.Task, Hint: q.Project, Budget: q.Budget, Now: q.Now, Dir: b.repo,
	})
	if err != nil {
		return eval.Response{}, err
	}
	return eval.Response{Text: pack.Render()}, nil
}

// DropDerived is `rm -rf .logos` followed by `logos index` — the claim that the
// database is a cache, executed. Whatever comes back afterwards is what the
// vault really held.
func (b *Logos) DropDerived() error {
	if b.ix != nil {
		b.ix.Close()
		b.ix = nil
	}
	if err := os.RemoveAll(filepath.Join(b.root, ".logos")); err != nil {
		return err
	}
	if err := b.open(); err != nil {
		return err
	}
	if _, err := b.ix.Sync(); err != nil {
		return err
	}
	// Both halves, exactly as `logos index` runs them. Rebuilding notes but not
	// memories would measure a reindex nobody performs.
	_, _, err := b.ix.SyncMemories(b.embed, b.model)
	return err
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var out []rune
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
			dash = false
		default:
			if !dash && len(out) > 0 {
				out = append(out, '-')
				dash = true
			}
		}
	}
	return strings.Trim(string(out), "-")
}
