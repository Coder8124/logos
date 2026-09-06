// Package main is the Wails desktop app.
//
// It is a thin binding layer: every method here delegates to the same
// internal/* packages the CLI uses, so there is one engine and the app can
// never drift from the command line. No business logic lives in this package.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/secretary"
	"github.com/Coder8124/brain/internal/session"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx   context.Context
	vault string
}

func NewApp(vault string) *App { return &App{vault: vault} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Hide dismisses the panel. With no traffic lights, this is how the window is
// closed — bound to Esc in the frontend, so the panel behaves like a menubar
// dropdown that gets out of the way rather than an app you quit.
func (a *App) Hide() {
	if a.ctx != nil {
		runtime.WindowHide(a.ctx)
	}
}

// Show brings the panel back (used when relaunched while already running).
func (a *App) Show() {
	if a.ctx != nil {
		runtime.WindowShow(a.ctx)
	}
}

// open returns a fresh index handle per call. These are cheap, and a
// short-lived handle avoids holding the SQLite file open while the user has
// Obsidian writing to the same vault.
func (a *App) open() (*index.Index, error) {
	// index.Open creates the directory it is pointed at, which is right for the
	// index but wrong as a way to acquire a vault: pointed somewhere the user
	// never chose, it makes an empty one and every view then truthfully reports
	// zero of everything. That is indistinguishable from a working install with
	// nothing in it, and it is how the app spent its life reading ~/brain-vault
	// while the real memory was in ~/brain. Say the path instead of inventing
	// a store — the same refusal `brain doctor` and the MCP server already make.
	if _, err := os.Stat(a.vault); err != nil {
		return nil, fmt.Errorf(
			"no vault at %s — run `brain setup --vault <path>`, which records the "+
				"location for this app, then relaunch", a.vault)
	}
	ix, err := index.Open(a.vault)
	if err != nil {
		return nil, err
	}
	if err := secretary.Init(ix.DB); err != nil {
		ix.Close()
		return nil, err
	}
	return ix, nil
}

// router builds a model router for the vault, shared by the flavor bindings.
func (a *App) router() (*router.Router, error) {
	cfg, err := router.Load(a.vault)
	if err != nil {
		return nil, err
	}
	return router.New(cfg, a.vault)
}

// LoopDone and LoopDrop close an open loop from the panel. Done means handled;
// Drop means "stop telling me" and is retained so it is not re-surfaced.
func (a *App) LoopDone(id int64) error {
	ix, err := a.open()
	if err != nil {
		return err
	}
	defer ix.Close()
	return secretary.SetStatus(ix.DB, id, secretary.Done)
}

func (a *App) LoopDrop(id int64) error {
	ix, err := a.open()
	if err != nil {
		return err
	}
	defer ix.Close()
	return secretary.SetStatus(ix.DB, id, secretary.Dropped)
}

// AddLoop lets the user hand the secretary a commitment directly.
func (a *App) AddLoop(text string) error {
	ix, err := a.open()
	if err != nil {
		return err
	}
	defer ix.Close()
	_, err = secretary.Add(ix.DB, &secretary.Commitment{Text: text})
	return err
}

// ---- shapes the frontend consumes. Kept flat and JSON-friendly. ----

type Status struct {
	Vault    string `json:"vault"`
	Notes    int    `json:"notes"`
	Edges    int    `json:"edges"`
	Runtime  string `json:"runtime"`
	Memories int    `json:"memories"`
}

// Status backs the panel header.
func (a *App) Status() (Status, error) {
	ix, err := a.open()
	if err != nil {
		return Status{}, err
	}
	defer ix.Close()

	// Every count is checked. A dropped error here renders as a zero, and a
	// zero in this panel is a claim: it says the vault holds nothing. That is
	// the same sentence a broken index would produce, and the app has already
	// shipped one bug whose whole symptom was a confident, healthy zero.
	s := Status{Vault: a.vault}
	if s.Notes, err = ix.NoteCount(); err != nil {
		return s, fmt.Errorf("counting notes: %w", err)
	}
	if s.Edges, err = ix.EdgeCount(); err != nil {
		return s, fmt.Errorf("counting links: %w", err)
	}
	if err := memory.Init(ix.DB); err != nil {
		return s, fmt.Errorf("opening memory: %w", err)
	}
	if s.Memories, err = memory.Count(ix.DB); err != nil {
		return s, fmt.Errorf("counting memories: %w", err)
	}

	if cfg, err := router.Load(a.vault); err == nil {
		if rt, err := router.New(cfg, a.vault); err == nil {
			if m, err := rt.Model(router.T2); err == nil {
				s.Runtime = rt.Local().Name + " · " + m
			}
		}
	}
	return s, nil
}

// OverviewView is the state banner every terminal-app view leads with: proof
// that indexing and continuity are actually running, not just configured to.
// A feature with no visible state reads as broken even when it works — this
// is what makes the difference legible.
type OverviewView struct {
	Vault        string `json:"vault"`
	Notes        int    `json:"notes"`
	Memories     int    `json:"memories"`
	Checkpoints  int    `json:"checkpoints"`
	OpenSessions int    `json:"openSessions"`
	Projects     int    `json:"projects"`
	// IndexBuilt is when the on-disk index was last written, unix seconds, 0
	// if it has never been built.
	IndexBuilt int64 `json:"indexBuilt"`
	// VaultWritten is the newest checkpoint or memory file's mtime, unix
	// seconds, 0 if the vault holds neither yet.
	VaultWritten int64 `json:"vaultWritten"`
}

// Overview backs the terminal app's state strip. It is read on tab switches
// rather than polled alongside Status: counting checkpoints means walking the
// vault, which is cheap once but not something to repeat every few seconds.
func (a *App) Overview() (OverviewView, error) {
	ix, err := a.open()
	if err != nil {
		return OverviewView{}, err
	}
	defer ix.Close()

	// As in Status: a swallowed error here is displayed as a zero, and a zero
	// in the state strip is read as "nothing has been recorded", which is the
	// one thing this strip exists to disprove.
	v := OverviewView{Vault: a.vault}
	if v.Notes, err = ix.NoteCount(); err != nil {
		return v, fmt.Errorf("counting notes: %w", err)
	}
	if err := memory.Init(ix.DB); err != nil {
		return v, fmt.Errorf("opening memory: %w", err)
	}
	if v.Memories, err = memory.Count(ix.DB); err != nil {
		return v, fmt.Errorf("counting memories: %w", err)
	}
	if err := session.Init(ix.DB); err != nil {
		return v, fmt.Errorf("opening sessions: %w", err)
	}
	if v.OpenSessions, err = session.OpenCount(ix.DB); err != nil {
		return v, fmt.Errorf("counting open sessions: %w", err)
	}
	projects, err := session.Projects(a.vault)
	if err != nil {
		return v, fmt.Errorf("listing projects: %w", err)
	}
	v.Projects = len(projects)
	v.Checkpoints, v.VaultWritten = vaultStats(a.vault)
	v.IndexBuilt = indexBuiltAt(a.vault)
	return v, nil
}

// vaultStats walks the two vault directories that are the durable record —
// checkpoints and memories — counting checkpoint files and finding the
// newest mtime across both. It reads file metadata only, never content, so
// it stays cheap regardless of how much either directory holds.
func vaultStats(vault string) (checkpoints int, written int64) {
	for i, dir := range []string{
		filepath.Join(vault, session.CheckpointDir),
		filepath.Join(vault, memory.Dir),
	} {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			if i == 0 {
				checkpoints++
			}
			if info, err := d.Info(); err == nil {
				if t := info.ModTime().Unix(); t > written {
					written = t
				}
			}
			return nil
		})
	}
	return checkpoints, written
}

// indexBuiltAt is the mtime of the on-disk index, the newer of its main file
// and its WAL — the index is rebuilt continuously in WAL mode, so a write
// often lands there first. 0 means the index has not been built yet.
func indexBuiltAt(vault string) int64 {
	var latest time.Time
	for _, name := range []string{"index.db", "index.db-wal"} {
		if fi, err := os.Stat(filepath.Join(vault, ".brain", name)); err == nil {
			if fi.ModTime().After(latest) {
				latest = fi.ModTime()
			}
		}
	}
	if latest.IsZero() {
		return 0
	}
	return latest.Unix()
}

// Ask answers a question from the vault, for the panel's ask box.
func (a *App) Ask(question string) (string, error) {
	ix, err := a.open()
	if err != nil {
		return "", err
	}
	defer ix.Close()

	cfg, err := router.Load(a.vault)
	if err != nil {
		return "", err
	}
	rt, err := router.New(cfg, a.vault)
	if err != nil {
		return "", err
	}
	embed, _ := rt.Model(router.T0)
	chat, _ := rt.Model(router.T2)

	answer, _, err := ix.Ask(rt.Local(), embed, chat, question, 6, 6000)
	return answer, err
}
