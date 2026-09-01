// Package adapters holds one implementation of eval.Adapter per memory system
// under test.
//
// The adapters are the fair-comparison surface. Each one is written to make its
// system look as good as that system can look: brain gets to use checkpoints
// because it has them, and a store with only add() and search() gets the same
// information flattened into prose rather than withheld. Where a system loses,
// it should lose because of what it is, not because of how it was driven here.
package adapters

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pragun/brain/internal/contextpack"
	"github.com/pragun/brain/internal/eval"
	"github.com/pragun/brain/internal/index"
	"github.com/pragun/brain/internal/memory"
	"github.com/pragun/brain/internal/provider"
	"github.com/pragun/brain/internal/secretary"
	"github.com/pragun/brain/internal/session"
	"github.com/pragun/brain/internal/vault"
)

// Brain drives this project through the same entry points the MCP server and
// the CLI use: notes and checkpoints go in, contextpack.Build comes out. No
// benchmark-only retrieval path — if the suite scores well, the shipped product
// scores well.
type Brain struct {
	root  string // a scratch vault, never the user's
	ix    *index.Index
	embed *provider.Provider
	model string

	dirty bool           // vault has unsynced files
	paths map[string]int // title collisions get a suffix rather than an overwrite
}

// NewBrain builds an adapter over a scratch vault. Pass a nil provider to run
// lexical-and-graph only, which is fast and needs no model loaded.
func NewBrain(embed *provider.Provider, model string) (*Brain, error) {
	b := &Brain{embed: embed, model: model}
	return b, b.Reset()
}

func (b *Brain) Name() string {
	if b.embed == nil {
		return "brain (no embed)"
	}
	return "brain"
}

func (b *Brain) Reset() error {
	if b.ix != nil {
		b.ix.Close()
	}
	if b.root != "" {
		os.RemoveAll(b.root)
	}
	root, err := os.MkdirTemp("", "eval-brain-")
	if err != nil {
		return err
	}
	b.root = root
	b.paths = map[string]int{}
	b.dirty = false
	return b.open()
}

func (b *Brain) open() error {
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

func (b *Brain) Close() error {
	if b.ix != nil {
		b.ix.Close()
	}
	if b.root != "" {
		return os.RemoveAll(b.root)
	}
	return nil
}

func (b *Brain) Write(ev eval.Event) error {
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
		return session.Commit(b.ix.DB, b.root, &session.Checkpoint{
			Project: ev.Project, Agent: ev.Actor, Task: ev.Task,
			State: ev.Text, Decisions: ev.Decisions, Failed: ev.Failed,
			Questions: ev.Questions, Next: ev.Next, TS: ev.TS,
		})
	}
	return fmt.Errorf("unknown event kind %q", ev.Kind)
}

// writeDoc lays a document into the vault the way a person would: the project's
// own page under projects/, everything else under topics/, named by its title
// so that [[wikilinks]] in other notes resolve.
func (b *Brain) writeDoc(ev eval.Event) error {
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

func (b *Brain) Read(q eval.Query) (eval.Response, error) {
	if b.dirty {
		if _, err := b.ix.Sync(); err != nil {
			return eval.Response{}, err
		}
		b.dirty = false
	}
	pack, err := contextpack.Build(b.ix, b.embed, b.model, contextpack.Request{
		Task: q.Task, Hint: q.Project, Budget: q.Budget, Now: q.Now,
	})
	if err != nil {
		return eval.Response{}, err
	}
	return eval.Response{Text: pack.Render()}, nil
}

// DropDerived is `rm -rf .brain` followed by `brain index` — the claim that the
// database is a cache, executed. Whatever comes back afterwards is what the
// vault really held.
func (b *Brain) DropDerived() error {
	if b.ix != nil {
		b.ix.Close()
		b.ix = nil
	}
	if err := os.RemoveAll(filepath.Join(b.root, ".brain")); err != nil {
		return err
	}
	if err := b.open(); err != nil {
		return err
	}
	if _, err := b.ix.Sync(); err != nil {
		return err
	}
	// Both halves, exactly as `brain index` runs them. Rebuilding notes but not
	// memories would measure a reindex nobody performs.
	_, err := b.ix.SyncMemories(b.embed, b.model)
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
