package watch

import (
	"fmt"
	"os"
	"time"

	"github.com/MartyJRE/claudia/internal/autopilot"
	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// Seams for tests.
var (
	findRepos = repos.Find
	scanUsage = (*claude.UsageScanner).Scan
	tickPilot = (*autopilot.Pilot).Tick
	getConfig = config.Get
)

// pilot is the watcher's side of the autopilot: what it ticks with, kept
// between ticks.
type pilot struct {
	*autopilot.Pilot
	board     *claude.Store // a week of sessions, as the TUI's Board reads them
	links     *links.Links
	repos     []repos.Repo
	reposAt   time.Time
	ledger    *claude.Ledger
	scanner   *claude.UsageScanner
	scannedAt time.Time
	at        time.Time
	ctxTold   map[string]bool // runs told their context is filling up
}

func (w *watcher) startPilot(profiles []claude.Profile) {
	w.pilot = &pilot{
		Pilot:   &autopilot.Pilot{DB: w.runs, Notify: func(n autopilot.Note) { notify(pilotNote(n)) }},
		board:   claude.NewStore(profiles, 7*24*time.Hour),
		links:   links.Load(),
		ledger:  claude.OpenLedger(),
		scanner: claude.NewUsageScanner(profiles, 31),
		ctxTold: map[string]bool{},
	}
}

func pilotNote(n autopilot.Note) note {
	return note{title: n.Title, subtitle: n.Subtitle, body: n.Body, thread: "autopilot", id: n.ID, target: n.Target, url: n.URL}
}

// autopilot keeps the cost ledger current and, when it's on, starts what the
// Board has queued; once a minute.
func (w *watcher) autopilot(now time.Time) {
	p := w.pilot
	if p == nil || w.runs == nil || now.Sub(p.at) < time.Minute {
		return
	}
	p.at = now
	if now.Sub(p.scannedAt) >= 5*time.Minute {
		p.scannedAt = now
		scanUsage(p.scanner)
		p.ledger.Reload()
		if p.ledger.Merge(p.scanner.Sessions()) {
			if err := p.ledger.Save(); err != nil {
				fmt.Fprintln(os.Stderr, now.Format(time.TimeOnly), "cost ledger:", err)
			}
		}
	}
	cfg := getConfig()
	if !cfg.Autopilot.Enabled {
		return
	}
	if now.Sub(p.reposAt) >= 5*time.Minute {
		p.reposAt = now
		p.repos = findRepos(repos.Root())
	}
	p.links.Reload()
	kinds := launch.Kinds()
	var ss []board.Session
	latest := w.runs.Latest()
	w.summaryMu.Lock()
	for _, s := range p.board.Sessions() {
		sum := w.summaries[s.ID]
		bs := board.Session{Session: s, Kind: kinds[s.ID], Summary: sum.Text, SummaryFor: sum.For}
		bs.WaitOnRun(latest)
		ss = append(ss, bs)
	}
	w.summaryMu.Unlock()
	tickPilot(p.Pilot, now, cfg, autopilot.Inputs{GL: w.gl, Jira: w.js, Sessions: ss, Links: p.links, Repos: p.repos,
		Profiles: p.board.Profiles, Ledger: p.ledger, Atl: w.atl})
}

// ctxAt is the context fill a running run is warned at: without autocompact
// it stops when full.
const ctxAt = 0.9

// contextNotes tells once per run when its context is nearly full.
func (w *watcher) contextNotes(all []claude.Session, loc *tmux.Locator) {
	if w.runs == nil || w.pilot == nil {
		return
	}
	by := map[string]claude.Session{}
	for _, s := range all {
		by[s.ID] = s
	}
	for _, r := range w.runs.Running() {
		s, ok := by[r.Session]
		if !ok || w.pilot.ctxTold[r.ID] || s.Transcript.ContextFill() < ctxAt {
			continue
		}
		w.pilot.ctxTold[r.ID] = true
		n := note{title: "Context 90% · " + board.StepLabel(r.Step) + " on " + r.Subject, body: "it stops when full — /compact or start fresh",
			thread: "autopilot", id: "ctx:" + r.ID}
		if s.Live != nil {
			if p, ok := loc.Pane(s.Live.PID); ok {
				n.target = p.Target() + "." + p.Index
			}
		}
		notify(n)
	}
}
