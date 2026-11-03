package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fresh(t *testing.T, file string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	for _, k := range []string{"CLAUDIA_GITLAB_HOST", "ATLASSIAN_BASE_URL", "CLAUDIA_JIRA_PROJECT", "CLAUDIA_CODE_ROOT", "CLAUDIA_PREFER", "CLAUDIA_JIRA_KEYS"} {
		t.Setenv(k, "")
	}
	// Keeps Get from asking a real glab for its host.
	t.Setenv("PATH", "")
	if file != "" {
		p := Path()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	once, cfg = sync.Once{}, Config{}
	t.Cleanup(func() { once, cfg = sync.Once{}, Config{} })
	return home
}

func TestPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x")
	if got := Path(); got != "/x/claudia/config.json" {
		t.Errorf("got %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/h")
	if got := Path(); got != "/h/.config/claudia/config.json" {
		t.Errorf("got %s", got)
	}
}

func TestDefaults(t *testing.T) {
	home := fresh(t, "")
	c := Get()
	if c.GitLabHost != "gitlab.com" || c.VaultProjects != "01 - Projects" || c.WorkProfile != "work" || c.WorkDir != "Work" {
		t.Errorf("defaults: %+v", c)
	}
	if c.CodeRoot != filepath.Join(home, "Documents", "Code") {
		t.Errorf("code root %s", c.CodeRoot)
	}
	if c.BriefEvery != 2*time.Hour {
		t.Errorf("brief every %v", c.BriefEvery)
	}
	if c.ReviewPrompt != defaultReviewPrompt || c.AnalysisPrompt != defaultAnalysisPrompt || c.ImplementationPrompt != defaultImplementationPrompt {
		t.Error("default prompts")
	}
	if len(c.TicketKeys) != 0 || !c.RoundedPills() {
		t.Errorf("keys %v rounded %v", c.TicketKeys, c.RoundedPills())
	}
}

func TestFile(t *testing.T) {
	home := fresh(t, `{
		"gitlab_host": "git.acme.io",
		"atlassian_url": "https://acme.atlassian.net/",
		"jira_project": "PAY",
		"code_root": "~/src",
		"profiles": {"work": "~/.claude-work", "abs": "/opt/claude"},
		"vault": "~/Notes",
		"brief_every": "30m",
		"review_prompt": "look at {urls}",
		"keys": {"sessions.close": "ctrl+w", "sessions.kill": ["K", "ctrl+k"]},
		"rounded_pills": false
	}`)
	c := Get()
	if c.GitLabHost != "git.acme.io" || c.AtlassianURL != "https://acme.atlassian.net" {
		t.Errorf("hosts: %q %q", c.GitLabHost, c.AtlassianURL)
	}
	if len(c.TicketKeys) != 1 || c.TicketKeys[0] != "PAY" {
		t.Errorf("ticket keys %v", c.TicketKeys)
	}
	if c.CodeRoot != filepath.Join(home, "src") || c.Vault != filepath.Join(home, "Notes") {
		t.Errorf("paths: %s %s", c.CodeRoot, c.Vault)
	}
	if c.Profiles["work"] != filepath.Join(home, ".claude-work") || c.Profiles["abs"] != "/opt/claude" {
		t.Errorf("profiles %v", c.Profiles)
	}
	if c.BriefEvery != 30*time.Minute || c.ReviewPrompt != "look at {urls}" {
		t.Errorf("brief %v review %q", c.BriefEvery, c.ReviewPrompt)
	}
	if k := c.Keys["sessions.kill"]; len(k) != 2 || k[1] != "ctrl+k" || len(c.Keys["sessions.close"]) != 1 {
		t.Errorf("keys %v", c.Keys)
	}
	if c.RoundedPills() {
		t.Error("rounded_pills false ignored")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	fresh(t, `{"gitlab_host": "file.io", "jira_project": "PAY", "code_root": "/file", "work_dir": "Job"}`)
	t.Setenv("CLAUDIA_GITLAB_HOST", "env.io")
	t.Setenv("ATLASSIAN_BASE_URL", "https://env.atlassian.net")
	t.Setenv("CLAUDIA_JIRA_PROJECT", "ENV")
	t.Setenv("CLAUDIA_CODE_ROOT", "/env")
	t.Setenv("CLAUDIA_PREFER", "Corp")
	t.Setenv("CLAUDIA_JIRA_KEYS", "A,B")
	c := Get()
	if c.GitLabHost != "env.io" || c.AtlassianURL != "https://env.atlassian.net" || c.JiraProject != "ENV" || c.CodeRoot != "/env" || c.WorkDir != "Corp" {
		t.Errorf("got %+v", c)
	}
	if len(c.TicketKeys) != 2 || c.TicketKeys[1] != "B" {
		t.Errorf("keys %v", c.TicketKeys)
	}
}

func TestBriefEvery(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"off":   0,
		"0":     0,
		"45m":   45 * time.Minute,
		"-1h":   2 * time.Hour,
		"daily": 2 * time.Hour,
	} {
		fresh(t, `{"brief_every": "`+raw+`"}`)
		if got := Get().BriefEvery; got != want {
			t.Errorf("%q: got %v want %v", raw, got, want)
		}
	}
}

func TestGetIsCached(t *testing.T) {
	fresh(t, `{"jira_project": "ONE"}`)
	_ = Get()
	if err := os.WriteFile(Path(), []byte(`{"jira_project": "TWO"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Get().JiraProject; got != "ONE" {
		t.Errorf("got %q", got)
	}
}

func TestBrokenFileKeepsDefaults(t *testing.T) {
	fresh(t, `{not json`)
	if c := Get(); c.GitLabHost != "gitlab.com" || c.WorkDir != "Work" {
		t.Errorf("got %+v", c)
	}
}
