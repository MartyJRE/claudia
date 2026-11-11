package ui

import (
	"strings"
	"testing"
	"time"
)

func colKeys(m *Model) []string {
	var out []string
	for _, c := range m.vis.board[m.boardCol] {
		out = append(out, c.id())
	}
	return out
}

func TestAgreedOrderShell(t *testing.T) {
	m := boardModelShell(t)
	db := withRunsShell(t, m)
	m.refilter()
	pressShell(m, "l", "j")
	if c, _ := m.currentCard(); c.issue.Key != "ABC-2" {
		t.Fatalf("on %s", c.issue.Key)
	}
	pressShell(m, "*")
	if !strings.Contains(m.flash, "ABC-2 joins the agreed order; K/J move it") || colKeys(m)[0] != "ABC-2" {
		t.Errorf("pinned: %q %v", m.flash, colKeys(m))
	}
	containsAllShell(t, "chip", wideCard(t, m, "ABC-2"), "① ABC-2")
	if strings.Contains(wideCard(t, m, "ABC-1"), "①") {
		t.Error("an unpinned card has no place")
	}
	// The cursor follows ABC-2 to the top; ABC-1 below isn't agreed.
	if c, _ := m.currentCard(); c.issue.Key != "ABC-2" {
		t.Errorf("the cursor stays on the card: %s", c.issue.Key)
	}
	pressShell(m, "j", "J")
	if !strings.Contains(m.flash, "ABC-1 isn't in the agreed order: * pins it") {
		t.Errorf("move unpinned: %q", m.flash)
	}
	pressShell(m, "*", "K")
	if got := colKeys(m); got[0] != "ABC-1" || got[1] != "ABC-2" {
		t.Errorf("K moves it up: %v", got)
	}
	if c, _ := m.currentCard(); c.issue.Key != "ABC-1" {
		t.Errorf("the cursor moved with ABC-1: %s", c.issue.Key)
	}
	containsAllShell(t, "detail", plainShell(m.detailBoard()), "① agreed today")
	m.pinCard(cardShell(t, m, "ABC-1"))
	if _, ok := m.agreed["ABC-1"]; ok || !strings.Contains(m.flash, "ABC-1 left the agreed order") {
		t.Errorf("unpinned: %q", m.flash)
	}

	_ = db.SetAgreed([]string{"ABC-2"}, "Zdeněk, grooming", 0, time.Now().Add(-48*time.Hour))
	m.loadAgreed()
	m.refilter()
	m.cur[vBoard].pos = 0
	if !strings.Contains(plainShell(m.detailBoard()), "① agreed "+time.Now().Add(-48*time.Hour).Format("Mon")+", Zdeněk, grooming") {
		t.Errorf("note: %s", plainShell(m.detailBoard()))
	}

	review := mrShell("payments", 11, "x", "alice", "feat/x", time.Hour)
	rc := boardCard{mr: &review}
	m.pinCard(rc)
	if !strings.Contains(plainShell(strings.Join(m.reviewCardLines(rc, 60, false), "\n")), "② !11") ||
		!strings.Contains(plainShell(m.detailReview(rc)), "② agreed today") {
		t.Errorf("review card: %s", plainShell(strings.Join(m.reviewCardLines(rc, 60, false), "\n")))
	}

	db.Close()
	m.pinCard(cardShell(t, m, "ABC-1"))
	if !strings.Contains(m.flash, "closed") {
		t.Errorf("pin on a closed db: %q", m.flash)
	}
	m.db = nil
	m.pinCard(rc)
	if !strings.Contains(m.flash, "run log isn't open") {
		t.Errorf("no db: %q", m.flash)
	}
	m.loadAgreed()
	if len(m.agreed) != 0 {
		t.Error("no db, no order")
	}
}

func TestAgreedWhen(t *testing.T) {
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.Local)
	for at, want := range map[time.Time]string{
		now.Add(-time.Hour):          "today",
		now.Add(-24 * time.Hour):     "yesterday",
		now.Add(-3 * 24 * time.Hour): "Sun",
		now.Add(-9 * 24 * time.Hour): "2 Nov",
	} {
		if got := agreedWhen(at, now); got != want {
			t.Errorf("%v: %q, want %q", at, got, want)
		}
	}
	if circled(1) != "①" || circled(20) != "⑳" || circled(21) != "#21" {
		t.Error("circled")
	}
}
