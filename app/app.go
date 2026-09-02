// Package main is the Wails desktop app.
//
// It is a thin binding layer: every method here delegates to the same
// internal/* packages the CLI uses, so there is one engine and the app can
// never drift from the command line. No business logic lives in this package.
package main

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/Coder8124/brain/internal/capture"
	"github.com/Coder8124/brain/internal/index"
	"github.com/Coder8124/brain/internal/memory"
	"github.com/Coder8124/brain/internal/rollup"
	"github.com/Coder8124/brain/internal/router"
	"github.com/Coder8124/brain/internal/routine"
	"github.com/Coder8124/brain/internal/secretary"

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

	s := Status{Vault: a.vault}
	s.Notes, _ = ix.NoteCount()
	s.Edges, _ = ix.EdgeCount()
	s.Events, _ = capture.Count(ix.DB)
	s.Pending, _ = rollup.PendingCount(ix.DB)
	if memory.Init(ix.DB) == nil {
		s.Memories, _ = memory.Count(ix.DB)
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
