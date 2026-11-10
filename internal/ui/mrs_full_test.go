package ui

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/gitlab"
	"github.com/MartyJRE/claudia/internal/repos"
)

// fakeMRCalls answers approve, unapprove and rebase with err and records them.
func fakeMRCalls(t *testing.T, err error) *[]string {
	var got []string
	call := func(verb string) func(gitlab.MR) error {
		return func(mr gitlab.MR) error {
			got = append(got, verb+" "+mr.WebURL)
			return err
		}
	}
	fake(t, &approveMR, call("approve"))
	fake(t, &unapproveMR, call("unapprove"))
	fake(t, &rebaseMR, call("rebase"))
	return &got
}

func TestReviewActionsFull(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vReview)
	u := m.vis.mrs[0].WebURL
	calls := fakeMRCalls(t, nil)
	for _, k := range []string{"A", "U", "B"} {
		pressShell(m, k)
		if msg := pressShell(m, k)(); msg != refreshMsg(srcGitlab) {
			t.Errorf("%s twice acts and refreshes GitLab: %#v", k, msg)
		}
	}
	if !slices.Equal(*calls, []string{"approve " + u, "unapprove " + u, "rebase " + u}) {
		t.Errorf("calls: %v", *calls)
	}

	fakeMRCalls(t, errors.New("403 Forbidden"))
	for _, k := range []string{"A", "B"} {
		pressShell(m, k)
		if msg := pressShell(m, k)(); msg != (flashMsg{"403 Forbidden", true}) {
			t.Errorf("%s failing is flashed: %#v", k, msg)
		}
	}

	m.gl.Review = nil
	m.reindex()
	if _, cmd := m.keyMRs("enter"); cmd != nil || m.chooser != nil {
		t.Error("enter on an empty list does nothing")
	}
}

func TestThreadsOpenFull(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vMine)
	ran := fakeCombined(t, "", nil)
	pressShell(m, "t")
	pressShell(m, "enter")()
	if len(*ran) != 1 || (*ran)[0] != "open thread-1" {
		t.Errorf("picking a thread opens it: %v", *ran)
	}
}

func TestInboxApproveAndBriefFull(t *testing.T) {
	m := newModelShell(t)
	m.switchView(vInbox)
	for i, it := range m.vis.inbox {
		if it.kind == kReview {
			m.cur[vInbox].pos = i
		}
	}
	calls := fakeMRCalls(t, nil)
	pressShell(m, "A")
	if msg := pressShell(m, "A")(); msg != refreshMsg(srcGitlab) || len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "approve ") {
		t.Errorf("approving from the inbox: %#v %v", msg, *calls)
	}
	fakeMRCalls(t, errors.New("nope"))
	pressShell(m, "A")
	if msg := pressShell(m, "A")(); msg != (flashMsg{"nope", true}) {
		t.Errorf("a failed approve is flashed: %#v", msg)
	}

	breakState(t)
	pressShell(m, "R")
	if !m.flashErr || !m.briefPending.IsZero() {
		t.Errorf("a brief request that can't be written is flashed: %q", m.flash)
	}
}

func TestOpenLinkFetchAndLookFull(t *testing.T) {
	m := newModelShell(t)
	fake(t, &fetchMR, func(project string, iid int) (gitlab.MR, error) {
		return gitlab.MR{IID: iid, WebURL: project}, errors.New("404")
	})
	_, cmd := m.openLink("https://gitlab.example.com/acme/pay/-/merge_requests/5")
	if msg, ok := cmd().(mrLoadedMsg); !ok || msg.mr.IID != 5 || msg.mr.WebURL != "acme/pay" || msg.err == nil {
		t.Errorf("an unknown MR is fetched: %#v", msg)
	}

	m.repos = []repos.Repo{{Dir: "/code/pay"}}
	started := fakeStart(t, "pay:1", nil)
	m.openLink("https://example.org/some/page")
	pressShell(m, "enter")
	if len(*started) != 1 || (*started)[0].Name != "look: example.org/some/page" || promptOf((*started)[0]) != "Take a look at https://example.org/some/page" {
		t.Errorf("anything else is looked at in a picked repo: %+v", *started)
	}
}

func TestAskLinkClipboardFull(t *testing.T) {
	m := newModelShell(t)
	clip := "  https://example.org/x  \n"
	var asked []string
	fake(t, &cmdOutput, func(c *exec.Cmd) ([]byte, error) {
		asked = append(asked, cmdLine(c))
		return []byte(clip), nil
	})
	m.askLink()
	if m.prompt.in.Value() != "https://example.org/x" || !slices.Equal(asked, []string{"pbpaste"}) {
		t.Errorf("a link on the clipboard is filled in: %q", m.prompt.in.Value())
	}
	m.repos = []repos.Repo{{Dir: "/code/pay"}}
	pressShell(m, "enter")
	if m.chooser == nil || m.chooser.title != "take a look in" {
		t.Error("enter opens the link")
	}

	clip = "not a link"
	m.chooser = nil
	m.askLink()
	if m.prompt.in.Value() != "" {
		t.Errorf("other clipboard text is ignored: %q", m.prompt.in.Value())
	}
}
