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
	{Key: "assistant_dir", Group: "Code", Label: "assistant dir", Kind: KindPath,
		Default: "the code root", Help: "where the @ assistant starts unless you pick another dir"},
	{Key: "personal_dir", Group: "Code", Label: "personal dir", Kind: KindString,
		Default: "Personal", Help: "the folder under the code root where new GitHub repos go; new GitLab repos go next to clones under the work dir"},

	{Key: "profiles", Group: "Claude", Label: "profiles", Kind: KindJSON,
		Default: "every ~/.claude and ~/.claude-<name>", Help: "Claude Code config dirs by name", Example: `{"work": "~/.claude-work", "personal": "~/.claude"}`},
	{Key: "work_profile", Group: "Claude", Label: "work profile", Kind: KindString,
		Default: "work", Help: "the profile for repos under the work dir"},
	{Key: "remote_control", Group: "Claude", Label: "remote control", Kind: KindJSON,
		Help: "profiles whose sessions start with Remote Control", Example: `["work"]`},
	{Key: "session_names", Group: "Claude", Label: "session names", Kind: KindJSON,
		Default: "<verb>: <summary>", Help: "names per intent: analysis, implementation, general, continue, test, review, fix, reply, readiness, mr, qa; {key}, {summary} and {verb} are filled in",
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
	{Key: "readiness_prompt", Group: "Prompts", Label: "readiness", Kind: KindPrompt,
		Default: defaultReadinessPrompt, Help: "the Board's readiness step: checks a ticket is specified well enough and posts questions when it isn't; {key} and {brief} as above"},
	{Key: "mr_prompt", Group: "Prompts", Label: "open MR", Kind: KindPrompt,
		Default: defaultMRPrompt, Help: "the Board's open MR step: pushes the branch and opens the MR; {key} and {brief} as above"},
	{Key: "qa_prompt", Group: "Prompts", Label: "QA notes", Kind: KindPrompt,
		Default: defaultQAPrompt, Help: "the Board's QA notes step: a Jira comment for the tester; {key} and {brief} as above"},

	{Key: "teams.tenant", Group: "Teams", Label: "tenant", Kind: KindString, Help: "the Entra tenant claudia signs in to"},
	{Key: "teams.client_id", Group: "Teams", Label: "client id", Kind: KindString, Help: "the Entra app claudia signs in with"},
	{Key: "teams.team_id", Group: "Teams", Label: "team id", Kind: KindString, Help: "from the channel's \"Get link to channel\" URL"},
	{Key: "teams.channel_id", Group: "Teams", Label: "channel id", Kind: KindString, Help: "from the channel's \"Get link to channel\" URL"},
	{Key: "teams.me", Group: "Teams", Label: "me", Kind: KindString, Help: "your Teams display name, to tell your replies from others'"},
	{Key: "teams.mail.direct", Group: "Teams", Label: "mail to you", Kind: KindBool,
		Default: "on", Help: "list Focused mail with you in To; setting this or the rules turns mail on"},
	{Key: "teams.mail.rules", Group: "Teams", Label: "mail rules", Kind: KindJSON,
		Help: "list mail regardless of recipients when the sender or subject contains the text", Example: `[{"from": "boss@"}, {"subject": "Approval"}]`},

	{Key: "board.statuses", Group: "Board", Label: "statuses", Kind: KindJSON,
		Default: "worked out from MRs and sessions", Help: "puts a Jira status in a Board column: Up next, Plan, Building, Review, Merge, QA or Done",
		Example: `{"Ready for QA": "QA", "Blocked": "Up next"}`},

	{Key: "autopilot.enabled", Group: "Autopilot", Label: "enabled", Kind: KindBool,
		Default: "off", Help: "the watcher starts Board steps on its own within the limits below; P on the Board pauses it"},
	{Key: "autopilot.steps", Group: "Autopilot", Label: "step modes", Kind: KindJSON,
		Default: `readiness, plan, mr, feedback, qa, review auto; implement after-plan-ok; merge ask`,
		Help:    "per step: auto starts it, after-plan-ok (implement) waits for p on the plan unless it's simple, ask waits for s, off never offers it; A on a card overrides them for that card",
		Example: `{"implement": "ask", "qa": "off"}`},
	{Key: "autopilot.allow_auto_merge", Group: "Autopilot", Label: "auto merge", Kind: KindBool,
		Default: "off", Help: "lets merge be auto, in step modes or on a card"},
	{Key: "autopilot.review_approve", Group: "Autopilot", Label: "approve clean", Kind: KindBool,
		Default: "on", Help: "approve an MR whose review run found nothing, on a green pipeline and the head it read"},
	{Key: "autopilot.review_react", Group: "Autopilot", Label: "react with", Kind: KindString,
		Default: "✅", Help: "the emoji put on the MR's Teams post when it's approved that way; empty for none"},
	{Key: "autopilot.hours", Group: "Autopilot", Label: "hours", Kind: KindString,
		Default: "09:00-18:00", Help: "when new runs may start; outside them nothing new starts, and nothing running is stopped"},
	{Key: "autopilot.days", Group: "Autopilot", Label: "days", Kind: KindJSON,
		Default: "Monday to Friday", Help: "the days new runs may start", Example: `["mon", "tue", "wed", "thu", "fri"]`},
	{Key: "autopilot.max_running", Group: "Autopilot", Label: "max running", Kind: KindJSON,
		Default: "1", Help: "runs going at once, the ones you started included; a card only ever runs one", Example: `1`},
	{Key: "autopilot.max_five_hour", Group: "Autopilot", Label: "5h window", Kind: KindJSON,
		Default: "60", Help: "plan usage in percent above which nothing new starts", Example: `60`},
	{Key: "autopilot.max_five_hour_after_hours", Group: "Autopilot", Label: "5h after hours", Kind: KindJSON,
		Default: "90", Help: "the same for a 5h window that resets after your hours end, which you won't use up yourself", Example: `90`},
	{Key: "autopilot.max_seven_day", Group: "Autopilot", Label: "7d window", Kind: KindJSON,
		Default: "80", Help: "weekly plan usage in percent above which nothing new starts", Example: `80`},
	{Key: "autopilot.max_ticket_usd", Group: "Autopilot", Label: "per ticket $", Kind: KindJSON,
		Default: "50", Help: "a ticket that cost this much stops having its steps started for it", Example: `50`},
	{Key: "autopilot.max_day_usd", Group: "Autopilot", Label: "per day $", Kind: KindJSON,
		Default: "30", Help: "what the autopilot's runs may cost in a day", Example: `30`},
	{Key: "autopilot.review_every", Group: "Autopilot", Label: "review every", Kind: KindDuration,
		Default: "1h", Help: "at most one review started on its own per period, e.g. 30m"},

	{Key: "brief_every", Group: "UI", Label: "brief every", Kind: KindDuration,
		Default: "2h", Help: "how often the inbox brief is rewritten, e.g. 30m; off leaves only the on-demand refresh"},
	{Key: "rounded_pills", Group: "UI", Label: "rounded pills", Kind: KindBool,
		Default: "on", Help: "off draws pills square, for fonts without the Nerd Font half circles"},
	{Key: "terminal", Group: "UI", Label: "terminal", Kind: KindString,
		Default: "auto", Help: "the terminal app claudia runs in, e.g. iTerm2; auto detects it, falling back to Ghostty"},
	{Key: "animations", Group: "UI", Label: "animations", Kind: KindBool,
		Default: "on", Help: "off keeps the session status glyphs and loading spinners still"},
	{Key: "workers", Group: "UI", Label: "workers", Kind: KindString, Env: "CLAUDIA_WORKERS",
		Default: "hidden", Help: "claudia's own background claude runs (brief, summaries) in the Sessions tab: hidden, top or bottom"},
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
