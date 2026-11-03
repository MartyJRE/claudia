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

func TestReviewPillMatchesTheInbox(t *testing.T) {
	m := newModelShell(t)
	before := m.pendingReviews()
	if before == 0 {
		t.Fatal("fixture: expected a pending review")
	}
	m.gl.Review[0].MyReview = "requested_changes"
	if got := m.pendingReviews(); got != before-1 {
		t.Errorf("a review I've done still counts: %d, want %d", got, before-1)
	}
	m.gl.Review[0].MyReview = ""
	for _, it := range m.inbox() {
		if it.kind == kReview {
			m.dismissed.toggle(it.id)
		}
	}
	if got := m.pendingReviews(); got != 0 {
		t.Errorf("snoozed reviews still count: %d", got)
	}
	if strings.Contains(plainShell(m.viewHeader()), "to review") {
		t.Error("no pill when nothing's left to review")
	}
}
