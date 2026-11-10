package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/vault"
)

func vaultViews(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Notes")
	base := time.Now().Add(-10 * time.Hour)
	files := []struct {
		rel, body string
		ago       time.Duration
	}{
		{"01 - Projects/ABC-2/Plan.md", "---\ndate: 2026-10-01T09:00\ntags: [abc-2, payments]\n---\n\n# Plan\n\nSplit the [[Card form]] in two.\n\nAsk [[Ann Smith|Ann]] about it.\n", 1},
		{"01 - Projects/ABC-2/Sub/Deep.md", "deep thoughts", 3},
		{"04 - Permanent/Teams.md", "who owns [[ABC-3]]", 2},
		{"Inbox.md", "nothing", 5},
	}
	for _, f := range files {
		p := filepath.Join(root, f.rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		at := base.Add(-f.ago * time.Hour)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func notesModelViews(t *testing.T) *Model {
	m := jiraModelViews(t)
	m.vault = vault.New(vaultViews(t), "01 - Projects", claude.TicketRegexp())
	m.view = vNotes
	m.vaultLoading = true
	m.vault.Refresh()
	m.Update(vaultMsg(m.vault.All()))
	return m
}

func noteRowsViews(rows []nrow) string {
	var b []string
	for _, r := range rows {
		s := strings.Repeat(" ", r.depth)
		if r.isFolder() {
			s += "#" + filepath.Base(r.folder)
			if r.open {
				s += "-"
			}
		} else {
			s += r.note.Title
		}
		b = append(b, s)
	}
	return strings.Join(b, "|")
}

func cursorToNoteViews(t *testing.T, m *Model, name string) {
	t.Helper()
	for i, r := range m.vis.notes {
		if r.note.Title == name || r.isFolder() && "#"+filepath.Base(r.folder) == name {
			m.cur[vNotes].pos = i
			return
		}
	}
	t.Fatalf("%s not visible: %s", name, noteRowsViews(m.vis.notes))
}

func TestNotesTree(t *testing.T) {
	m := notesModelViews(t)
	if m.vaultLoading || m.vaultAt.IsZero() {
		t.Error("vaultMsg finishes loading")
	}
	if got, want := noteRowsViews(m.vis.notes), "#01 - Projects|Teams|Inbox"; got != want {
		t.Fatalf("closed tree: got %s want %s", got, want)
	}
	s := screenViews(m)
	for _, want := range []string{"▸ 01 - Projects  2", "Teams  04 - Permanent", "Inbox", "your vault"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
	if d := plainViews(m.detailNote()); !strings.Contains(d, "01 - Projects") || !strings.Contains(d, "2 notes · space or enter opens") {
		t.Errorf("folder detail:\n%s", d)
	}

	pressViews(m, " ")
	cursorToNoteViews(t, m, "#ABC-2")
	pressViews(m, "enter")
	if got, want := noteRowsViews(m.vis.notes), "#01 - Projects-| #ABC-2-|  Deep|  Plan|Teams|Inbox"; got != want {
		t.Errorf("open tree: got %s want %s", got, want)
	}
	if p := loadNotesPrefs(); len(p.Open) != 2 {
		t.Errorf("open folders are saved: %v", p.Open)
	}
	cursorToNoteViews(t, m, "#01 - Projects")
	pressViews(m, " ")
	if got := noteRowsViews(m.vis.notes); got != "#01 - Projects|Teams|Inbox" {
		t.Errorf("closing the top folder hides everything under it: %s", got)
	}
}

func TestNotesRecentLayoutAndFilter(t *testing.T) {
	m := notesModelViews(t)
	m.cur[vNotes].pos = 2
	pressViews(m, "v")
	if !m.notesPrefs.Recent || !loadNotesPrefs().Recent || m.cur[vNotes].pos != 0 {
		t.Fatal("v switches to the recent list, saves it and resets the cursor")
	}
	if got, want := noteRowsViews(m.vis.notes), "Plan|Teams|Deep|Inbox"; got != want {
		t.Errorf("recent: got %s want %s", got, want)
	}
	s := screenViews(m)
	if !strings.Contains(s, "your vault · recently edited") || !strings.Contains(s, "Plan  01 - Projects/ABC-2") {
		t.Errorf("recent screen:\n%s", s)
	}
	pressViews(m, "v")
	if m.notesPrefs.Recent {
		t.Error("v again goes back to folders")
	}

	m.filter.SetValue("abc-3")
	m.refilter()
	if got := noteRowsViews(m.vis.notes); got != "Teams" {
		t.Errorf("filter matches ticket keys too: %s", got)
	}
}

func TestNotesDetail(t *testing.T) {
	m := notesModelViews(t)
	m.notesPrefs.Recent = true
	m.refilter()
	cursorToNoteViews(t, m, "Plan")
	s := plainViews(m.detailNote())
	for _, want := range []string{"Plan  01 - Projects/ABC-2/Plan.md", "ABC-2 Card form · To Do", "Split the Card form in two.", "Ask Ann Smith|Ann about it."} {
		if !strings.Contains(s, want) {
			t.Errorf("detail lacks %q:\n%s", want, s)
		}
	}
	for _, unwanted := range []string{"tags:", "# Plan"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("detail shows %q:\n%s", unwanted, s)
		}
	}
	m.vis.notes = nil
	if strings.Contains(plainViews(m.detailNote()), "Plan") {
		t.Error("no row, empty panel")
	}
}

func TestNotesReader(t *testing.T) {
	m := notesModelViews(t)
	m.notesPrefs.Recent = true
	m.refilter()
	cursorToNoteViews(t, m, "Plan")
	pressViews(m, "enter")
	if m.reader == nil {
		t.Fatal("enter opens the reader")
	}
	s := screenViews(m)
	for _, want := range []string{"📝 Plan", "01 - Projects/ABC-2", "ABC-2", "Card form", "2026-10-01T09:00 · abc-2, payments", "open in Obsidian"} {
		if !strings.Contains(s, want) {
			t.Errorf("reader lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "[[") {
		t.Errorf("wikilinks should be rendered:\n%s", s)
	}
	pressViews(m, "G", "g", "j")
	if _, cmd := m.Update(keyViews("e")); cmd == nil {
		t.Error("e hands over to the editor")
	}

	m.vault.Refresh()
	m.Update(vaultMsg(m.vault.All()))
	if m.reader == nil || m.reader.note.Title != "Plan" {
		t.Error("a vault refresh keeps the reader on its note")
	}
	pressViews(m, "esc")
	if m.reader != nil {
		t.Error("esc closes the reader")
	}

	m.openReader(vault.Note{Title: "Gone", Path: filepath.Join(t.TempDir(), "gone.md")})
	if !strings.Contains(screenViews(m), "no such file") {
		t.Error("a missing file shows the error")
	}
}

func TestNotesKeysOnFolders(t *testing.T) {
	m := notesModelViews(t)
	for _, k := range []string{"e", "o", "y", "J"} {
		if _, cmd := m.Update(keyViews(k)); cmd != nil {
			t.Errorf("%s on a folder does nothing", k)
		}
	}
	m.vis.notes = nil
	if _, cmd := m.Update(keyViews("enter")); cmd != nil || m.reader != nil {
		t.Error("nothing selected, nothing opens")
	}
}

func TestNotesTicketJump(t *testing.T) {
	m := notesModelViews(t)
	m.notesPrefs.Recent = true
	m.refilter()
	cursorToNoteViews(t, m, "Teams")
	pressViews(m, "J")
	if m.view != vJira {
		t.Fatal("J goes to the note's ticket")
	}
	if is, _ := m.currentIssue(); is.Key != "ABC-3" {
		t.Errorf("on %s", is.Key)
	}
}

func TestTicketNotes(t *testing.T) {
	m := notesModelViews(t)
	if got := m.ticketKeys("ABC-1"); strings.Join(got, ",") != "ABC-1,ABC-2,ABC-3" {
		t.Errorf("an epic includes its stories: %v", got)
	}
	var titles []string
	for _, n := range m.notesFor("ABC-1") {
		titles = append(titles, n.Title)
	}
	if strings.Join(titles, ",") != "Plan,Deep,Teams" {
		t.Errorf("epic notes: %v", titles)
	}
	if m.notesFor("") != nil || m.ticketKeys("") != nil {
		t.Error("no key, no notes")
	}
	if got := plainViews(m.notesLine("ABC-2")); !strings.Contains(got, "2 note(s): Plan · Deep") {
		t.Errorf("notes line %q", got)
	}
	lines := m.withNotes([]string{"a", "b", "c"}, "ABC-2")
	if len(lines) != 4 || !strings.Contains(lines[2], "note(s)") || lines[3] != "c" {
		t.Errorf("notes go third: %q", lines)
	}
	if got := m.withNotes([]string{"a"}, "ABC-4"); len(got) != 1 {
		t.Errorf("no notes, no line: %q", got)
	}

	m.view = vJira
	m.refilter()
	cursorToViews(t, m, "ABC-2")
	if !strings.Contains(screenViews(m), "📝 2 note(s)") {
		t.Error("the jira detail lists the notes")
	}
	pressViews(m, "N")
	if m.chooser == nil || m.chooser.title != "notes for ABC-2" || len(m.chooser.all) != 2 {
		t.Fatalf("N lists the ticket's notes: %+v", m.chooser)
	}
	pressViews(m, "down", "enter")
	if m.reader == nil || m.reader.note.Title != "Deep" {
		t.Fatalf("picking a note opens it")
	}
	pressViews(m, "esc")

	cursorToViews(t, m, "ABC-4")
	pressViews(m, "N")
	if m.chooser != nil || m.flash != "no notes for ABC-4 yet — w writes one" {
		t.Errorf("flash %q", m.flash)
	}
	cursorToViews(t, m, "#Sprint 2")
	pressViews(m, "N")
	if m.flash != "no ticket here" || !m.flashErr {
		t.Errorf("flash %q", m.flash)
	}

	m.vault = nil
	pressViews(m, "N")
	if !strings.Contains(m.flash, `set "vault"`) {
		t.Errorf("flash %q", m.flash)
	}
}

func TestWriteNote(t *testing.T) {
	m := notesModelViews(t)
	m.view = vJira
	m.refilter()
	cursorToViews(t, m, "#Sprint 2")
	pressViews(m, "w")
	if m.prompt != nil || m.flash != "no ticket here to write about" {
		t.Fatalf("flash %q", m.flash)
	}
	cursorToViews(t, m, "ABC-3")
	pressViews(m, "w")
	if m.prompt == nil || m.prompt.title != "new note in 01 - Projects/ABC-3" || m.prompt.in.Value() != "Refund flow" {
		t.Fatalf("w asks for a title prefilled with the summary: %+v", m.prompt)
	}
	if !strings.Contains(screenViews(m), "new note in 01 - Projects/ABC-3") {
		t.Error("the prompt shows")
	}
	pressViews(m, ":", " ", "v", "2")
	if _, cmd := m.Update(keyViews("enter")); cmd == nil {
		t.Error("the new note opens in the editor")
	}
	p := filepath.Join(m.vault.Root, "01 - Projects", "ABC-3", "Refund flow - v2.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "tags: [abc-3]") || !strings.Contains(string(b), "[[ABC-3]]") {
		t.Errorf("note body:\n%s", b)
	}

	m.vault = nil
	pressViews(m, "w")
	if !strings.Contains(m.flash, `set "vault"`) {
		t.Errorf("flash %q", m.flash)
	}
}

func TestNotesEmptyStatesAndRefresh(t *testing.T) {
	m := newModelViews(t)
	m.view = vNotes
	m.refilter()
	if !strings.Contains(screenViews(m), `set "vault" in`) {
		t.Error("no vault configured")
	}
	if m.refreshVault() != nil {
		t.Error("no vault, nothing to refresh")
	}

	m.vault = vault.New(vaultViews(t), "01 - Projects", claude.TicketRegexp())
	if !strings.Contains(screenViews(m), "reading the vault") {
		t.Error("before the first scan")
	}
	cmd := m.refreshVault()
	if cmd == nil || m.refreshVault() != nil {
		t.Fatal("one scan at a time")
	}
	msg, ok := cmd().(vaultMsg)
	if !ok || len(msg) != 4 {
		t.Fatalf("scan found %v", msg)
	}
	m.Update(msg)
	if m.vaultLoading || len(m.vis.notes) != 3 {
		t.Errorf("loading=%v rows=%d", m.vaultLoading, len(m.vis.notes))
	}

	if _, cmd := m.Update(editedMsg{err: errors.New("nvim: exit 1")}); cmd == nil || m.flash != "nvim: exit 1" {
		t.Errorf("an editor error flashes and the vault is rescanned, flash %q", m.flash)
	}
}

func TestCurrentTicketPerView(t *testing.T) {
	m := jiraModelViews(t)
	m.jira.Mentions = append(m.jira.Mentions, jiraMentionViews("c1", "ABC-4", false))
	m.conf.Mentions = append(m.conf.Mentions, confMentionViews())
	m.reindex()
	cursorToViews(t, m, "ABC-2")
	if got := m.currentTicket(); got != "ABC-2" {
		t.Errorf("jira: %q", got)
	}
	m.view = vMentions
	m.refilter()
	if got := m.currentTicket(); got != "ABC-4" {
		t.Errorf("jira mention: %q", got)
	}
	m.cur[vMentions].pos = 1
	if got := m.currentTicket(); got != "" {
		t.Errorf("confluence mention has no ticket: %q", got)
	}
	m.view = vUsage
	if m.currentTicket() != "" {
		t.Error("usage has no ticket")
	}
}

func TestObsidianToMarkdown(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"frontmatter", "---\ndate: 2026-01-02\ntags: [a, b]\n---\nbody", "_2026-01-02 · a, b_\n\nbody"},
		{"frontmatter without meta", "---\ntitle: x\n---\nbody", "body"},
		{"unterminated frontmatter", "---\ndate: x\nbody", "---\ndate: x\nbody"},
		{"embed", "see ![[shot.png]]", "see 🖼 `shot.png`"},
		{"alias", "ask [[Ann Smith|Ann]]", "ask **Ann**"},
		{"wikilink", "on [[ABC-1]]", "on **ABC-1**"},
	}
	for _, c := range cases {
		if got := obsidianToMarkdown(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestObsidianURL(t *testing.T) {
	got := obsidianURL("/home/me/My Notes", "01 - Projects/ABC-2/Plan.md")
	want := "obsidian://open?vault=My+Notes&file=01+-+Projects%2FABC-2%2FPlan"
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}
