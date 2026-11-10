package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MartyJRE/claudia/internal/autopilot"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/runs"
)

// apState is what the Board shows of the autopilot, read from state.db on
// every refresh: the watcher writes it, P and A here.
type apState struct {
	on      bool
	paused  bool
	status  autopilot.Status
	fresh   bool // the watcher wrote the status lately
	over    map[string]map[string]string
	planOK  map[string]string
	blocked map[string]string
}

func (m *Model) loadAutopilot() {
	m.ap = apState{on: getConfig().Autopilot.Enabled}
	if m.db == nil {
		return
	}
	_, m.ap.paused = m.db.Get(autopilot.KeyPaused)
	m.ap.status, m.ap.fresh = autopilot.ReadStatus(m.db)
	m.ap.fresh = m.ap.fresh && clockNow().Sub(m.ap.status.At) < 3*time.Minute
	m.ap.over = m.db.Overrides()
	m.ap.planOK = m.db.Prefixed(autopilot.KeyPlanOK)
	m.ap.blocked = m.db.Prefixed(autopilot.KeyBlocked)
}

// applyAutopilot reads how the card's next step starts, and how full the
// context of its running run is.
func (m *Model) applyAutopilot(c *boardCard) {
	a := getConfig().Autopilot
	c.auto = a.Enabled
	c.over = m.ap.over[c.id()]
	if !c.review() {
		c.planOK = m.ap.planOK[c.issue.Key] != ""
	}
	c.block = m.ap.blocked[c.id()]
	next, hold := nextStep(*c)
	switch {
	case hold != nil:
		if r, ok := m.sessionRow(hold.Session); ok && r.live() && r.Transcript != nil {
			c.ctx = r.Transcript.ContextFill()
		}
	case next != "":
		c.gate = autopilot.GateOf(a, c.over, c.card(), next, c.planOK)
	}
}

// autoTurn is the turn the autopilot gives a card whose next step isn't
// under way, and whether it has one to give.
func autoTurn(c boardCard, next string) (turn, string, string, bool) {
	if !c.auto || next == "" {
		return 0, "", "", false
	}
	label := stepLabel(next)
	switch {
	case c.gate == autopilot.Off:
		return turnYou, "", "", true
	case c.block != "":
		return turnNeedsYou, c.block, "", true
	case c.gate == autopilot.Ask:
		return turnNeedsYou, "go ahead with " + label + "?", "", true
	case c.gate == autopilot.PlanOK:
		return turnNeedsYou, "plan OK?", "", true
	case c.gate == autopilot.Backoff:
		return turnNeedsYou, label + " failed twice", "", true
	}
	return turnAgent, label + " queued", queuedVerb, true
}

const queuedVerb = "queued"

// stripHint is what the end of a card's strip asks of you, with the key for it.
func (m *Model) stripHint(c boardCard, next string, hold *runs.Run) (string, bool) {
	ask := func(text, key string) (string, bool) {
		return lipgloss.NewStyle().Foreground(yellow).Render("◆ " + text + " · " + key), true
	}
	switch {
	case hold != nil && hold.Outcome == runs.NeedsYou && hold.Reason == runs.ContextFull:
		return ask("context full", "enter")
	case hold != nil && hold.Outcome == runs.Waiting:
		return sDim.Render("⧗ " + cmp.Or(hold.Reason, "waiting")), true
	case hold != nil && waitingForAnswers(c):
		return sDim.Render("⧗ waiting for answers"), true
	case hold != nil && hold.Outcome == runs.NeedsYou:
		return ask("answer Claude", "enter")
	case hold != nil:
		return "", false
	case c.review() && c.rstage == rsFindings:
		return ask("check findings", "enter")
	}
	if _, _, _, ok := autoTurn(c, next); !ok || c.gate == autopilot.Off {
		return "", false
	}
	run := m.key("board.run")
	switch {
	case c.block != "":
		return ask(c.block, run)
	case c.gate == autopilot.Ask:
		return ask("go ahead with "+stepLabel(next)+"?", run)
	case c.gate == autopilot.PlanOK:
		return ask("plan OK?", m.key("board.plan_ok"))
	case c.gate == autopilot.Backoff:
		return ask(stepLabel(next)+" failed twice", run)
	}
	return lipgloss.NewStyle().Foreground(blue).Render("◷ " + stepLabel(next) + " queued"), true
}

// overrideChip names a card's own autopilot settings.
func overrideChip(over map[string]string) string {
	if len(over) == 0 {
		return ""
	}
	if all, ok := over[runs.AnyStep]; ok && len(over) == 1 {
		return sAccent.Render("⚙ " + all)
	}
	return sAccent.Render("⚙ custom")
}

func ctxChip(fill float64) string {
	if fill <= 0 {
		return ""
	}
	st := sDim
	if fill >= 0.8 {
		st = lipgloss.NewStyle().Foreground(yellow)
	}
	return st.Render(fmt.Sprintf("ctx %.0f%%", fill*100))
}

// autopilotPill is the header's word on the autopilot, when it's on.
func (m *Model) autopilotPill() string {
	if !m.ap.on {
		return ""
	}
	st := m.ap.status
	reason := ""
	switch {
	case m.ap.paused:
		reason = "paused"
	case !m.ap.fresh:
		reason = "the watcher isn't running"
	default:
		reason = st.Reason
	}
	if reason != "" {
		return pill(black, commentGrey, "⏸ autopilot: "+reason)
	}
	s := fmt.Sprintf("▶ autopilot %d/%d", st.Running, st.Max)
	if st.Unknown {
		s += " · usage unknown"
	}
	return pill(black, blue, s)
}

// modesLine lists the card's effective mode per step, ⚙ marking its own.
func modesLine(c boardCard) string {
	parts := []string{sDim.Render("autopilot")}
	for _, id := range cardSteps(c) {
		mode, own := autopilot.Mode(getConfig().Autopilot, c.over, id)
		s := sDim.Render(stepLabel(id) + " ")
		if own {
			s += sAccent.Render("⚙" + mode)
		} else {
			s += sText.Render(mode)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, sDim.Render("  "))
}

// cardSteps are the steps the autopilot can start on the card.
func cardSteps(c boardCard) []string {
	if c.review() {
		return []string{"review"}
	}
	var out []string
	for _, s := range ticketSteps {
		out = append(out, s.ID)
	}
	return out
}

func (m *Model) togglePause() {
	if m.db == nil {
		m.setFlash(fmt.Sprintf("the run log isn't open: %v", m.dbErr), true)
		return
	}
	if !getConfig().Autopilot.Enabled {
		m.setFlash("the autopilot is off: turn on autopilot.enabled in settings", false)
		return
	}
	v, msg := "1", "autopilot paused: nothing new starts until "+m.key("board.pause")
	if m.ap.paused {
		v, msg = "", "autopilot resumed"
	}
	if err := m.db.Set(autopilot.KeyPaused, v); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.setFlash(msg, false)
	m.refilter()
}

// planOK is p: the card's plan is good to implement. Pressed again, it's taken back.
func (m *Model) planOK(c boardCard) {
	if c.review() || m.db == nil {
		m.setFlash("only a ticket's plan gets an OK", true)
		return
	}
	key := autopilot.KeyPlanOK + c.issue.Key
	v, msg := strconv.FormatInt(clockNow().UnixMilli(), 10), "plan of "+c.issue.Key+" OK: implement goes ahead"
	if c.planOK {
		v, msg = "", "plan OK of "+c.issue.Key+" taken back"
	}
	if err := m.db.Set(key, v); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.setFlash(msg, false)
	m.refilter()
}

// overrideChooser is A: the card's own autopilot settings.
func (m *Model) overrideChooser(c boardCard) {
	if m.db == nil {
		m.setFlash(fmt.Sprintf("the run log isn't open: %v", m.dbErr), true)
		return
	}
	id, ref := c.id(), c.ref()
	set := func(mode string) func(*Model) {
		return func(m *Model) {
			err := m.db.ClearOverrides(id)
			if err == nil && mode != "" {
				err = m.db.SetOverride(id, runs.AnyStep, mode, clockNow())
			}
			m.overrideSet(ref, err)
		}
	}
	items := []choice{
		{label: "use my settings", search: "use my settings", value: set("")},
		{label: "everything auto", search: "everything auto", value: set(config.ModeAuto)},
		{label: "ask before every step", search: "ask before every step", value: set(config.ModeAsk)},
		{label: "off (manual only)", search: "off manual only", value: set(config.ModeOff)},
		{label: "per step…", search: "per step", value: func(m *Model) { m.stepChooser(c) }},
	}
	m.chooser = newChooser("autopilot for "+ref, items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		ch.value.(func(*Model))(m)
		return m, nil
	})
}

func (m *Model) stepChooser(c boardCard) {
	a := getConfig().Autopilot
	var items []choice
	for _, id := range cardSteps(c) {
		mode, own := autopilot.Mode(a, c.over, id)
		hint := "yours"
		if own {
			hint = "this card"
		}
		items = append(items, choice{label: stepLabel(id) + ": " + mode, hint: hint, search: stepLabel(id), value: id})
	}
	m.chooser = newChooser("autopilot for "+c.ref()+" by step", items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		m.modeChooser(c, ch.value.(string))
		return m, nil
	})
}

func (m *Model) modeChooser(c boardCard, step string) {
	id, ref := c.id(), c.ref()
	modes := []string{config.ModeAuto, config.ModeAsk, config.ModeOff}
	if step == "implement" {
		modes = slices.Insert(modes, 1, config.ModeAfterPlanOK)
	}
	items := []choice{{label: "use my setting (" + getConfig().Autopilot.Mode(step) + ")", search: "use my setting", value: ""}}
	for _, mode := range modes {
		items = append(items, choice{label: mode, search: mode, value: mode})
	}
	m.chooser = newChooser(stepLabel(step)+" on "+ref, items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		mode := ch.value.(string)
		m.overrideSet(ref, m.db.SetOverride(id, step, mode, clockNow()))
		if step == "merge" && mode == config.ModeAuto && !getConfig().Autopilot.AllowAutoMerge {
			m.setFlash("merge still asks: auto merge needs autopilot.allow_auto_merge in settings", false)
		}
		return m, nil
	})
}

func (m *Model) overrideSet(ref string, err error) {
	if err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.setFlash("autopilot for "+ref+" set", false)
	m.refilter()
}
