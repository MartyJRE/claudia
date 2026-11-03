<p align="center">
  <img src="assets/logo.png" width="128" alt="claudia logo">
</p>

<h1 align="center">claudia</h1>

<p align="center">
  <b>A terminal cockpit for Claude Code.</b><br>
  Who's waiting on you, your merge requests, your sprint and every Claude session, in one TUI.
</p>

<p align="center">
  <img src="https://img.shields.io/badge/go-1.24+-61afef?style=flat-square&logo=go&logoColor=white" alt="Go 1.24+">
  <img src="https://img.shields.io/badge/macOS-tmux-c678dd?style=flat-square&logo=apple&logoColor=white" alt="macOS + tmux">
  <img src="https://img.shields.io/badge/GitLab%20·%20Jira%20·%20Confluence-98c379?style=flat-square" alt="GitLab, Jira, Confluence">
</p>

<p align="center">
  <img src="assets/preview.svg" width="860" alt="claudia's Inbox: everyone waiting on you, oldest first">
</p>

claudia lists review requests, Jira and Confluence mentions, review threads on
your MRs and Claude sessions that need input, oldest first, and jumps you into the
right Claude Code session for each: a review, a drafted reply, the fixes.

Claude itself always runs as the real `claude` CLI in tmux windows, so nothing
of Claude Code is reimplemented or lost. claudia only finds, starts, resumes and
switches between those sessions.

## Views

| Key | View | What it shows |
|---|---|---|
| 1 | Inbox | Everyone waiting on you, oldest first |
| 2 | Sessions | Live and recent Claude sessions with status, branch, ticket, model |
| 3 | Review | MRs where you are a reviewer and haven't approved yet |
| 4 | My MRs | Your open MRs (reviewers, pipelines, review threads) and recently merged ones |
| 5 | Jira | Your sprint, or the team's |
| 6 | Mentions | Jira comments and Confluence pages that mention you |
| 7 | Confluence | Pages you edited recently |
| 8 | Notes | Your Obsidian vault as a folder tree, or recently edited first |
| 9 | Usage | Tokens and API-equivalent cost per day, plan limits |

`u` opens any link in Claude (also `claudia open <link>` from a shell): an MR
link gets reviewed in your local clone even if it isn't assigned to you, a Jira
link gets what the ticket needs, anything else lands in a repo you pick. `n`
starts a plain session in any repo.

The Inbox opens with a short brief in plain English, written by a headless
`claude -p` over claudia's own data (no tools): who's waited longest, what's
failing, what to do next. It's rewritten every `brief_every` (default `2h`, `off`
to disable) and on `R`; `claudia brief` prints a fresh one. `@` opens a
long-lived assistant session that knows claudia's commands, for "what should I
do first?" and small errands.

With an Obsidian vault configured, every ticket shows its notes: the ones in
its project folder and every note that mentions the key (an epic includes its
stories' notes). `N` lists them, `enter` reads one, `e` edits it in `$EDITOR`,
`w` writes a new one into the ticket's folder.

`c` starts the Claude session the item calls for: a review for a review request,
a drafted reply for a mention, fixes for review threads. On a ticket it asks
which kind (analysis, implementation, general, review, …), with the one that
fits the ticket's state preselected, so `enter` just goes. Every view lists its
keys in the footer.

In Sessions, the live ones stay in the order they started, so activity never
reshuffles the list. `K` closes a session (`R` resumes it later). To see several
side by side, mark them with `m` and press `S`: they're tiled into one window in
a `splits` tmux session. `U` sends them back to their own windows, each one in
the session it came from.

## Claude can drive it too

Every session claudia starts or resumes is told about `claudia ctl`, so you can
ask Claude "is this merged yet?" and it checks, answers, clears the item from
your inbox and switches you back to claudia:

```sh
claudia ctl here            # this session's MRs, ticket and inbox items, as JSON
claudia ctl inbox           # the whole inbox
claudia ctl dismiss --here  # snooze this session's inbox items until they change
claudia ctl home [--close]  # back to claudia; --close also ends the session
```

## Requirements

- macOS, tmux, Go 1.24+
- [`glab`](https://gitlab.com/gitlab-org/cli), logged in (claudia uses its login)
- Claude Code
- Optional: Ghostty (images, notification click-through), a Swift toolchain for
  the native notification helper

## Install

```sh
go install github.com/MartyJRE/claudia@latest
claudia setup                # native notifications: builds ~/Applications/Claudia.app (needs Xcode CLT)
```

In `~/.tmux.conf`, to toggle between claudia and the session you came from:

```tmux
bind C if -F '#{==:#{session_name},claudia}' 'switch-client -l' 'new-session -A -s claudia "$HOME/go/bin/claudia ui"'
set -g allow-passthrough on   # ticket images through tmux
```

Run `claudia` to open it in its own tmux session.

When a Claude session starts waiting on you while you're in another tmux session,
it shows up there as an extra tab. The tab goes away once you've answered and
switched to another tab; the window stays in its repo's session throughout.

## Configuration

`~/.config/claudia/config.json` (every field is optional):

```json
{
  "atlassian_url": "https://your-site.atlassian.net",
  "jira_project": "ABC",
  "ticket_keys": ["ABC", "OPS"],
  "gitlab_host": "gitlab.example.com",
  "code_root": "~/Documents/Code",
  "work_dir": "Work",
  "review_prompt": "/review-mr {urls}",
  "analysis_prompt": "/story-analysis {key}",
  "session_names": {"analysis": "{key}-analysis", "general": "{key}"},
  "keys": {"sessions.close": "ctrl+w", "global.down": ["j", "down"]}
}
```

- `gitlab_host` defaults to glab's configured host.
- Claude profiles: every `~/.claude` and `~/.claude-<name>` dir with sessions is
  picked up (named `default` and `<name>`). Name them yourself with
  `"profiles": {"work": "~/.claude-work", "personal": "~/.claude"}`.
- `work_dir` is the folder under `code_root` whose repos are listed first and
  started with the `work` profile (`work_profile` to pick another); other repos
  use `personal` if it exists.
- `vault`: an Obsidian vault, e.g. `"~/Documents/Notes"`; `vault_projects` is its
  folder with one subfolder per ticket (default `01 - Projects`).
- `remote_control`: profiles whose sessions start with Remote Control, e.g. `["work"]`.
- `review_prompt`, `analysis_prompt` and `implementation_prompt` let you point at
  your own skills; the defaults are plain prompts.
- `session_names` names sessions per intent: `analysis`, `implementation`,
  `general`, `continue`, `test`, `review`, `fix`, `reply`. `{key}`, `{summary}`
  and `{verb}` are filled in. Without it, sessions are named `<verb>: <summary>`.
- `keys` rebinds any key outside text input: an action name to a key or a list
  of keys. `claudia keys` lists every action with its current keys and points out
  clashes. claudia also flags clashes when it starts.

Credentials stay in the environment: `ATLASSIAN_EMAIL` and `ATLASSIAN_API_TOKEN`
(an [Atlassian API token](https://id.atlassian.com/manage-profile/security/api-tokens)).

Plan limits come from Claude Code's status line. Add this to your status line
script to hand them to claudia:

```sh
if limits=$(jq -ce 'select(.rate_limits) | {rate_limits, at: now}' <<< "$input" 2>/dev/null); then
  out="${XDG_STATE_HOME:-$HOME/.local/state}/claudia"; mkdir -p "$out"
  f="$out/limits-$(basename "${CLAUDE_CONFIG_DIR:-$HOME/.claude}").json"
  printf '%s' "$limits" > "$f.tmp" && mv "$f.tmp" "$f"
fi
```

State and caches live in `~/.local/state/claudia`.
