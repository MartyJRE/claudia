package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MartyJRE/claudia/internal/config"
)

// settingsModelShell opens the settings page over a config.json holding file.
func settingsModelShell(t *testing.T, file string) *Model {
	t.Helper()
	m := newModelShell(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, f := range config.Fields {
		if f.Env != "" {
			t.Setenv(f.Env, "")
		}
	}
	if file != "" {
		os.MkdirAll(filepath.Dir(config.Path()), 0o755)
		if err := os.WriteFile(config.Path(), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pressShell(m, ",")
	if m.settings == nil {
		t.Fatalf("no settings page: %s", m.flash)
	}
	return m
}

func cursorToShell(t *testing.T, m *Model, key string) {
	t.Helper()
	pressShell(m, "g")
	for m.settings.field().Key != key {
		before := m.settings.cur
		pressShell(m, "j")
		if m.settings.cur == before {
			t.Fatalf("no field %s", key)
		}
	}
}

func saveShell(m *Model) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	return cmd
}

func configFileShell(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSettingsEditSaveRestartShell(t *testing.T) {
	m := settingsModelShell(t, `{"work_dir": "Job", "vault": "~/Notes", "future": 1}`)
	stopped := 0
	fake(t, &stopWatcher, func() { stopped++ })
	containsAllShell(t, "the page", screenShell(m), "settings", "GitLab", "Teams", "work dir", "Job", "file", "default: ~/Documents/Code")

	cursorToShell(t, m, "work_dir")
	pressShell(m, "enter", "ctrl+u")
	typeShell(m, "Corp")
	pressShell(m, "enter")
	containsAllShell(t, "edited", screenShell(m), "● work dir", "Corp")
	cursorToShell(t, m, "rounded_pills")
	pressShell(m, "enter")
	containsAllShell(t, "edited", screenShell(m), "● rounded pills", "off", "● 2 unsaved")

	cmd := saveShell(m)
	if !isQuitShell(cmd) || !m.Result.Restart || stopped != 1 {
		t.Fatalf("saving restarts claudia and stops the watcher: quit %v restart %v stopped %d (%s)", isQuitShell(cmd), m.Result.Restart, stopped, m.flash)
	}
	f := configFileShell(t)
	containsAllShell(t, "the file", f, `"work_dir": "Corp"`, `"rounded_pills": false`, `"vault": "~/Notes"`, `"future": 1`)
	lacksShell(t, "the file", f, "code_root", "Documents/Code", "brief_every")
}

func TestSettingsEnvOverrideShell(t *testing.T) {
	m := settingsModelShell(t, `{"work_dir": "Job"}`)
	t.Setenv("CLAUDIA_PREFER", "Corp")
	cursorToShell(t, m, "work_dir")
	pressShell(m, "enter")
	if m.settings.editing || !strings.Contains(m.flash, "CLAUDIA_PREFER sets this") {
		t.Fatalf("an env-set field can't be edited: editing %v flash %q", m.settings.editing, m.flash)
	}
	pressShell(m, "d")
	if !m.settings.draft.Has("work_dir") {
		t.Error("nor reset")
	}
	containsAllShell(t, "the row", screenShell(m), "Corp", "env CLAUDIA_PREFER")
}

func TestSettingsDurationAndDefaultShell(t *testing.T) {
	m := settingsModelShell(t, `{"brief_every": "30m"}`)
	cursorToShell(t, m, "brief_every")
	pressShell(m, "enter", "ctrl+u")
	typeShell(m, "daily")
	pressShell(m, "enter")
	if !m.settings.editing || !m.flashErr || !strings.Contains(m.flash, "isn't a duration") {
		t.Fatalf("a bad duration keeps the input open: editing %v %q", m.settings.editing, m.flash)
	}
	pressShell(m, "esc")
	if m.settings.editing || m.settings.changes() != 0 {
		t.Fatal("esc drops the edit")
	}
	pressShell(m, "d")
	containsAllShell(t, "reset", screenShell(m), "● brief every", "default: 2h")
}

func TestSettingsDiscardShell(t *testing.T) {
	m := settingsModelShell(t, "")
	cursorToShell(t, m, "rounded_pills")
	pressShell(m, "enter", "esc")
	if m.settings == nil || !strings.Contains(m.flash, "unsaved changes") {
		t.Fatalf("esc on unsaved changes asks first: %q", m.flash)
	}
	pressShell(m, "esc")
	if m.settings != nil {
		t.Fatal("a second esc drops them")
	}
	if _, err := os.Stat(config.Path()); !os.IsNotExist(err) {
		t.Error("nothing was written")
	}
}

func TestSettingsPromptShell(t *testing.T) {
	m := settingsModelShell(t, "")
	cursorToShell(t, m, "review_prompt")
	pressShell(m, "enter")
	if !strings.HasPrefix(m.settings.area.Value(), "Review these merge requests") {
		t.Fatalf("the default prompt is there to edit: %q", m.settings.area.Value())
	}
	containsAllShell(t, "the box", screenShell(m), "review prompt", "alt+enter new line")
	pressShell(m, "enter")
	if m.settings.changes() != 0 {
		t.Fatal("keeping the default writes nothing")
	}
	pressShell(m, "enter")
	m.settings.area.SetValue("/review-mr {urls}")
	pressShell(m, "enter")
	if v, _ := m.settings.str("review_prompt"); v != "/review-mr {urls}" {
		t.Errorf("prompt %q", v)
	}
}

// fakeEditorScript makes $EDITOR a script that writes body over the file, and
// runs it in place of handing it the terminal.
func fakeEditorScript(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "body"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "ed")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat '"+filepath.Join(dir, "body")+"' > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", script)
	fake(t, &execProcess, func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		err := c.Run()
		return func() tea.Msg { return fn(err) }
	})
}

func editJSONShell(t *testing.T, m *Model, key, body string) {
	t.Helper()
	fakeEditorScript(t, body)
	cursorToShell(t, m, key)
	cmd := pressShell(m, "enter")
	if cmd == nil {
		t.Fatalf("%s: no editor (%s)", key, m.flash)
	}
	m.Update(cmd())
}

func TestSettingsJSONInEditorShell(t *testing.T) {
	m := settingsModelShell(t, `{"profiles": {"work": "~/.claude-work"}}`)
	fake(t, &stopWatcher, func() {})

	editJSONShell(t, m, "profiles", `["work"]`)
	if !m.flashErr || !strings.Contains(m.flash, "wasn't changed") || m.settings.changes() != 0 {
		t.Fatalf("the wrong type isn't applied: %q", m.flash)
	}
	editJSONShell(t, m, "profiles", `{oops`)
	if !strings.Contains(m.flash, "isn't valid JSON") || m.settings.changes() != 0 {
		t.Fatalf("broken JSON isn't applied: %q", m.flash)
	}
	editJSONShell(t, m, "profiles", "{\n  \"work\": \"~/.claude-w\",\n  \"me\": \"~/.claude\"\n}\n")
	containsAllShell(t, "the row", screenShell(m), "● profiles", `"me":"~/.claude"`)

	editJSONShell(t, m, "keys", `{"sessions.close": "j"}`)
	containsAllShell(t, "a clash shows under keys", screenShell(m), "✗ j is bound to both")
	if cmd := saveShell(m); isQuitShell(cmd) || !strings.Contains(m.flash, "not saved") {
		t.Fatalf("clashing keys aren't saved: %q", m.flash)
	}
	editJSONShell(t, m, "keys", `{"sessions.close": "ctrl+w"}`)
	if cmd := saveShell(m); !isQuitShell(cmd) {
		t.Fatalf("saved: %q", m.flash)
	}
	containsAllShell(t, "the file", configFileShell(t), `"me": "~/.claude"`, `"sessions.close": "ctrl+w"`)
}

func TestSettingsJSONStartsFromExampleShell(t *testing.T) {
	m := settingsModelShell(t, "")
	var opened string
	fake(t, &execProcess, func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		b, _ := os.ReadFile(c.Args[len(c.Args)-1])
		opened = string(b)
		return func() tea.Msg { return fn(nil) }
	})
	cursorToShell(t, m, "teams.mail.rules")
	m.Update(pressShell(m, "enter")())
	if !strings.Contains(opened, `"from": "boss@"`) || m.settings.changes() != 0 {
		t.Errorf("an unset field opens on its example, and leaving it be changes nothing: %q", opened)
	}
}

func TestSettingsBrokenFileShell(t *testing.T) {
	m := newModelShell(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.MkdirAll(filepath.Dir(config.Path()), 0o755)
	os.WriteFile(config.Path(), []byte("{nope"), 0o600)
	pressShell(m, ",")
	if m.settings != nil || !m.flashErr {
		t.Errorf("a file that doesn't parse isn't opened, so it can't be overwritten: %q", m.flash)
	}
}
