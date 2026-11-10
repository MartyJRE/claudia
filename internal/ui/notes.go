package ui

import (
	"cmp"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MartyJRE/claudia/internal/atlassian"
	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/vault"
)

// Obsidian notes next to tickets: listed on every item that has a ticket,
// readable in a pager, edited in $EDITOR, and browsable in the Notes tab.

//go:embed onedark.json
var onedarkStyle []byte

func newVault() *vault.Index {
	c := getConfig()
	if c.Vault == "" {
		return nil
	}
	if st, err := os.Stat(c.Vault); err != nil || !st.IsDir() {
		return nil
	}
	return vault.New(c.Vault, c.VaultProjects, claude.TicketRegexp())
}

type vaultMsg []vault.Note

func (m *Model) refreshVault() tea.Cmd {
	v := m.vault
	if v == nil || m.vaultLoading {
		return nil
	}
	m.vaultLoading = true
	return func() tea.Msg {
		v.Refresh()
		return vaultMsg(v.All())
	}
}

// ticketKeys is the key plus its children's keys, so an epic lists the notes of
// its stories too.
func (m *Model) ticketKeys(key string) []string {
	if key == "" {
		return nil
	}
	keys := []string{key}
	for _, list := range [][]atlassian.Issue{m.jira.Sprint, m.jira.Team} {
		for _, is := range list {
			if is.Parent == key && !slices.Contains(keys, is.Key) {
				keys = append(keys, is.Key)
			}
		}
	}
	return keys
}

func (m *Model) notesFor(key string) []vault.Note {
	if m.vault == nil || key == "" {
		return nil
	}
	return m.vault.For(m.ticketKeys(key)...)
}

// notesLine is the detail-panel summary of a ticket's notes.
func (m *Model) notesLine(key string) string {
	ns := m.notesFor(key)
	if len(ns) == 0 {
		return ""
	}
	var titles []string
	for _, n := range ns {
		titles = append(titles, n.Title)
	}
	return lipgloss.NewStyle().Foreground(yellow).Render(fmt.Sprintf("📝 %d note(s): ", len(ns))) +
		sText.Render(strings.Join(titles, sDim.Render(" · "))) + sDim.Render("   N to read")
}

// withNotes puts the notes line third in a detail panel, where it survives the
// panel's height limit.
func (m *Model) withNotes(lines []string, key string) []string {
	l := m.notesLine(key)
	if l == "" {
		return lines
	}
	i := min(2, len(lines))
	return append(lines[:i], append([]string{l}, lines[i:]...)...)
}

// currentTicket is the ticket behind whatever the cursor is on.
func (m *Model) currentTicket() string {
	switch m.view {
	case vInbox:
		if it, ok := m.currentInbox(); ok {
			return it.ticket
		}
	case vSessions:
		if r, ok := m.currentSession(); ok {
			return m.ix.sessionTicket[r.ID]
		}
	case vBoard:
		if c, ok := m.currentCard(); ok && c.review() {
			return mrTicket(*c.mr)
		} else if ok {
			return c.issue.Key
		}
	case vReview, vMine:
		if r, ok := m.currentMR(); ok {
			return mrTicket(r.MR)
		}
	case vJira:
		if is, ok := m.currentIssue(); ok {
			return is.Key
		}
	case vMentions:
		if x, ok := m.currentMention(); ok && x.jira {
			return x.where
		}
	}
	return ""
}

func (m *Model) chooseNotes() {
	if m.vault == nil {
		m.setFlash("set \"vault\" in "+config.Path()+" to see notes", true)
		return
	}
	key := m.currentTicket()
	ns := m.notesFor(key)
	if len(ns) == 0 {
		if key == "" {
			m.setFlash("no ticket here", true)
		} else {
			m.setFlash("no notes for "+key+" yet — w writes one", false)
		}
		return
	}
	var items []choice
	for _, n := range ns {
		items = append(items, choice{label: sText.Render(n.Title), hint: n.Folder() + " · " + age(n.Modified), search: n.Rel, value: n})
	}
	m.chooser = newChooser("notes for "+key, items, func(m *Model, ch choice) (tea.Model, tea.Cmd) {
		n := ch.value.(vault.Note)
		m.openReader(n)
		return m, nil
	})
}

// writeNote asks for a title and creates the note in the ticket's project folder.
func (m *Model) writeNote() tea.Cmd {
	if m.vault == nil {
		m.setFlash("set \"vault\" in "+config.Path()+" to write notes", true)
		return nil
	}
	key := m.currentTicket()
	if key == "" {
		m.setFlash("no ticket here to write about", true)
		return nil
	}
	in := newInput("title › ", "what is this note about")
	if is, ok := m.ix.issues[key]; ok {
		in.SetValue(is.Summary)
		in.CursorEnd()
	}
	in.Width = max(40, m.width-12)
	m.prompt = &promptBox{title: "new note in " + m.vault.Projects + "/" + key, in: in, submit: func(m *Model, title string) (tea.Model, tea.Cmd) {
		path, err := m.vault.NewNote(key, title, []string{strings.ToLower(key)})
		if err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		return m, m.edit(path)
	}}
	return m.prompt.in.Focus()
}

type editedMsg struct {
	path string
	err  error
}

// edit hands the terminal to the user's editor and refreshes after.
func (m *Model) edit(path string) tea.Cmd {
	return execProcess(editorCmd(path), func(err error) tea.Msg { return editedMsg{path, err} })
}

// editorCmd opens path in $VISUAL, else $EDITOR, else vi. It goes through the
// shell, so an editor set with arguments ("code -w") works.
func editorCmd(path string) *exec.Cmd {
	editor := cmp.Or(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	return exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
}

func obsidianURL(root, rel string) string {
	return "obsidian://open?vault=" + url.QueryEscape(filepath.Base(root)) +
		"&file=" + url.QueryEscape(strings.TrimSuffix(rel, ".md"))
}

// ── reader ─────────────────────────────────────────────────────────────────

type reader struct {
	note  vault.Note
	vp    viewport.Model
	width int
}

func (m *Model) openReader(n vault.Note) {
	m.reader = &reader{note: n}
	m.renderReader()
}

func (m *Model) readerSize() (int, int) {
	return m.width, max(5, m.height-4)
}

var (
	embedRe   = regexp.MustCompile(`!\[\[([^\]]+)\]\]`)
	aliasRe   = regexp.MustCompile(`\[\[([^\]|]+)\|([^\]]+)\]\]`)
	wikiRe    = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	tagsRe    = regexp.MustCompile(`(?m)^tags:\s*(.+)$`)
	dateRe    = regexp.MustCompile(`(?m)^date:\s*(.+)$`)
	renderers = map[int]*glamour.TermRenderer{}
)

// obsidianToMarkdown strips the frontmatter into a short header line and turns
// wikilinks and embeds into text glamour can draw.
func obsidianToMarkdown(src string) string {
	meta := ""
	if rest, ok := strings.CutPrefix(src, "---\n"); ok {
		if fm, body, ok := strings.Cut(rest, "\n---\n"); ok {
			var parts []string
			if d := dateRe.FindStringSubmatch(fm); d != nil {
				parts = append(parts, strings.TrimSpace(d[1]))
			}
			if t := tagsRe.FindStringSubmatch(fm); t != nil {
				parts = append(parts, strings.Trim(strings.TrimSpace(t[1]), "[]"))
			}
			if len(parts) > 0 {
				meta = "_" + strings.Join(parts, " · ") + "_\n\n"
			}
			src = body
		}
	}
	src = embedRe.ReplaceAllString(src, "🖼 `$1`")
	src = aliasRe.ReplaceAllString(src, "**$2**")
	src = wikiRe.ReplaceAllString(src, "**$1**")
	return meta + src
}

// renderMarkdown draws md with glamour wrapped at wrap, or returns it as it is
// if glamour can't.
func renderMarkdown(md string, wrap int) string {
	g := renderers[wrap]
	var err error
	if g == nil {
		g, err = glamour.NewTermRenderer(glamour.WithStylesFromJSONBytes(onedarkStyle), glamour.WithWordWrap(wrap))
		if err == nil {
			renderers[wrap] = g
		}
	}
	text := ""
	if err == nil {
		text, err = g.Render(md)
	}
	if err != nil {
		return md
	}
	return text
}

func (m *Model) renderReader() {
	r := m.reader
	w, h := m.readerSize()
	body, err := os.ReadFile(r.note.Path)
	text := ""
	if err != nil {
		text = sErr.Render(err.Error())
	} else {
		text = renderMarkdown(obsidianToMarkdown(string(body)), min(w-4, 110))
	}
	off := 0
	if r.width == w {
		off = r.vp.YOffset
	}
	r.vp = viewport.New(w, h)
	r.vp.SetContent(text)
	r.vp.SetYOffset(off)
	r.width = w
}

func (m *Model) updateReader(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.reader
	switch msg.String() {
	case "esc", "q", "backspace":
		m.reader = nil
		return m, nil
	case "e":
		return m, m.edit(r.note.Path)
	case "o":
		return m, openURL(obsidianURL(m.vault.Root, r.note.Rel))
	case "y":
		return m, copyText(r.note.Path, "note path")
	case "g", "home":
		r.vp.GotoTop()
		return m, nil
	case "G", "end":
		r.vp.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	r.vp, cmd = r.vp.Update(msg)
	return m, cmd
}

func (m *Model) viewReader() string {
	r := m.reader
	title := sAccent.Render(" 📝 "+r.note.Title) + sDim.Render("  "+r.note.Folder()+" · edited "+age(r.note.Modified)+" ago")
	if ks := r.note.Keys; len(ks) > 0 {
		more := ""
		if len(ks) > 3 {
			ks, more = ks[:3], sDim.Render(fmt.Sprintf(" +%d", len(r.note.Keys)-3))
		}
		title += "  " + sTicket.Render(strings.Join(ks, " ")) + more
	}
	title = ansi.Truncate(title, m.width, "…")
	pct := fmt.Sprintf("%3.0f%%", r.vp.ScrollPercent()*100)
	keys := [][2]string{{"j/k", "scroll"}, {"e", "edit"}, {"o", "open in Obsidian"}, {"y", "copy path"}, {"esc", "back"}}
	var parts []string
	for _, k := range keys {
		parts = append(parts, sKey.Render(k[0])+" "+sDim.Render(k[1]))
	}
	footer := " " + strings.Join(parts, sRule.Render("  │  ")) + "  " + sDim.Render(pct)
	return lipgloss.JoinVertical(lipgloss.Left, title, sRule.Render(strings.Repeat("─", m.width)), r.vp.View(), footer)
}

// ── Notes tab ──────────────────────────────────────────────────────────────

type nrow struct {
	folder    string // folder rows: the path relative to the vault
	note      vault.Note
	depth     int
	open      bool
	count     int
	flatTitle bool   // recent/filter lists show the folder next to the title
	via       string // tree: the folder(s) of a lone note, drawn in their place
}

func (r nrow) isFolder() bool { return r.folder != "" }

const notesPrefsFile = "notes-prefs.json"

type notesPrefs struct {
	Open   []string `json:"open"`
	Recent bool     `json:"recent"`
}

func loadNotesPrefs() notesPrefs {
	var p notesPrefs
	state.Load(notesPrefsFile, &p)
	return p
}

func (m *Model) saveNotesPrefs() { _ = state.Save(notesPrefsFile, m.notesPrefs) }

func (m *Model) filterNotes(match func(...string) bool, filtering bool) []nrow {
	if m.vault == nil {
		return nil
	}
	all := m.vaultNotes
	if filtering || m.notesPrefs.Recent {
		var rows []nrow
		for _, n := range all {
			if match(n.Title, n.Rel, strings.Join(n.Keys, " ")) {
				rows = append(rows, nrow{note: n, flatTitle: true})
			}
			if !filtering && len(rows) >= 200 {
				break
			}
		}
		return rows
	}
	// Folder tree: folders first (alphabetical), then notes by recency.
	type dir struct {
		notes []vault.Note
		subs  map[string]bool
	}
	dirs := map[string]*dir{".": {subs: map[string]bool{}}}
	get := func(p string) *dir {
		if dirs[p] == nil {
			dirs[p] = &dir{subs: map[string]bool{}}
		}
		return dirs[p]
	}
	counts := map[string]int{}
	only := map[string]vault.Note{} // a folder's note, where it holds just one
	for _, n := range all {
		f := n.Folder()
		get(f).notes = append(get(f).notes, n)
		for p := f; p != "."; p = filepath.Dir(p) {
			counts[p]++
			only[p] = n
			get(filepath.Dir(p)).subs[p] = true
			get(p)
		}
	}
	var rows []nrow
	var walk func(p string, depth int)
	walk = func(p string, depth int) {
		d := dirs[p]
		var subs []string
		for s := range d.subs {
			subs = append(subs, s)
		}
		slices.Sort(subs)
		for _, s := range subs {
			if counts[s] == 1 {
				n := only[s]
				rows = append(rows, nrow{note: n, depth: depth, via: strings.TrimPrefix(n.Folder(), p+"/")})
				continue
			}
			open := slices.Contains(m.notesPrefs.Open, s)
			rows = append(rows, nrow{folder: s, depth: depth, open: open, count: counts[s]})
			if open {
				walk(s, depth+1)
			}
		}
		for _, n := range d.notes {
			rows = append(rows, nrow{note: n, depth: depth})
		}
	}
	walk(".", 0)
	return rows
}

var noteCols = []col{
	{"NOTE", 0, 0},
	{"TICKETS", 22, 110},
	{"EDITED", 7, 0},
}

func (m *Model) listNotes(h int) string {
	rows := m.vis.notes
	empty := "no notes"
	if m.vault == nil {
		empty = "set \"vault\" in " + config.Path() + " to browse your notes"
	} else if m.vaultNotes == nil {
		empty = m.spin() + " reading the vault…"
	}
	return m.table(noteCols, m.vis.widths.notes, len(rows), &m.cur[vNotes], h, m.noteCells, empty)
}

func (m *Model) noteCells(i int, sel bool) []string {
	r := m.vis.notes[i]
	indent := strings.Repeat("  ", r.depth)
	if r.isFolder() {
		glyph := "▸ "
		if r.open {
			glyph = "▾ "
		}
		return []string{sDim.Render(indent) + sAccent.Render(glyph) + sBold.Render(filepath.Base(r.folder)) + sDim.Render(fmt.Sprintf("  %d", r.count)), "", ""}
	}
	st := sText
	if sel {
		st = sBold
	}
	title := sDim.Render(indent+"  ") + st.Render(r.note.Title)
	if r.via != "" {
		title += sDim.Render("  " + r.via)
	}
	if r.flatTitle {
		title = st.Render(r.note.Title) + sDim.Render("  "+r.note.Folder())
	}
	return []string{title, sTicket.Render(strings.Join(r.note.Keys, " ")), sDim.Render(age(r.note.Modified))}
}

func (m *Model) currentNoteRow() (nrow, bool) {
	c := m.cur[vNotes].pos
	if c < 0 || c >= len(m.vis.notes) {
		return nrow{}, false
	}
	return m.vis.notes[c], true
}

func (m *Model) detailNote() string {
	r, ok := m.currentNoteRow()
	if !ok {
		return m.panel(nil)
	}
	if r.isFolder() {
		return m.panel([]string{sBold.Render(r.folder), sDim.Render(fmt.Sprintf("%d notes · space or enter opens", r.count))})
	}
	lines := []string{sBold.Render(r.note.Title) + sDim.Render("  "+r.note.Rel)}
	if len(r.note.Keys) > 0 {
		var ks []string
		for _, k := range r.note.Keys {
			s := sTicket.Render(k)
			if is, ok := m.ix.issues[k]; ok {
				s += sDim.Render(" "+is.Summary+" · ") + statusText(is)
			}
			ks = append(ks, s)
		}
		lines = append(lines, strings.Join(ks, "   "))
	}
	if b, err := os.ReadFile(r.note.Path); err == nil {
		body := string(b)
		if rest, ok := strings.CutPrefix(body, "---\n"); ok {
			if _, after, ok := strings.Cut(rest, "\n---\n"); ok {
				body = after
			}
		}
		for _, l := range strings.Split(body, "\n") {
			if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			lines = append(lines, sDim.Render(wikiRe.ReplaceAllString(l, "$1")))
			if len(lines) >= 5 {
				break
			}
		}
	}
	return m.panel(lines)
}

func (m *Model) keyNotes(k string) (tea.Model, tea.Cmd) {
	r, ok := m.currentNoteRow()
	act := m.action(k)
	switch act {
	case "notes.layout":
		m.notesPrefs.Recent = !m.notesPrefs.Recent
		m.saveNotesPrefs()
		m.cur[vNotes] = cursor{}
		m.refilter()
		m.setFlash("layout: folders", false)
		if m.notesPrefs.Recent {
			m.setFlash("layout: recently edited", false)
		}
		return m, nil
	case "notes.fold", "notes.read":
		if !ok {
			return m, nil
		}
		if r.isFolder() {
			if i := slices.Index(m.notesPrefs.Open, r.folder); i >= 0 {
				m.notesPrefs.Open = slices.Delete(m.notesPrefs.Open, i, i+1)
			} else {
				m.notesPrefs.Open = append(m.notesPrefs.Open, r.folder)
			}
			m.saveNotesPrefs()
			m.refilter()
			return m, nil
		}
		if act == "notes.read" {
			m.openReader(r.note)
		}
	case "notes.edit":
		if ok && !r.isFolder() {
			return m, m.edit(r.note.Path)
		}
	case "notes.obsidian":
		if ok && !r.isFolder() {
			return m, openURL(obsidianURL(m.vault.Root, r.note.Rel))
		}
	case "notes.copy":
		if ok && !r.isFolder() {
			return m, copyText(r.note.Path, "note path")
		}
	case "notes.ticket":
		if ok && !r.isFolder() && len(r.note.Keys) > 0 {
			return m.gotoTicket(r.note.Keys[0])
		}
	}
	return m, nil
}

func (m *Model) applyVault(notes []vault.Note) {
	m.vaultNotes = notes
	m.vaultLoading = false
	m.vaultAt = time.Now()
	if m.reader != nil {
		for _, n := range notes {
			if n.Path == m.reader.note.Path {
				m.reader.note = n
			}
		}
		m.renderReader()
	}
	m.refilter()
}
