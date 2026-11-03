package ui

import (
	"strings"
	"testing"
)

func TestReviewedMRsLeaveTheInbox(t *testing.T) {
	m := newModelShell(t)
	mr := m.gl.Review[0]
	has := func() bool {
		for _, it := range m.inbox() {
			if it.kind == kReview && it.url == mr.WebURL {
				return true
			}
		}
		return false
	}
	if !has() {
		t.Fatal("fixture: the review should start in the inbox")
	}
	for state, label := range map[string]string{"requested_changes": "changes requested", "reviewed": "reviewed"} {
		m.gl.Review[0].MyReview = state
		if has() {
			t.Errorf("%s: a review I've done waits on the author, not me", state)
		}
		if got := plainShell(m.approvalText(m.gl.Review[0])); got != label {
			t.Errorf("%s: Review tab says %q", state, got)
		}
		m.refilter()
		m.switchView(vReview)
		if s := plainShell(m.View()); !strings.Contains(s, mr.Title) {
			t.Errorf("%s: still listed in the Review tab", state)
		}
		m.switchView(vInbox)
	}
	m.gl.Review[0].MyReview = "unreviewed"
	if !has() {
		t.Error("back in the inbox once the author asks for another review")
	}
}
