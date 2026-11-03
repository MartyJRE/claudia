// Package config holds the site-specific settings: where GitLab and Atlassian
// live and which Jira project to follow. They come from
// ~/.config/claudia/config.json, and environment variables override the file.
package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	// GitLabHost defaults to glab's configured host.
	GitLabHost string `json:"gitlab_host"`
	// AtlassianURL is the Jira/Confluence site, e.g. https://example.atlassian.net.
	AtlassianURL string `json:"atlassian_url"`
	// JiraProject is the project whose sprint the team view shows.
	JiraProject string `json:"jira_project"`
	// TicketKeys are the project keys recognised in branches and titles;
	// defaults to JiraProject.
	TicketKeys []string `json:"ticket_keys"`
	// CodeRoot is where git repos are discovered; defaults to ~/Documents/Code.
	CodeRoot string `json:"code_root"`
	// WorkDir is the folder under CodeRoot whose repos are listed first and
	// started with the work profile; defaults to "Work".
	WorkDir string `json:"work_dir"`
	// Profiles names Claude Code config dirs, e.g. {"work": "~/.claude-work"}.
	// Without it every ~/.claude and ~/.claude-<name> dir is used, named
	// "default" and <name>.
	Profiles map[string]string `json:"profiles"`
	// WorkProfile is the profile for repos under WorkDir; defaults to "work"
	// when such a profile exists.
	WorkProfile string `json:"work_profile"`
	// Vault is an Obsidian vault whose notes are shown next to tickets.
	Vault string `json:"vault"`
	// VaultProjects is the vault folder holding one subfolder per ticket;
	// defaults to "01 - Projects".
	VaultProjects string `json:"vault_projects"`
	// BriefEvery is how often the inbox brief is rewritten, e.g. "2h"; "off"
	// leaves only the on-demand refresh. Defaults to 2h.
	BriefEveryRaw string        `json:"brief_every"`
	BriefEvery    time.Duration `json:"-"`
	// RemoteControl lists the profiles whose sessions start with Remote Control,
	// e.g. ["work"].
	RemoteControl []string `json:"remote_control"`
	// ReviewPrompt starts a review session; {urls} is replaced by the MR links,
	// e.g. "/review-mr {urls}" to use a review skill.
	ReviewPrompt string `json:"review_prompt"`
	// AnalysisPrompt starts the analysis of a ticket that isn't started yet;
	// {key} is the ticket key and {brief} where its exported description is.
	AnalysisPrompt string `json:"analysis_prompt"`
	// ImplementationPrompt starts implementing a ticket; same placeholders.
	ImplementationPrompt string `json:"implementation_prompt"`
	// SessionNames names sessions per intent (analysis, implementation,
	// general, review, fix, test, continue, reply), e.g. {"analysis":
	// "{key}-analysis"}; {key}, {summary} and {verb} are filled in.
	SessionNames map[string]string `json:"session_names"`
	// Keys rebinds actions, e.g. {"sessions.close": "ctrl+w"}; a list binds
	// several keys. `claudia keys` lists the actions.
	Keys map[string]KeyList `json:"keys"`
	// RoundedPillsRaw: false draws pills square, for fonts without the Nerd
	// Font half circles. Ghostty ships them, so they're on by default.
	RoundedPillsRaw *bool `json:"rounded_pills"`
}

func (c Config) RoundedPills() bool { return c.RoundedPillsRaw == nil || *c.RoundedPillsRaw }

// KeyList is one key or a list of them.
type KeyList []string

func (k *KeyList) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*k = KeyList{one}
		return nil
	}
	return json.Unmarshal(b, (*[]string)(k))
}

const defaultReviewPrompt = "Review these merge requests as one change: {urls}. " +
	"Read each diff and its existing discussion with glab, read the surrounding code where the diff isn't enough, " +
	"and check that the MRs fit together. List findings by severity with file and line. Don't post anything to GitLab."

const defaultImplementationPrompt = "Read the Jira issue {key} at {brief} and implement it in this repo. " +
	"Check out the ticket's branch if there is one, else create one from the base branch; stop and ask if the working tree has changes. " +
	"Plan first and show me the plan, then implement it with tests. Don't push and don't write anything to Jira until I confirm."

const defaultAnalysisPrompt = "Read the Jira issue {key} at {brief} and analyse it before implementation: " +
	"what changes where in this codebase, the rollout steps, what's out of scope, and the open questions. " +
	"Don't change any files and don't write anything to Jira."

var (
	once sync.Once
	cfg  Config
)

func Path() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "claudia", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "claudia", "config.json")
}

func Get() Config {
	once.Do(func() {
		if b, err := os.ReadFile(Path()); err == nil {
			_ = json.Unmarshal(b, &cfg)
		}
		env := func(dst *string, key string) {
			if v := os.Getenv(key); v != "" {
				*dst = v
			}
		}
		env(&cfg.GitLabHost, "CLAUDIA_GITLAB_HOST")
		env(&cfg.AtlassianURL, "ATLASSIAN_BASE_URL")
		env(&cfg.JiraProject, "CLAUDIA_JIRA_PROJECT")
		env(&cfg.CodeRoot, "CLAUDIA_CODE_ROOT")
		env(&cfg.WorkDir, "CLAUDIA_PREFER")
		if k := os.Getenv("CLAUDIA_JIRA_KEYS"); k != "" {
			cfg.TicketKeys = strings.Split(k, ",")
		}

		if cfg.GitLabHost == "" {
			if out, err := exec.Command("glab", "config", "get", "host").Output(); err == nil {
				cfg.GitLabHost = strings.TrimSpace(string(out))
			}
		}
		if cfg.GitLabHost == "" {
			cfg.GitLabHost = "gitlab.com"
		}
		cfg.AtlassianURL = strings.TrimSuffix(cfg.AtlassianURL, "/")
		if len(cfg.TicketKeys) == 0 && cfg.JiraProject != "" {
			cfg.TicketKeys = []string{cfg.JiraProject}
		}
		home, _ := os.UserHomeDir()
		for name, dir := range cfg.Profiles {
			if strings.HasPrefix(dir, "~/") {
				cfg.Profiles[name] = filepath.Join(home, dir[2:])
			}
		}
		if strings.HasPrefix(cfg.Vault, "~/") {
			cfg.Vault = filepath.Join(home, cfg.Vault[2:])
		}
		if cfg.VaultProjects == "" {
			cfg.VaultProjects = "01 - Projects"
		}
		cfg.BriefEvery = 2 * time.Hour
		if v := cfg.BriefEveryRaw; v == "off" || v == "0" {
			cfg.BriefEvery = 0
		} else if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.BriefEvery = d
		}
		if cfg.WorkProfile == "" {
			cfg.WorkProfile = "work"
		}
		if cfg.CodeRoot == "" {
			cfg.CodeRoot = filepath.Join(home, "Documents", "Code")
		} else if strings.HasPrefix(cfg.CodeRoot, "~/") {
			cfg.CodeRoot = filepath.Join(home, cfg.CodeRoot[2:])
		}
		if cfg.WorkDir == "" {
			cfg.WorkDir = "Work"
		}
		if cfg.ReviewPrompt == "" {
			cfg.ReviewPrompt = defaultReviewPrompt
		}
		if cfg.AnalysisPrompt == "" {
			cfg.AnalysisPrompt = defaultAnalysisPrompt
		}
		if cfg.ImplementationPrompt == "" {
			cfg.ImplementationPrompt = defaultImplementationPrompt
		}
	})
	return cfg
}
