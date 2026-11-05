package ui

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/launch"
	"github.com/MartyJRE/claudia/internal/repos"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
	"github.com/MartyJRE/claudia/internal/vault"
)

// drainFull runs a command and every command batched inside it.
func drainFull(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, drainFull(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func countFull[T any](msgs []tea.Msg) int {
	n := 0
	for _, m := range msgs {
		if _, ok := m.(T); ok {
			n++
		}
	}
	return n
}

// instantTicks fires timers at once and records what they were set to.
func instantTicks(t *testing.T) *[]time.Duration {
	var got []time.Duration
	fake(t, &tickAfter, func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		got = append(got, d)
		return func() tea.Msg { return fn(time.Unix(0, 0)) }
	})
	return &got
}

// fakeStart makes launch.Start answer with target (or err) and records the specs.
func fakeStart(t *testing.T, target string, err error) *[]launch.Spec {
	var got []launch.Spec
	fake(t, &startSession, func(sp launch.Spec) (string, error) {
		got = append(got, sp)
		return target, err
	})
	return &got
}

// fakeSwitch records tmux switch-client targets.
func fakeSwitch(t *testing.T, err error) *[]string {
	var got []string
	fake(t, &switchClient, func(target string) error {
		got = append(got, target)
		return err
	})
	return &got
}

// fakeCombined records commands run through run() and answers with out/err.
func fakeCombined(t *testing.T, out string, err error) *[]string {
	var got []string
	fake(t, &cmdCombinedOutput, func(c *exec.Cmd) ([]byte, error) {
		got = append(got, cmdLine(c))
		return []byte(out), err
	})
	return &got
}

// fakeClipboard records what copyText pipes into pbcopy.
func fakeClipboard(t *testing.T, err error) *[]string {
	var got []string
	fake(t, &cmdRun, func(c *exec.Cmd) error {
		b, _ := io.ReadAll(c.Stdin)
		got = append(got, cmdLine(c)+" < "+string(b))
		return err
	})
	return &got
}

func withConfig(t *testing.T, edit func(*config.Config)) {
	c := config.Get()
	edit(&c)
	fake(t, &getConfig, func() config.Config { return c })
}

func TestNewFull(t *testing.T) {
	isolateShell(t)
	t.Setenv("HOME", t.TempDir())
	m := New(Options{Popup: true})
	if !m.opts.Popup || m.view != vSessions || m.filter.Prompt != "/ " || m.vault != nil {
		t.Errorf("fresh model: popup %v view %v prompt %q vault %v", m.opts.Popup, m.view, m.filter.Prompt, m.vault)
	}
	if len(m.store.Profiles) != 0 || m.usageScanner == nil || m.usageScanner.Days != 31 {
		t.Errorf("no ~/.claude* profiles in an empty home, usage over 31 days: %+v", m.usageScanner)
	}
	if !m.flashErr || !strings.Contains(m.flash, "claudia setup") || time.Until(m.flashAt) < 10*time.Second {
		t.Errorf("without the notifier helper the hint stays up a while: %q", m.flash)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	helper := filepath.Join(home, "Applications", "Claudia.app", "Contents", "MacOS", "claudia-notify")
	if err := os.MkdirAll(filepath.Dir(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	m = New(Options{})
	if m.flash != "" {
		t.Errorf("with the helper installed and clean config, no flash: %q", m.flash)
	}

	withConfig(t, func(c *config.Config) { c.Keys = map[string]config.KeyList{"nope.action": {"z"}} })
	m = New(Options{})
	if !m.flashErr || m.flash != "config keys: unknown key action nope.action" {
		t.Errorf("config problems are flashed: %q", m.flash)
	}
}

func TestInitFull(t *testing.T) {
	m := newModelShell(t)
	ticks := instantTicks(t)
	fake(t, &findRepos, func(root string) []repos.Repo { return []repos.Repo{{Dir: "/code/x"}} })
	fake(t, &scanUsage, func(*claude.UsageScanner) []claude.DayModel { return []claude.DayModel{{Day: "2026-11-01"}} })
	m.sessLoading = false
	msgs := drainFull(m.Init())
	if !m.sessLoading || !m.usageLoading || !m.busy[srcRepos] || !m.busy[srcConfluence] || m.busy[srcGitlab] {
		t.Errorf("init marks loads in flight: sess %v usage %v busy %v", m.sessLoading, m.usageLoading, m.busy)
	}
	if countFull[sessionsMsg](msgs) != 1 || countFull[usageMsg](msgs) != 1 || countFull[fetched](msgs) != 2 ||
		countFull[tickMsg](msgs) != 1 || countFull[frameMsg](msgs) != 1 {
		t.Errorf("init loads sessions, usage, the stale sources and starts both timers: %#v", msgs)
	}
	if want := []time.Duration{1500 * time.Millisecond, 120 * time.Millisecond}; len(*ticks) != 2 || (*ticks)[0] != want[0] || (*ticks)[1] != want[1] {
		t.Errorf("timers: %v", *ticks)
	}
}

func TestTickFull(t *testing.T) {
	m := newModelShell(t)
	instantTicks(t)
	fake(t, &updateAvailable, func() bool { return true })
	fake(t, &findRepos, func(string) []repos.Repo { return nil })
	m.fetchedAt[srcConfluence] = time.Now()
	m.repos = []repos.Repo{{Dir: "/code/x"}}
	m.usageAt = time.Now()
	m.sessLoading = false
	m.vault = vault.New(t.TempDir(), "", claude.TicketRegexp())
	m.flash, m.flashAt = "old news", time.Now().Add(-time.Minute)
	m.dismissed = dismissed{}
	if err := state.Save(dismissedFile, dismissed{"mr:x": time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := RequestOpen("https://example.org/page"); err != nil {
		t.Fatal(err)
	}

	_, cmd := m.Update(tickMsg{})
	msgs := drainFull(cmd)
	if !m.sessLoading || !m.vaultLoading || !m.updateAvail || m.flash != "" || m.updateAt.IsZero() {
		t.Errorf("tick reloads sessions, vault, update state and clears old flashes: sess %v vault %v upd %v flash %q",
			m.sessLoading, m.vaultLoading, m.updateAvail, m.flash)
	}
	if _, ok := m.dismissed["mr:x"]; !ok {
		t.Error("tick picks up snoozes from claudia ctl")
	}
	if m.chooser == nil || m.chooser.title != "take a look in" {
		t.Error("tick picks up claudia open requests")
	}
	if countFull[sessionsMsg](msgs) != 1 || countFull[vaultMsg](msgs) != 1 || countFull[tickMsg](msgs) != 1 || countFull[fetched](msgs) != 1 {
		t.Errorf("tick commands: %#v", msgs)
	}

	fake(t, &updateAvailable, func() bool { t.Error("update state is checked every 30s only"); return false })
	m.chooser = nil
	m.setFlash("fresh", false)
	_, cmd = m.Update(tickMsg{})
	msgs = drainFull(cmd)
	if m.flash != "fresh" || m.chooser != nil {
		t.Errorf("a fresh flash stays, no open request twice: %q", m.flash)
	}
	if countFull[sessionsMsg](msgs) != 0 || countFull[vaultMsg](msgs) != 0 || countFull[tickMsg](msgs) != 1 {
		t.Errorf("loads in flight are not repeated: %#v", msgs)
	}
}

func TestFrameAndOtherMsgsFull(t *testing.T) {
	m := newModelShell(t)
	instantTicks(t)
	_, cmd := m.Update(frameMsg{})
	if m.frame != 1 || countFull[frameMsg](drainFull(cmd)) != 1 {
		t.Errorf("frame advances the spinner and re-arms: %d", m.frame)
	}
	if _, cmd := m.Update(struct{}{}); cmd != nil {
		t.Error("unknown messages are ignored")
	}

	note := filepath.Join(t.TempDir(), "n.md")
	if err := os.WriteFile(note, []byte("# hi\n\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.openReader(vault.Note{Path: note, Title: "n"})
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	if m.reader.width != 90 || m.reader.vp.Height != 26 || m.filter.Width != 45 {
		t.Errorf("resizing re-renders the reader: width %d height %d", m.reader.width, m.reader.vp.Height)
	}
}

func TestStartAndGoToFull(t *testing.T) {
	m := newModelShell(t)
	started := fakeStart(t, "", errors.New("no tmux"))
	_, cmd := m.Update(startMsg{sp: launch.Spec{Dir: "/code/x", Name: "x"}})
	if cmd != nil || !m.flashErr || m.flash != "no tmux" || len(*started) != 1 || (*started)[0].Dir != "/code/x" {
		t.Errorf("a failed start is flashed: %q %v", m.flash, *started)
	}

	fakeStart(t, "x:2", nil)
	_, cmd = m.start(launch.Spec{Dir: "/code/x"})
	if !isQuitShell(cmd) || m.Result.Attach != "x:2" {
		t.Errorf("outside tmux main attaches to the new window: %q", m.Result.Attach)
	}

	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	switched := fakeSwitch(t, errors.New("no client"))
	m.Result.Attach = ""
	if _, cmd := m.goTo("x:2"); cmd != nil || m.flash != "no client" || m.Result.Attach != "" {
		t.Errorf("a failed switch is flashed: %q", m.flash)
	}
	fakeSwitch(t, nil)
	if _, cmd := m.goTo("x:3"); cmd != nil {
		t.Error("inside tmux the dashboard stays open")
	}
	m.opts.Popup = true
	if _, cmd := m.goTo("x:4"); !isQuitShell(cmd) {
		t.Error("the popup closes after switching")
	}
	if len(*switched) != 1 || (*switched)[0] != "x:2" {
		t.Errorf("switch targets: %v", *switched)
	}
}

func TestGlobalKeysFull(t *testing.T) {
	m := newModelShell(t)
	fakeStart(t, "assistant:1", nil)
	pressShell(m, "@")
	if m.Result.Attach != "assistant:1" {
		t.Errorf("@ opens the assistant: %q", m.Result.Attach)
	}

	pressShell(m, "u")
	if m.prompt == nil || m.prompt.title != "open a link in Claude" {
		t.Fatal("u asks for a link")
	}
	m.prompt = nil

	if cmd := pressShell(m, "ctrl+u"); cmd == nil || m.upd == nil {
		t.Error("ctrl+u starts the update")
	}
	m.upd = nil

	m.switchView(vUsage)
	if _, cmd := m.updateKey(keyShell("Z")); cmd != nil {
		t.Error("an unbound key on Usage does nothing")
	}
}

func TestUnknownViewFull(t *testing.T) {
	m := newModelShell(t)
	m.view = nViews
	if m.viewList(10) != "" || m.viewDetail() != "" || m.subtitle() != "" || m.count() != 0 {
		t.Error("a view without a tab renders nothing")
	}
}

func TestMaybeUsageScansFull(t *testing.T) {
	m := newModelShell(t)
	var scanned *claude.UsageScanner
	fake(t, &scanUsage, func(s *claude.UsageScanner) []claude.DayModel {
		scanned = s
		return []claude.DayModel{{Day: "2026-11-02"}}
	})
	m.usageScanner = claude.NewUsageScanner(nil, 31)
	cmd := m.maybeUsage()
	if cmd == nil || !m.usageLoading {
		t.Fatal("a stale usage scan starts")
	}
	if msg, ok := cmd().(usageMsg); !ok || len(msg) != 1 || scanned != m.usageScanner {
		t.Errorf("the model's scanner runs: %#v", msg)
	}
}

func TestProfileForUnknownFull(t *testing.T) {
	m := newModelShell(t)
	m.store.Profiles = nil
	if p := m.profileFor("/code/x", 1); p.Name != "default" {
		t.Errorf("without profiles the default one: %+v", p)
	}
}

func TestOpenTeamsInTheAppFull(t *testing.T) {
	ran := fakeCombined(t, "", nil)
	openURL("https://teams.microsoft.com/l/message/19:x@thread.tacv2/1730?tenantId=t")()
	openURL("https://teams.microsoft.com/v2/")()
	if got := strings.Join(*ran, "|"); got != "open msteams:/l/message/19:x@thread.tacv2/1730?tenantId=t|open https://teams.microsoft.com/v2/" {
		t.Errorf("a Teams link opens the app: %s", got)
	}
	ran = fakeCombined(t, "no app", errors.New("exit status 1"))
	if msg := openURL("https://teams.cloud.microsoft/l/chat/19:a/0")(); msg == nil {
		t.Error("both failing flashes")
	}
	if got := strings.Join(*ran, "|"); got != "open msteams:/l/chat/19:a/0|open https://teams.cloud.microsoft/l/chat/19:a/0" {
		t.Errorf("without the app, the browser: %s", got)
	}
}

func TestRunAndClipboardFull(t *testing.T) {
	ran := fakeCombined(t, "  boom\n", errors.New("exit status 1"))
	msg := openURL("https://example.org")()
	if f, ok := msg.(flashMsg); !ok || f.text != "boom exit status 1" || !f.err {
		t.Errorf("a failing command flashes its output: %#v", msg)
	}
	fakeCombined(t, "", nil)
	if msg := openURL("x")(); msg != nil {
		t.Errorf("a quiet success says nothing: %#v", msg)
	}
	if len(*ran) != 1 || (*ran)[0] != "open https://example.org" {
		t.Errorf("ran: %v", *ran)
	}

	copied := fakeClipboard(t, nil)
	if msg := copyText("ABC-1", "key")(); msg != (flashMsg{"copied key", false}) {
		t.Errorf("copy: %#v", msg)
	}
	if len(*copied) != 1 || (*copied)[0] != "pbcopy < ABC-1" {
		t.Errorf("pbcopy gets the text on stdin: %v", *copied)
	}
	fakeClipboard(t, errors.New("no pbcopy"))
	if msg := copyText("x", "key")(); msg != (flashMsg{"no pbcopy", true}) {
		t.Errorf("copy failure: %#v", msg)
	}
}

func TestHomeFull(t *testing.T) {
	switched := fakeSwitch(t, nil)
	if err := Home(); err != nil || len(*switched) != 1 || (*switched)[0] != "="+tmux.Home {
		t.Errorf("home switches to claudia's session: %v", *switched)
	}
}
