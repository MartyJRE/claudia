package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/vault"
)

// fakeEditor records what would be handed the terminal and returns its callback.
func fakeEditor(t *testing.T) (*[]string, *tea.ExecCallback) {
	var got []string
	var done tea.ExecCallback
	fake(t, &execProcess, func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		got = append(got, cmdLine(c))
		done = fn
		return func() tea.Msg { return nil }
	})
	return &got, &done
}

func TestEditorFull(t *testing.T) {
	m := notesModelViews(t)
	edited, done := fakeEditor(t)
	cursorToNoteViews(t, m, "Inbox")
	path := m.vis.notes[m.cur[vNotes].pos].note.Path

	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	pressViews(m, "e")
	t.Setenv("EDITOR", "code -w")
	m.openReader(m.vis.notes[m.cur[vNotes].pos].note)
	pressViews(m, "e")
	t.Setenv("VISUAL", "hx")
	m.openReader(m.vis.notes[m.cur[vNotes].pos].note)
	pressViews(m, "e")
	sh := func(ed string) string { return `sh -c ` + ed + ` "$1" sh ` + path }
	if len(*edited) != 3 || (*edited)[0] != sh("vi") || (*edited)[1] != sh("code -w") || (*edited)[2] != sh("hx") {
		t.Errorf("$VISUAL, else $EDITOR, else vi:\n%s", strings.Join(*edited, "\n"))
	}
	if msg, ok := (*done)(errors.New("exit 1")).(editedMsg); !ok || msg.path != path || msg.err == nil {
		t.Errorf("the editor's end comes back as editedMsg: %#v", msg)
	}
}

func TestNoteLinksFull(t *testing.T) {
	m := notesModelViews(t)
	ran := fakeCombined(t, "", nil)
	copied := fakeClipboard(t, nil)
	cursorToNoteViews(t, m, "Inbox")
	note := m.vis.notes[m.cur[vNotes].pos].note
	url := obsidianURL(m.vault.Root, note.Rel)

	pressViews(m, "o")()
	pressViews(m, "y")()
	m.openReader(note)
	pressViews(m, "o")()
	pressViews(m, "y")()
	if len(*ran) != 2 || (*ran)[0] != "open "+url || (*ran)[1] != "open "+url {
		t.Errorf("o opens Obsidian from the list and the reader: %v", *ran)
	}
	if len(*copied) != 2 || (*copied)[0] != "pbcopy < "+note.Path || (*copied)[1] != (*copied)[0] {
		t.Errorf("y copies the path: %v", *copied)
	}
}

func TestReaderDetailsFull(t *testing.T) {
	m := notesModelViews(t)
	body := "# T\n\n" + strings.Repeat("line\n", 10)
	p := filepath.Join(t.TempDir(), "Many.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	n := vault.Note{Path: p, Rel: "Many.md", Title: "Many", Keys: []string{"ABC-1", "ABC-2", "ABC-3", "ABC-4", "ABC-10"}, Modified: time.Now()}
	m.openReader(n)
	if s := screenViews(m); !strings.Contains(s, "ABC-1 ABC-2 ABC-3 +2") {
		t.Errorf("more than three keys are counted:\n%s", s)
	}

	m.reader = nil
	m.vaultNotes = append(m.vaultNotes, n)
	m.notesPrefs.Recent = true
	m.refilter()
	cursorToNoteViews(t, m, "Many")
	if d := plainViews(m.detailNote()); strings.Count(d, "line") != 3 {
		t.Errorf("the preview stops at five lines:\n%s", d)
	}

	old := onedarkStyle
	onedarkStyle = []byte("{not json")
	t.Cleanup(func() { onedarkStyle = old })
	m.width = 77
	m.openReader(n)
	if s := m.reader.vp.View(); !strings.Contains(s, "# T") {
		t.Errorf("without a renderer the raw text shows:\n%s", s)
	}
}

func TestRecentNotesCapFull(t *testing.T) {
	m := notesModelViews(t)
	m.vaultNotes = nil
	for i := range 250 {
		m.vaultNotes = append(m.vaultNotes, vault.Note{Rel: fmt.Sprintf("n%d.md", i), Title: fmt.Sprint(i)})
	}
	m.notesPrefs.Recent = true
	m.refilter()
	if len(m.vis.notes) != 200 {
		t.Errorf("recent notes are capped at 200: %d", len(m.vis.notes))
	}
	m.filter.SetValue("1")
	m.refilter()
	if len(m.vis.notes) <= 100 {
		t.Errorf("a filter searches all of them: %d", len(m.vis.notes))
	}
}

func TestOpenFolderGlyphFull(t *testing.T) {
	m := notesModelViews(t)
	cursorToNoteViews(t, m, "#01 - Projects")
	pressViews(m, " ")
	if s := screenViews(m); !strings.Contains(s, "▾ 01 - Projects") {
		t.Errorf("an open folder shows ▾:\n%s", s)
	}
}

func TestWriteNoteFailsFull(t *testing.T) {
	m := notesModelViews(t)
	m.view = vJira
	m.refilter()
	if err := os.WriteFile(filepath.Join(m.vault.Root, "01 - Projects", "ABC-3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cursorToViews(t, m, "ABC-3")
	pressViews(m, "w")
	if _, cmd := m.Update(keyViews("enter")); cmd != nil || !m.flashErr {
		t.Errorf("a note that can't be created is flashed: %q", m.flash)
	}
}

func TestCurrentTicketListsFull(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)
	for i, it := range m.vis.inbox {
		if it.ticket != "" {
			m.cur[vInbox].pos = i
			break
		}
	}
	if got := m.currentTicket(); got == "" || got != m.vis.inbox[m.cur[vInbox].pos].ticket {
		t.Errorf("inbox: %q", got)
	}
	m.switchView(vSessions)
	cursorOnShell(t, m, "aaaaaaaa-wait")
	if got := m.currentTicket(); got != "ABC-1" {
		t.Errorf("sessions: %q", got)
	}
	m.switchView(vMine)
	if got := m.currentTicket(); got != "ABC-2" {
		t.Errorf("my MRs: %q", got)
	}
}
