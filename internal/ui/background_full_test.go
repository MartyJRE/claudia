package ui

import (
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/launch"
)

func TestBackgroundStartStaysInClaudia(t *testing.T) {
	m := newModelShell(t)
	t.Setenv("TMUX", "/tmp/sock,1,0")
	started := fakeStart(t, "pay:3", nil)
	switched := fakeSwitch(t, nil)

	m.background = true
	_, cmd := m.start(launch.Spec{ID: "new", Name: "review: pay"})
	if len(*started) != 1 || len(*switched) != 0 || m.Result.Attach != "" {
		t.Fatalf("started %v, switched %v, attach %q", *started, *switched, m.Result.Attach)
	}
	if !strings.Contains(m.flash, "started review: pay in the background") || cmd == nil || m.background {
		t.Errorf("flash %q, reload %v, flag left %v", m.flash, cmd != nil, m.background)
	}

	m.Update(startMsg{sp: launch.Spec{Name: "later"}, background: true})
	if len(*switched) != 0 || !strings.Contains(m.flash, "later in the background") {
		t.Errorf("a background start that arrives later must not switch either: %v %q", *switched, m.flash)
	}
	m.Update(startMsg{sp: launch.Spec{Name: "now"}})
	if len(*switched) != 1 {
		t.Errorf("a normal start switches to it: %v", *switched)
	}
}

func TestCIsTheBackgroundVariant(t *testing.T) {
	m := newModelShell(t)
	pressShell(m, "C")
	if !m.background || m.chooser == nil {
		t.Errorf("C in the inbox should start what c does, in the background: bg %v chooser %v", m.background, m.chooser != nil)
	}
	m.chooser = nil
	pressShell(m, "j")
	if m.background {
		t.Error("any other key clears the background flag")
	}

	m.switchView(vReview)
	pressShell(m, "C")
	if !m.background || m.chooser == nil || !strings.HasPrefix(m.chooser.title, "review ") {
		t.Errorf("C in Review reviews in the background: %+v", m.chooser)
	}
	m.chooser = nil
	m.switchView(vMine)
	pressShell(m, "C")
	if !m.background || m.chooser == nil || !strings.HasPrefix(m.chooser.title, "fix ") {
		t.Errorf("C in My MRs fixes threads in the background: %+v", m.chooser)
	}
	m.chooser = nil
	m.gl.Mine, m.gl.Merged = nil, nil
	m.refilter()
	if pressShell(m, "C"); m.chooser != nil {
		t.Error("nothing selected, nothing started")
	}

	m.switchView(vJira)
	pressShell(m, "C")
	if !m.background {
		t.Error("C in Jira")
	}
	m.chooser = nil
	m.switchView(vMentions)
	pressShell(m, "C")
	if !m.background {
		t.Error("C in Mentions")
	}
}
