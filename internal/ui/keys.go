package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MartyJRE/claudia/internal/config"
)

// Every key outside text input and choosers goes through a named action, so
// config.json can rebind it. The scope is the part of the id before the dot.
type action struct {
	id   string
	keys []string
	help string
}

var actions = []action{
	{"global.quit", []string{"q", "ctrl+c"}, "quit"},
	{"global.back", []string{"esc"}, "clear filter / quit"},
	{"global.up", []string{"up", "k", "ctrl+p"}, "up"},
	{"global.down", []string{"down", "j", "ctrl+n"}, "down"},
	{"global.top", []string{"g", "home"}, "top"},
	{"global.bottom", []string{"G", "end"}, "bottom"},
	{"global.next_view", []string{"tab", "right", "l"}, "next view"},
	{"global.prev_view", []string{"shift+tab", "left", "h"}, "previous view"},
	{"global.assistant", []string{"@"}, "assistant"},
	{"global.update", []string{"ctrl+u"}, "update claudia"},
	{"global.releases", []string{"W"}, "what's new"},
	{"global.settings", []string{","}, "settings"},
	{"global.open_link", []string{"u"}, "open link"},
	{"global.ticket_notes", []string{"N"}, "ticket's notes"},
	{"global.write_note", []string{"w"}, "write note"},
	{"global.new_session", []string{"n"}, "new"},
	{"global.filter", []string{"/"}, "filter"},
	{"global.refresh", []string{"ctrl+r"}, "refresh"},
	{"global.visual", []string{"V"}, "select rows"},

	{"visual.claude", []string{"c"}, "claude on the selection"},
	{"visual.claude_bg", []string{"C"}, "claude on the selection, in the background"},

	{"notes.read", []string{"enter"}, "read"},
	{"notes.fold", []string{" "}, "fold"},
	{"notes.layout", []string{"v"}, "layout"},
	{"notes.edit", []string{"e"}, "edit"},
	{"notes.obsidian", []string{"o"}, "Obsidian"},
	{"notes.copy", []string{"y"}, "copy path"},
	{"notes.ticket", []string{"J"}, "ticket"},

	{"inbox.open", []string{"enter", "o"}, "open"},
	{"inbox.all", []string{"a"}, "show snoozed"},
	{"inbox.brief", []string{"R"}, "brief"},
	{"inbox.review", []string{"r"}, "review with claude"},
	{"inbox.threads", []string{"t"}, "threads"},
	{"inbox.rebase", []string{"B"}, "rebase"},
	{"inbox.approve", []string{"A"}, "approve"},
	{"inbox.snooze", []string{"x"}, "snooze"},
	{"inbox.ticket", []string{"J"}, "ticket"},
	{"inbox.teams", []string{"T"}, "teams post"},
	{"inbox.claude", []string{"c"}, "claude does what fits"},
	{"inbox.claude_bg", []string{"C"}, "same, in the background"},
	{"inbox.plan", []string{"P"}, "comment on the plan"},
	{"inbox.layout", []string{"v"}, "layout"},
	{"inbox.fold", []string{" "}, "fold"},
	{"inbox.fold_all", []string{"-"}, "fold all"},
	{"inbox.unfold_all", []string{"+", "="}, "unfold all"},

	{"board.left", []string{"h", "left"}, "column left"},
	{"board.right", []string{"l", "right"}, "column right"},
	{"board.sessions", []string{"enter"}, "sessions"},
	{"board.claude", []string{"c"}, "claude"},
	{"board.claude_bg", []string{"C"}, "claude in the background"},
	{"board.open", []string{"o"}, "open in Jira"},
	{"board.mr", []string{"M"}, "open MR"},
	{"board.threads", []string{"t"}, "threads"},
	{"board.move", []string{"m"}, "move"},
	{"board.copy", []string{"y"}, "copy key"},
	{"board.team", []string{"a"}, "mine/team"},
	{"board.done", []string{"d"}, "show done"},

	{"usage.layout", []string{"v"}, "layout"},

	{"sessions.jump", []string{"enter"}, "jump"},
	{"sessions.fold", []string{" "}, "fold"},
	{"sessions.fold_all", []string{"-"}, "fold all"},
	{"sessions.unfold_all", []string{"+", "="}, "unfold all"},
	{"sessions.group", []string{"v"}, "layout"},
	{"sessions.all", []string{"a"}, "show closed"},
	{"sessions.resume", []string{"R"}, "resume"},
	{"sessions.close", []string{"K"}, "kill"},
	{"sessions.mark", []string{"m"}, "mark"},
	{"sessions.split", []string{"S"}, "split marked"},
	{"sessions.unsplit", []string{"U"}, "unsplit"},
	{"sessions.ticket", []string{"J"}, "ticket"},
	{"sessions.set_ticket", []string{"t"}, "set ticket"},
	{"sessions.mr", []string{"o"}, "MR"},
	{"sessions.copy_id", []string{"y"}, "copy id"},
	{"sessions.plan", []string{"P"}, "comment on the plan"},
	{"sessions.restore", []string{"X"}, "restore"},
	{"sessions.forget_restore", []string{"ctrl+x"}, "forget restore"},

	{"mrs.primary", []string{"enter"}, "review with claude / session"},
	{"mrs.all", []string{"a"}, "show all"},
	{"mrs.open", []string{"o"}, "open"},
	{"mrs.pipeline", []string{"p"}, "pipeline"},
	{"mrs.seen", []string{"s"}, "seen"},
	{"mrs.copy", []string{"y"}, "copy link"},
	{"mrs.sessions", []string{"c"}, "sessions"},
	{"mrs.threads", []string{"t"}, "threads"},
	{"mrs.fix", []string{"f"}, "fix threads"},
	{"mrs.rebase", []string{"B"}, "rebase"},
	{"mrs.ticket", []string{"J"}, "ticket"},
	{"mrs.teams", []string{"T"}, "teams post"},
	{"mrs.approve", []string{"A"}, "approve"},
	{"mrs.unapprove", []string{"U"}, "unapprove"},
	{"mrs.claude_bg", []string{"C"}, "fix threads in the background"},
	{"mrs.layout", []string{"v"}, "layout"},
	{"mrs.fold", []string{" "}, "fold"},
	{"mrs.fold_all", []string{"-"}, "fold all"},
	{"mrs.unfold_all", []string{"+", "="}, "unfold all"},

	{"jira.sessions", []string{"enter"}, "sessions"},
	{"jira.team", []string{"a"}, "mine/team"},
	{"jira.done", []string{"d"}, "done"},
	{"jira.sort", []string{"s"}, "sort"},
	{"jira.statuses", []string{"f"}, "statuses"},
	{"jira.fold", []string{" "}, "fold"},
	{"jira.fold_all", []string{"-"}, "fold all"},
	{"jira.unfold_all", []string{"+", "="}, "unfold all"},
	{"jira.layout", []string{"v"}, "layout"},
	{"jira.open", []string{"o"}, "open"},
	{"jira.copy", []string{"y"}, "copy key"},
	{"jira.claude", []string{"c"}, "claude"},
	{"jira.claude_bg", []string{"C"}, "claude in the background"},
	{"jira.summary", []string{"b"}, "summary"},
	{"jira.move", []string{"m"}, "move"},
	{"jira.copy_brief", []string{"e"}, "copy brief"},
	{"jira.images", []string{"i"}, "images"},

	{"mentions.open", []string{"enter", "o"}, "open"},
	{"mentions.all", []string{"a"}, "show handled"},
	{"mentions.dismiss", []string{"x"}, "dismiss"},
	{"mentions.ticket", []string{"J"}, "ticket"},
	{"mentions.copy", []string{"y"}, "copy link"},
	{"mentions.reply", []string{"c"}, "draft reply"},
	{"mentions.reply_bg", []string{"C"}, "draft reply in the background"},
	{"mentions.layout", []string{"v"}, "layout"},
	{"mentions.fold", []string{" "}, "fold"},
	{"mentions.fold_all", []string{"-"}, "fold all"},
	{"mentions.unfold_all", []string{"+", "="}, "unfold all"},

	{"confluence.open", []string{"enter", "o"}, "open"},
	{"confluence.copy", []string{"y"}, "copy link"},
	{"confluence.layout", []string{"v"}, "layout"},
	{"confluence.fold", []string{" "}, "fold"},
	{"confluence.fold_all", []string{"-"}, "fold all"},
	{"confluence.unfold_all", []string{"+", "="}, "unfold all"},
}

// mayShadow lists the global actions a view's action may take keys from on
// purpose: on the Board h/l move between columns, and tab still switches views.
var mayShadow = map[string][]string{
	"board.left":  {"global.prev_view"},
	"board.right": {"global.next_view"},
}

type keymap struct {
	byScope map[string]map[string]string // scope → key → action id
	keys    map[string][]string          // action id → keys
}

// newKeymap applies the overrides and reports unknown actions and keys bound
// twice where both would apply.
func newKeymap(over map[string]config.KeyList) (keymap, []string) {
	km := keymap{byScope: map[string]map[string]string{}, keys: map[string][]string{}}
	var problems []string
	for id := range over {
		if !slices.ContainsFunc(actions, func(a action) bool { return a.id == id }) {
			problems = append(problems, "unknown key action "+id)
		}
	}
	for _, a := range actions {
		keys := a.keys
		if o, ok := over[a.id]; ok {
			keys = o
		}
		km.keys[a.id] = keys
		sc := scopeOf(a.id)
		if km.byScope[sc] == nil {
			km.byScope[sc] = map[string]string{}
		}
		for _, k := range keys {
			if other := km.byScope[sc][k]; other != "" {
				problems = append(problems, fmt.Sprintf("%s is bound to both %s and %s", keyName(k), other, a.id))
				continue
			}
			km.byScope[sc][k] = a.id
		}
	}
	for sc, m := range km.byScope {
		if sc == "global" {
			continue
		}
		for k, id := range m {
			if g := km.byScope["global"][k]; g != "" && !slices.Contains(mayShadow[id], g) {
				problems = append(problems, fmt.Sprintf("%s is bound to both %s and %s", keyName(k), g, id))
			}
		}
	}
	slices.Sort(problems)
	return km, problems
}

func scopeOf(id string) string {
	sc, _, _ := strings.Cut(id, ".")
	return sc
}

func viewScope(v view) string {
	switch v {
	case vNotes:
		return "notes"
	case vInbox:
		return "inbox"
	case vBoard:
		return "board"
	case vUsage:
		return "usage"
	case vSessions:
		return "sessions"
	case vReview, vMine:
		return "mrs"
	case vJira:
		return "jira"
	case vMentions:
		return "mentions"
	case vConfluence:
		return "confluence"
	}
	return ""
}

// action resolves a pressed key in the current view, falling back to global.
func (m *Model) action(k string) string {
	if id := m.km.byScope[viewScope(m.view)][k]; id != "" {
		return id
	}
	return m.km.byScope["global"][k]
}

// key is what the footer shows for an action: its first bound key.
func (m *Model) key(id string) string {
	if ks := m.km.keys[id]; len(ks) > 0 {
		return keyName(ks[0])
	}
	return "—"
}

func keyName(k string) string {
	if k == " " {
		return "space"
	}
	return k
}

// hints builds footer entries from action ids, labelled with their help.
func (m *Model) hints(ids ...string) [][2]string {
	var out [][2]string
	for _, id := range ids {
		label := ""
		for _, a := range actions {
			if a.id == id {
				label = a.help
			}
		}
		out = append(out, m.hint(id, label))
	}
	return out
}

func (m *Model) hint(id, label string) [2]string { return [2]string{m.key(id), label} }

// KeyActions lists every action with its keys under the given overrides, for
// `claudia keys`.
func KeyActions() string {
	km, problems := newKeymap(getConfig().Keys)
	var b strings.Builder
	for _, a := range actions {
		var ks []string
		for _, k := range km.keys[a.id] {
			ks = append(ks, keyName(k))
		}
		fmt.Fprintf(&b, "%-26s %-22s %s\n", a.id, strings.Join(ks, " "), a.help)
	}
	for _, p := range problems {
		fmt.Fprintln(&b, "problem:", p)
	}
	return b.String()
}
