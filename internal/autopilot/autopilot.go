// Package autopilot starts the Board's steps on its own from the watcher:
// those whose mode allows it, the most blocking first, within the limits.
// Nothing it starts is hidden: runs are ordinary tmux sessions, and merge is
// a GitLab call.
package autopilot

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/board"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/links"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/runs"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/steps"
	"github.com/MartyJRE/claudia/internal/teams"
)

// Seams for tests: everything that leaves the process.
var (
	startSession = launch.Start
	exportBrief  = (*atlassian.Client).ExportBrief
	approveMR    = gitlab.Approve
	mergeMR      = gitlab.Merge
	newTeams     = teams.New
	fetchReview  = (*teams.Client).FetchReview
	teamsMe      = (*teams.Client).MeID
	react        = (*teams.Client).React
	readLimits   = claude.ReadLimits
	dirty        = steps.Dirty
)

// Keys in state.db's kv table, shared with the TUI.
const (
	KeyPaused  = "autopilot.paused"
	KeyStatus  = "autopilot.status"
	KeyPlanOK  = "plan-ok:" // + ticket: p was pressed on its plan
	KeyBlocked = "blocked:" // + card: why its next step can't start on its own
	keyHandled = "handled:" // + run: its review outcome was acted on
	keyRepo    = "repo:"    // + ticket: the repo picked for its steps
)

// RepoKey is where the repo picked for a ticket's steps is kept, so later
// steps run there without asking.
func RepoKey(ticket string) string { return keyRepo + ticket }

// Status is what the TUI's pill shows, written on every tick.
type Status struct {
	At      time.Time `json:"at"`
	Running int       `json:"running"`
	Max     int       `json:"max"`
	Queued  int       `json:"queued"`
	Reason  string    `json:"reason,omitempty"`  // why nothing new starts
	Unknown bool      `json:"unknown,omitempty"` // plan usage unknown: one run at a time
}

// ReadStatus is the status the watcher wrote last.
func ReadStatus(db *runs.Store) (Status, bool) {
	var st Status
	v, ok := db.Get(KeyStatus)
	return st, ok && json.Unmarshal([]byte(v), &st) == nil
}

// Note is a notification for the watcher to post.
type Note struct {
	Title, Subtitle, Body string
	ID                    string // a newer note with the same id replaces the older
	Target                string // the pane a click jumps to
	URL                   string // or the link it opens
}

// Inputs is what the watcher knows at a tick.
type Inputs struct {
	GL       gitlab.Snapshot
	Jira     atlassian.JiraSnapshot
	Sessions []board.Session
	Links    *links.Links
	Repos    []repos.Repo
	Profiles []claude.Profile
	Ledger   *claude.Ledger
	Atl      *atlassian.Client
}

type Pilot struct {
	DB     *runs.Store
	Notify func(Note)

	reason string // what the last pause or resume note said
	told   bool
}

type candidate struct {
	card board.Card
	step string
	dir  string
	rank int
}

// busy is how long after its last turn a live session still counts as
// someone working the card by hand.
const busy = 2 * time.Hour

// Tick starts what may start now and says where the autopilot stands.
func (p *Pilot) Tick(now time.Time, cfg config.Config, in Inputs) Status {
	a := cfg.Autopilot
	running := len(p.DB.Running())
	st := Status{At: now, Running: running, Max: a.MaxRunning()}
	if _, paused := p.DB.Get(KeyPaused); paused {
		st.Reason = "paused"
		return p.done(st)
	}
	p.settled(now, cfg, in)

	ix := board.BuildIndex(plain(in.Sessions), in.GL, in.Jira, in.Links)
	cards := append(board.Cards(in.Jira.Sprint, ix, in.Sessions, p.DB.For, cfg.Board.Statuses),
		board.ReviewCards(in.GL, ix, in.Sessions, p.DB.For)...)
	over := p.DB.Overrides()
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = c.ID()
	}
	_ = p.DB.PruneOverrides(ids, now, now.Add(-30*24*time.Hour))
	planOK := p.DB.Prefixed(KeyPlanOK)
	blocked := map[string]string{}
	var queue []candidate
	for rank, c := range cards {
		next, hold := board.NextStep(c)
		if hold != nil || next == "" || !c.IsReview() && board.OnHold(c.Issue.Status) || handsOn(c, now) {
			continue
		}
		if GateOf(a, over[c.ID()], c, next, planOK[c.Issue.Key] != "") != Queued {
			continue
		}
		if stale := a.ReviewStaleAfter(); c.IsReview() && stale > 0 && now.Sub(c.MR.UpdatedAt) > stale {
			blocked[c.ID()] = "stale review request"
			continue
		}
		if !c.IsReview() && in.Ledger != nil {
			if usd := in.Ledger.Ticket(c.Issue.Key).USD; usd >= a.MaxTicketUSD() {
				blocked[c.ID()] = fmt.Sprintf("spent $%.0f of $%.0f", usd, a.MaxTicketUSD())
				continue
			}
		}
		dir, why := p.where(cfg, in, c, next)
		if why != "" {
			blocked[c.ID()] = why
			continue
		}
		queue = append(queue, candidate{c, next, dir, rank})
	}
	p.block(blocked)
	slices.SortStableFunc(queue, func(x, y candidate) int {
		if c := cmp.Compare(slices.Index(order, x.step), slices.Index(order, y.step)); c != 0 {
			return c
		}
		if x.card.IsReview() && y.card.IsReview() {
			return y.card.MR.UpdatedAt.Compare(x.card.MR.UpdatedAt)
		}
		return cmp.Compare(x.rank, y.rank)
	})
	st.Queued = len(queue)

	l, ok := readLimits(limitsPath(launch.WorkProfile(in.Profiles)))
	reason, max, known := limit(a, now, l, ok, p.DB.AutopilotUSD(startOfDay(now)))
	st.Reason, st.Max, st.Unknown = reason, max, !known
	if reason != "" {
		return p.done(st)
	}
	p.tell("")
	reviewed := p.reviewedWithin(now, a.ReviewEvery())
	for _, q := range queue {
		if st.Running >= st.Max {
			break
		}
		if q.step == "review" && reviewed {
			continue
		}
		if err := p.start(now, cfg, in, ix, q); err != nil {
			p.Notify(Note{Title: "Autopilot couldn't start " + board.StepLabel(q.step), Subtitle: ref(q.card), Body: err.Error(), ID: "autopilot:error:" + q.card.ID()})
			continue
		}
		reviewed = reviewed || q.step == "review"
		if q.step != "merge" {
			st.Running++
		}
		st.Queued--
	}
	return p.done(st)
}

// handsOn: someone works the card in a session of their own right now.
func handsOn(c board.Card, now time.Time) bool {
	return slices.ContainsFunc(c.Sessions, func(s board.Session) bool {
		return s.IsLive() && (s.Working() || s.NeedsYou() || now.Sub(s.LastActive()) < busy) &&
			!slices.ContainsFunc(c.Runs, func(r runs.Run) bool { return r.Session == s.ID })
	})
}

func plain(ss []board.Session) []claude.Session {
	out := make([]claude.Session, len(ss))
	for i, s := range ss {
		out[i] = s.Session
	}
	return out
}

func limitsPath(p claude.Profile) string {
	return state.Path("limits-" + filepath.Base(p.Dir) + ".json")
}

// done tells of a change of state and writes the status for the TUI.
func (p *Pilot) done(st Status) Status {
	p.tell(st.Reason)
	if b, err := json.Marshal(st); err == nil {
		_ = p.DB.Set(KeyStatus, string(b))
	}
	return st
}

// tell posts a note when the autopilot pauses, resumes, or pauses for
// another reason; not for the state it starts in, and not for working hours
// opening and closing, which happens every day.
func (p *Pilot) tell(reason string) {
	routine := reason == offHours || reason == "" && p.reason == offHours
	if p.told && reason != p.reason && !routine {
		n := Note{Title: "Autopilot paused", Body: reason, ID: "autopilot:state"}
		if reason == "" {
			n = Note{Title: "Autopilot resumed", Body: "it starts the Board's steps again", ID: "autopilot:state"}
		}
		p.Notify(n)
	}
	p.told, p.reason = true, reason
}

// block records why queued cards can't start, for the TUI, and clears the rest.
func (p *Pilot) block(blocked map[string]string) {
	for id := range p.DB.Prefixed(KeyBlocked) {
		if _, ok := blocked[id]; !ok {
			_ = p.DB.Set(KeyBlocked+id, "")
		}
	}
	for id, why := range blocked {
		_ = p.DB.Set(KeyBlocked+id, why)
	}
}

// where is the dir a step runs in, or why it can't run there on its own.
func (p *Pilot) where(cfg config.Config, in Inputs, c board.Card, step string) (string, string) {
	const pick = "pick a repo"
	switch step {
	case "merge":
		return "", ""
	case "readiness":
		return steps.Anywhere(cfg), ""
	case "review", "feedback":
		mr, _ := board.ThreadsMR(c)
		if c.IsReview() {
			mr = *c.MR
		}
		if r, ok := steps.ReviewClone(in.Repos, mr); ok {
			return r.Dir, ""
		}
		return "", pick
	}
	picked, _ := p.DB.Get(RepoKey(c.Issue.Key))
	r, ok := steps.TicketRepo(in.Repos, c.Issue.Key, c.MRs, picked)
	switch {
	case !ok:
		return "", pick
	case (step == "implement" || step == "mr") && dirty(r.Dir):
		return "", "commit or stash in " + filepath.Base(r.Dir)
	}
	return r.Dir, ""
}

// reviewedWithin: the autopilot started a review in the last period.
func (p *Pilot) reviewedWithin(now time.Time, every time.Duration) bool {
	return every > 0 && slices.ContainsFunc(p.DB.Since(now.Add(-every)), func(r runs.Run) bool {
		return r.Step == "review" && r.StartedBy == runs.ByAutopilot
	})
}

var errNoJira = errors.New("Jira isn't set up")

// start runs the step: a merge right away, anything else as a session in its own window.
func (p *Pilot) start(now time.Time, cfg config.Config, in Inputs, ix board.Index, q candidate) error {
	c := q.card
	run := runs.Run{ID: launch.NewID(), Kind: "ticket", Subject: c.ID(), Step: q.step, Started: now, StartedBy: runs.ByAutopilot}
	if c.IsReview() {
		run.Kind = "review"
	}
	if q.step == "merge" {
		return p.merge(now, c, run)
	}
	profile := launch.ProfileFor(q.dir, in.Profiles)
	var sp launch.Spec
	var urls []string
	ticket := c.Issue.Key
	switch q.step {
	case "review":
		mr := *c.MR
		for _, o := range board.Related(mr, in.GL) {
			urls = append(urls, o.WebURL)
		}
		ticket = board.MRTicket(mr)
		sp, _ = launch.New(q.dir, profile, steps.Title(cfg, ix.Issues, "review", "review", ticket, mr.Title), steps.ReviewPrompt(cfg, urls))
		sp.Kind, run.SHA = "review", mr.SHA
	case "feedback":
		mr, _ := board.ThreadsMR(c)
		urls = []string{mr.WebURL}
		sp, _ = launch.New(q.dir, profile, steps.Title(cfg, ix.Issues, "fix", "fix threads", ticket, mr.Title), steps.ThreadsPrompt(mr))
		sp.Kind, run.SHA = "fix", mr.SHA
	default:
		s := steps.Ticket[q.step]
		path, err := "", errNoJira
		if in.Atl != nil {
			path, err = exportBrief(in.Atl, ticket, state.Path("jira-export"))
		}
		sp, _ = launch.New(q.dir, profile, steps.Title(cfg, ix.Issues, s.Intent, s.Verb, ticket, c.Issue.Summary), steps.FillBrief(s.Prompt(cfg), ticket, path, err))
		sp.Kind = s.Intent
	}
	run.Session = sp.ID
	sp.Color = steps.Color(colours(in.Sessions))
	// NOTE: auto mode is a first try for unattended runs; if it blocks too much or stalls runs,
	// we might switch to bypassPermissions plus a guard hook, or another mode.
	sp = sp.WithPermissionMode(cfg.Autopilot.PermissionMode()).AsRun(run.ID, run.Step, run.Subject)
	if err := p.DB.Start(run); err != nil {
		return err
	}
	if len(urls) > 0 {
		_ = in.Links.AddMRs(sp.ID, urls)
	}
	if ticket != "" {
		_ = in.Links.SetTicket(sp.ID, ticket)
	}
	target, err := startSession(sp)
	if err != nil {
		_ = p.DB.Settle(sp.ID, runs.Failed, err.Error(), now)
		return err
	}
	p.Notify(Note{Title: "Autopilot started " + board.StepLabel(q.step), Subtitle: ref(c), Body: sp.Name, ID: "autopilot:" + run.ID, Target: target + ".0"})
	return nil
}

func (p *Pilot) merge(now time.Time, c board.Card, run runs.Run) error {
	i := slices.IndexFunc(c.MRs, board.Mergeable)
	if i < 0 {
		return errors.New("no MR of " + ref(c) + " is ready to merge")
	}
	mr := c.MRs[i]
	run.SHA, run.Finished, run.Outcome, run.Reason = mr.SHA, now, runs.OK, "merged"
	n := Note{Title: fmt.Sprintf("Autopilot merged !%d", mr.IID), Subtitle: ref(c), Body: mr.Title, ID: "autopilot:" + run.ID, URL: mr.WebURL}
	if err := mergeMR(mr); err != nil {
		run.Outcome, run.Reason = runs.Failed, err.Error()
		n.Title, n.Body = fmt.Sprintf("Autopilot couldn't merge !%d", mr.IID), err.Error()
	}
	if err := p.DB.Start(run); err != nil {
		return err
	}
	p.Notify(n)
	return nil
}

func colours(ss []board.Session) map[string]int {
	used := map[string]int{}
	for _, s := range ss {
		if s.IsLive() && s.Transcript != nil && s.Color != "" {
			used[s.Color]++
		}
	}
	return used
}

func ref(c board.Card) string {
	if c.IsReview() {
		return fmt.Sprintf("!%d %s", c.MR.IID, c.MR.Repo())
	}
	return c.Issue.Key + " " + c.Issue.Summary
}

var noFindings = regexp.MustCompile(`(?i)\bno findings\b`)

// pending pipelines may still go green.
var pending = []string{"created", "waiting_for_resource", "preparing", "pending", "running", "scheduled"}

// settled acts on review runs that came back: a clean one on the head it read
// and a green pipeline is approved, one with findings is yours to read.
func (p *Pilot) settled(now time.Time, cfg config.Config, in Inputs) {
	handled := p.DB.Prefixed(keyHandled)
	for id, at := range handled {
		if ms, _ := strconv.ParseInt(at, 10, 64); now.Sub(time.UnixMilli(ms)) > 48*time.Hour {
			_ = p.DB.Set(keyHandled+id, "")
		}
	}
	for _, r := range p.DB.Since(now.Add(-24 * time.Hour)) {
		if r.Step != "review" || r.Outcome == runs.Running || handled[r.ID] != "" {
			continue
		}
		i := slices.IndexFunc(in.GL.Review, func(mr gitlab.MR) bool { return mr.WebURL == r.Subject })
		if i < 0 {
			continue
		}
		mr := in.GL.Review[i]
		mref := fmt.Sprintf("!%d %s", mr.IID, mr.Repo())
		switch {
		case r.Outcome == runs.Failed:
		case r.Outcome != runs.OK || !noFindings.MatchString(r.Reason):
			p.Notify(Note{Title: fmt.Sprintf("Review of !%d found something", mr.IID), Subtitle: mref, Body: r.Reason, ID: "autopilot:findings:" + r.ID, URL: mr.WebURL})
		case !cfg.Autopilot.ReviewApprove() || mr.ApprovedByMe(in.GL.Me.Username) || r.SHA != mr.SHA:
		case slices.Contains(pending, mr.PipelineStatus()):
			continue
		case mr.PipelineStatus() == "success":
			p.approve(cfg, mr, mref)
		}
		_ = p.DB.Set(keyHandled+r.ID, strconv.FormatInt(now.UnixMilli(), 10))
	}
}

func (p *Pilot) approve(cfg config.Config, mr gitlab.MR, mref string) {
	n := Note{Title: fmt.Sprintf("Auto-approved !%d", mr.IID), Subtitle: mref, Body: mr.Title, ID: "autopilot:approved:" + mr.WebURL, URL: mr.WebURL}
	if err := approveMR(mr); err != nil {
		n.Title, n.Body = fmt.Sprintf("Couldn't approve !%d", mr.IID), err.Error()
		p.Notify(n)
		return
	}
	if emoji := cfg.Autopilot.ReviewReact(); emoji != "" {
		if err := p.react(mr, emoji); err != nil {
			n.Body += " · no Teams reaction: " + err.Error()
		}
	}
	p.Notify(n)
}

// react puts the emoji on the MR's newest Teams post, unless I already reacted.
// Teams not set up isn't an error; signed out is.
func (p *Pilot) react(mr gitlab.MR, emoji string) error {
	tc, err := newTeams()
	if errors.Is(err, teams.ErrUnconfigured) {
		return nil
	} else if err != nil {
		return err
	}
	posts, err := fetchReview(tc)
	if errors.Is(err, teams.ErrNoChannel) {
		return nil
	} else if err != nil {
		return err
	}
	ps := teams.Snapshot{Posts: posts}.ForMR(mr.WebURL)
	if len(ps) == 0 {
		return nil
	}
	me, err := teamsMe(tc)
	if err != nil {
		return err
	}
	if ps[0].Reacted(me) {
		return nil
	}
	return react(tc, ps[0].ID, emoji)
}
