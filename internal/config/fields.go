package config

import "os"

type Kind string

const (
	KindString   Kind = "string"
	KindPath     Kind = "path"
	KindBool     Kind = "bool"
	KindDuration Kind = "duration"
	KindPrompt   Kind = "prompt"
	KindJSON     Kind = "json" // maps and lists, edited as JSON in $EDITOR
)

// Field describes one config.json key for the settings page.
type Field struct {
	Key     string
	Group   string
	Label   string
	Kind    Kind
	Help    string
	Default string // what applies when the key isn't set
	Env     string // the variable that overrides the file, if any
	Example string // JSON a field of KindJSON starts from when it isn't set
}

// Fields lists every key Config reads, in the order the settings page shows them.
var Fields = []Field{
	{Key: "gitlab_host", Group: "GitLab", Label: "host", Kind: KindString, Env: "CLAUDIA_GITLAB_HOST",
		Default: "glab's host, else gitlab.com", Help: "the GitLab instance MRs come from"},

	{Key: "atlassian_url", Group: "Jira", Label: "Atlassian site", Kind: KindString, Env: "ATLASSIAN_BASE_URL",
		Help: "the Jira/Confluence site, e.g. https://example.atlassian.net"},
	{Key: "jira_project", Group: "Jira", Label: "project", Kind: KindString, Env: "CLAUDIA_JIRA_PROJECT",
		Help: "the project whose sprint the Jira view shows"},
	{Key: "ticket_keys", Group: "Jira", Label: "ticket keys", Kind: KindJSON, Env: "CLAUDIA_JIRA_KEYS",
		Default: "the project", Help: "project keys recognised in branches and titles", Example: `["ABC", "OPS"]`},

	{Key: "code_root", Group: "Code", Label: "code root", Kind: KindPath, Env: "CLAUDIA_CODE_ROOT",
		Default: "~/Documents/Code", Help: "where git repos are discovered"},
	{Key: "work_dir", Group: "Code", Label: "work dir", Kind: KindString, Env: "CLAUDIA_PREFER",
		Default: "Work", Help: "the folder under the code root whose repos are listed first and use the work profile"},

	{Key: "profiles", Group: "Claude", Label: "profiles", Kind: KindJSON,
		Default: "every ~/.claude and ~/.claude-<name>", Help: "Claude Code config dirs by name", Example: `{"work": "~/.claude-work", "personal": "~/.claude"}`},
	{Key: "work_profile", Group: "Claude", Label: "work profile", Kind: KindString,
		Default: "work", Help: "the profile for repos under the work dir"},
	{Key: "remote_control", Group: "Claude", Label: "remote control", Kind: KindJSON,
		Help: "profiles whose sessions start with Remote Control", Example: `["work"]`},
	{Key: "session_names", Group: "Claude", Label: "session names", Kind: KindJSON,
		Default: "<verb>: <summary>", Help: "names per intent: analysis, implementation, general, continue, test, review, fix, reply; {key}, {summary} and {verb} are filled in",
		Example: `{"analysis": "{key}-analysis", "general": "{key}"}`},

	{Key: "vault", Group: "Vault", Label: "vault", Kind: KindPath,
		Help: "an Obsidian vault whose notes show next to tickets, e.g. ~/Documents/Notes"},
	{Key: "vault_projects", Group: "Vault", Label: "projects folder", Kind: KindString,
		Default: "01 - Projects", Help: "the vault folder with one subfolder per ticket"},

	{Key: "review_prompt", Group: "Prompts", Label: "review", Kind: KindPrompt,
		Default: defaultReviewPrompt, Help: "starts a review session; {urls} is replaced by the MR links"},
	{Key: "analysis_prompt", Group: "Prompts", Label: "analysis", Kind: KindPrompt,
		Default: defaultAnalysisPrompt, Help: "starts analysing a ticket; {key} is its key, {brief} where its description was exported"},
	{Key: "implementation_prompt", Group: "Prompts", Label: "implementation", Kind: KindPrompt,
		Default: defaultImplementationPrompt, Help: "starts implementing a ticket; {key} is its key, {brief} where its description was exported"},

	{Key: "teams.tenant", Group: "Teams", Label: "tenant", Kind: KindString, Help: "the Entra tenant claudia signs in to"},
	{Key: "teams.client_id", Group: "Teams", Label: "client id", Kind: KindString, Help: "the Entra app claudia signs in with"},
	{Key: "teams.team_id", Group: "Teams", Label: "team id", Kind: KindString, Help: "from the channel's \"Get link to channel\" URL"},
	{Key: "teams.channel_id", Group: "Teams", Label: "channel id", Kind: KindString, Help: "from the channel's \"Get link to channel\" URL"},
	{Key: "teams.me", Group: "Teams", Label: "me", Kind: KindString, Help: "your Teams display name, to tell your replies from others'"},
	{Key: "teams.mail.direct", Group: "Teams", Label: "mail to you", Kind: KindBool,
		Default: "on", Help: "list Focused mail with you in To; setting this or the rules turns mail on"},
	{Key: "teams.mail.rules", Group: "Teams", Label: "mail rules", Kind: KindJSON,
		Help: "list mail regardless of recipients when the sender or subject contains the text", Example: `[{"from": "boss@"}, {"subject": "Approval"}]`},

	{Key: "brief_every", Group: "UI", Label: "brief every", Kind: KindDuration,
		Default: "2h", Help: "how often the inbox brief is rewritten, e.g. 30m; off leaves only the on-demand refresh"},
	{Key: "rounded_pills", Group: "UI", Label: "rounded pills", Kind: KindBool,
		Default: "on", Help: "off draws pills square, for fonts without the Nerd Font half circles"},
	{Key: "keys", Group: "UI", Label: "keys", Kind: KindJSON,
		Help: "rebinds actions to a key or a list of keys; claudia keys lists the actions", Example: `{"sessions.close": "ctrl+w"}`},
}

// EnvValue is the environment's override of the field, or "".
func (f Field) EnvValue() string {
	if f.Env == "" {
		return ""
	}
	return os.Getenv(f.Env)
}

// Source is where the field's value comes from: "env <NAME>", "file" or "default".
func (f File) Source(fd Field) string {
	switch {
	case fd.EnvValue() != "":
		return "env " + fd.Env
	case f.Has(fd.Key):
		return "file"
	}
	return "default"
}
