package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/MartyJRE/claudia/internal/state"
)

func TestNotLiveShell(t *testing.T) {
	rows := sessionsShell()
	if notLive(nil, rows) != nil {
		t.Errorf("nothing pending stays nil")
	}
	items := []restoreItem{{ID: "aaaaaaaa-wait"}, {ID: "dddddddd-closed"}, {ID: "zzzz"}}
	got := notLive(items, rows)
	if len(got) != 2 || got[0].ID != "dddddddd-closed" || got[1].ID != "zzzz" {
		t.Errorf("running ones drop out: %+v", got)
	}
	if got := notLive([]restoreItem{{ID: "aaaaaaaa-wait"}}, rows); got != nil {
		t.Errorf("nil once everything is back: %+v", got)
	}
}

func TestSaveLiveShell(t *testing.T) {
	isolateShell(t)
	rows := sessionsShell()
	saveLive(rows, true)
	if _, err := os.Stat(state.Path(liveFile)); err == nil {
		t.Fatal("a pending restore offer must not be overwritten")
	}
	saveLive(rows, false)
	var snap liveSnapshot
	if !state.Load(liveFile, &snap) {
		t.Fatal("snapshot not written")
	}
	want := []restoreItem{
		{ID: "aaaaaaaa-wait", Profile: "work", Dir: "/code/payments", Name: "export csv", InTmux: true},
		{ID: "bbbbbbbb-busy", Profile: "personal", Dir: "/code/billing", Name: "invoice pdf"},
	}
	if len(snap.Sessions) != len(want) {
		t.Fatalf("only live CLI sessions with a dir: %+v", snap.Sessions)
	}
	for i := range want {
		if snap.Sessions[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, snap.Sessions[i], want[i])
		}
	}

	os.Remove(state.Path(liveFile))
	saveLive(rows, false)
	if _, err := os.Stat(state.Path(liveFile)); err == nil {
		t.Errorf("an unchanged set isn't rewritten")
	}
	saveLive(rows[1:], false)
	if !state.Load(liveFile, &snap) || len(snap.Sessions) != 1 {
		t.Errorf("a changed set is written: %+v", snap.Sessions)
	}
}

func TestPendingRestoreShell(t *testing.T) {
	isolateShell(t)
	if pendingRestore() != nil {
		t.Errorf("no snapshot, nothing to restore")
	}
	sessions := []restoreItem{{ID: "a", InTmux: true}, {ID: "b"}}
	for _, c := range []struct {
		name string
		snap liveSnapshot
		want string
	}{
		{"reboot", liveSnapshot{Boot: "another boot", Sessions: sessions}, "a b"},
		{"tmux restart", liveSnapshot{Boot: bootID(), Tmux: "4242", Sessions: sessions}, "a"},
		{"nothing restarted", liveSnapshot{Boot: bootID(), Tmux: tmuxServer(), Sessions: sessions}, ""},
		{"empty", liveSnapshot{Boot: "another boot"}, ""},
	} {
		if err := state.Save(liveFile, c.snap); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, it := range pendingRestore() {
			ids = append(ids, it.ID)
		}
		if got := strings.Join(ids, " "); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRestoreBannerShell(t *testing.T) {
	m := sessionsModelShell(t)
	m.restore = []restoreItem{{ID: "aaaaaaaa-wait"}, {ID: "x"}}
	s := plainShell(m.listSessions(20))
	containsAllShell(t, "banner", s, "2 sessions were running before the restart", "X restore all", "ctrl+x forget")

	pressShell(m, "ctrl+x")
	if m.restore != nil {
		t.Fatalf("ctrl+x forgets the offer")
	}
	if strings.Contains(plainShell(m.listSessions(20)), "before the restart") {
		t.Errorf("banner gone")
	}
	var snap liveSnapshot
	if !state.Load(liveFile, &snap) || len(snap.Sessions) != 2 {
		t.Errorf("forgetting records what runs now: %+v", snap.Sessions)
	}
}

func TestRestoreAllSkipsLiveShell(t *testing.T) {
	m := sessionsModelShell(t)
	if _, cmd := m.restoreAll(); cmd != nil || m.flash != "" {
		t.Errorf("nothing to restore is a no-op")
	}
	m.restore = []restoreItem{{ID: "aaaaaaaa-wait"}, {ID: "bbbbbbbb-busy"}}
	pressShell(m, "X")
	if m.restore != nil || m.flash != "restored 0 sessions" {
		t.Errorf("sessions already running are skipped: %q", m.flash)
	}
	if m.Result.Attach != "" {
		t.Errorf("nothing started, nothing to attach")
	}
}
