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
	"sort"
	"strings"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/rollup"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/routine"
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
	ix, err := index.Open(a.vault)
	if err != nil {
		return nil, err
	}
	if err := capture.InitStore(ix.DB); err != nil {
		ix.Close()
		return nil, err
	}
	if err := rollup.InitQueue(ix.DB); err != nil {
		ix.Close()
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

// Brief is what the app leads with: the secretary speaking first. This is the
// method that makes the product a secretary rather than an archive — the panel
// opens on this, not on an ask box.
func (a *App) Brief() (secretary.Brief, error) {
	ix, err := a.open()
	if err != nil {
		return secretary.Brief{}, err
	}
	defer ix.Close()

	b, err := secretary.Compose(ix.DB, time.Now())
	if err != nil {
		return b, err
	}
	b.Review, _ = rollup.PendingCount(ix.DB)
	return b, nil
}

// LoopDone and LoopDrop close an open loop from the brief. Done means handled;
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
	Vault     string `json:"vault"`
	Notes     int    `json:"notes"`
	Edges     int    `json:"edges"`
	Events    int    `json:"events"`
	Pending   int    `json:"pending"`
	Runtime   string `json:"runtime"`
	Recording bool   `json:"recording"`
	Memories  int    `json:"memories"`
}

type TimelineItem struct {
	Time  string `json:"time"`
	Dur   string `json:"dur"`
	Kind  string `json:"kind"`
	App   string `json:"app"`
	Label string `json:"label"`
}

type ProposalView struct {
	ID       int64    `json:"id"`
	Kind     string   `json:"kind"`
	Summary  string   `json:"summary"`
	Conf     float64  `json:"conf"`
	Model    string   `json:"model"`
	Evidence []string `json:"evidence"`
}

// Status backs the menubar orb and the panel header.
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
	if s.Events, err = capture.Count(ix.DB); err != nil {
		return s, fmt.Errorf("counting captured events: %w", err)
	}
	if s.Pending, err = rollup.PendingCount(ix.DB); err != nil {
		return s, fmt.Errorf("counting the review queue: %w", err)
	}
	if err := memory.Init(ix.DB); err != nil {
		return s, fmt.Errorf("opening memory: %w", err)
	}
	if s.Memories, err = memory.Count(ix.DB); err != nil {
		return s, fmt.Errorf("counting memories: %w", err)
	}
	s.Recording = recorderRunning()

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
// that capture, indexing, and continuity are actually running, not just
// configured to. A feature with no visible state reads as broken even when it
// works — this is what makes the difference legible.
type OverviewView struct {
	Vault        string `json:"vault"`
	Notes        int    `json:"notes"`
	Memories     int    `json:"memories"`
	Checkpoints  int    `json:"checkpoints"`
	OpenSessions int    `json:"openSessions"`
	Projects     int    `json:"projects"`
	Recording    bool   `json:"recording"`
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
	v := OverviewView{Vault: a.vault, Recording: recorderRunning()}
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

// Timeline backs the today view.
func (a *App) Timeline() ([]TimelineItem, error) {
	ix, err := a.open()
	if err != nil {
		return nil, err
	}
	defer ix.Close()

	from, to := capture.TodayBounds()
	events, err := capture.Range(ix.DB, from, to)
	if err != nil {
		return nil, err
	}

	var out []TimelineItem
	for _, e := range events {
		if e.Kind == capture.Focus && e.DurS < capture.IncidentalSecs {
			continue
		}
		item := TimelineItem{Time: capture.HHMM(e.TS), Kind: string(e.Kind), App: e.App}
		if e.DurS > 0 {
			item.Dur = capture.Dur(e.DurS)
		}
		switch e.Kind {
		case capture.URL:
			item.Label = e.URL
		case capture.Commit:
			item.Label = e.Path + " — " + e.Title
		default:
			item.Label = e.Title
		}
		out = append(out, item)
	}
	// Newest first reads better in a scrolling panel.
	sort.SliceStable(out, func(i, j int) bool { return i > j })
	return out, nil
}

// Proposals backs the review queue.
func (a *App) Proposals() ([]ProposalView, error) {
	ix, err := a.open()
	if err != nil {
		return nil, err
	}
	defer ix.Close()

	pending, err := rollup.List(ix.DB, rollup.Pending)
	if err != nil {
		return nil, err
	}

	out := make([]ProposalView, 0, len(pending))
	for _, p := range pending {
		v := ProposalView{
			ID: p.ID, Kind: string(p.Kind), Summary: p.Summary(),
			Conf: p.Conf, Model: p.Model,
		}
		if ev, err := capture.ByIDs(ix.DB, p.Evidence); err == nil {
			for i, e := range ev {
				if i >= 6 {
					break
				}
				switch e.Kind {
				case capture.URL:
					v.Evidence = append(v.Evidence, capture.HHMM(e.TS)+"  "+e.URL)
				case capture.Commit:
					v.Evidence = append(v.Evidence, capture.HHMM(e.TS)+"  "+e.Path+" — "+e.Title)
				default:
					v.Evidence = append(v.Evidence, capture.HHMM(e.TS)+"  "+e.App+" "+e.Title)
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Accept applies a proposal to the vault. Same code path the CLI review uses,
// so the trust loop is identical whichever surface the user is in.
func (a *App) Accept(id int64) error {
	ix, err := a.open()
	if err != nil {
		return err
	}
	defer ix.Close()

	for _, p := range mustList(ix) {
		if p.ID == id {
			if err := rollup.Apply(ix.DB, a.vault, p); err != nil {
				return err
			}
			return rollup.SetStatus(ix.DB, id, rollup.Accepted)
		}
	}
	return nil
}

func (a *App) Reject(id int64) error {
	ix, err := a.open()
	if err != nil {
		return err
	}
	defer ix.Close()
	return rollup.SetStatus(ix.DB, id, rollup.Rejected)
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

// Routines backs the routines panel. Read-only; proposing stays a deliberate
// action taken from the review flow.
func (a *App) Routines() ([]string, error) {
	ix, err := a.open()
	if err != nil {
		return nil, err
	}
	defer ix.Close()

	events, err := capture.Range(ix.DB, time.Now().AddDate(0, 0, -400).Unix(), time.Now().Unix()+1)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, p := range routine.FindPeriodic(events) {
		out = append(out, p.App+" · "+p.Cadence()+" "+p.Window())
	}
	for _, p := range routine.FindPeriodicSites(events) {
		out = append(out, p.App+" · "+p.Cadence()+" "+p.Window())
	}
	return out, nil
}

func mustList(ix *index.Index) []rollup.Proposal {
	p, _ := rollup.List(ix.DB, rollup.Pending)
	return p
}

// recorderRunning reports whether a capture daemon is live, so the orb can show
// an honest recording state. Presence of the pidfile is the signal the daemon
// writes; see cmd/brain capture.
func recorderRunning() bool {
	if _, err := os.Stat(os.Getenv("HOME") + "/.brain-recording"); err == nil {
		return true
	}
	return false
}
