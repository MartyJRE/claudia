package launch

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MartyJRE/claudia/internal/claude"
	"github.com/MartyJRE/claudia/internal/config"
	"github.com/MartyJRE/claudia/internal/state"
	"github.com/MartyJRE/claudia/internal/tmux"
)

// Spec describes a `claude` process to start in tmux. The binary is called
// directly rather than through the `claude` shell function, which would prompt
// for the profile.
type Spec struct {
	Dir     string
	Profile claude.Profile
	Name    string
	Args    []string
	Session string // tmux session; defaults to the repo's name
	ID      string // the Claude session id, when known
	Kind    string // what it was started for; picks the window's icon
	Color   string // a /color name to give the session, if any
	Env     []string
}

// Colors are the names Claude's /color accepts.
var Colors = []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}

// Nerd Font (Material Design) icons per kind; anything else gets tmux.Icon.
var kindIcons = map[string]string{
	"review":         "\U000F0208", // md-eye
	"fix":            "\U000F05B7", // md-wrench
	"reply":          "\U000F045A", // md-reply
	"analysis":       "\U000F0349", // md-magnify
	"implementation": "\U000F0169", // md-code_braces
	"test":           "\U000F0668", // md-test_tube
	"continue":       "\U000F040A", // md-play
	"look":           "\U000F0337", // md-link
	"batch":          "\U000F0756", // md-format_list_checks
	"readiness":      "\U000F014C", // md-clipboard_check
	"mr":             "\U000F04C2", // md-source_pull
	"qa":             "\U000F014D", // md-clipboard_text
}

const kindsFile = "session-kinds.json"

var executable = os.Executable

func Resume(s claude.Session) Spec {
	return Spec{Dir: s.Dir(), Profile: s.Profile, Name: s.Name(), Args: append([]string{"--resume", s.ID}, hint()...), ID: s.ID}
}

// hint tells the session it can operate claudia itself.
// Self is how a session calls claudia back: this binary, else claudia on PATH.
func Self() string {
	if self, err := executable(); err == nil {
		return self
	}
	return "claudia"
}

func hint() []string {
	self := Self()
	return []string{"--append-system-prompt", fmt.Sprintf(
		"This session runs in tmux, started from claudia, the user's terminal dashboard. "+
			"`%[1]s ctl here` prints, as JSON, what this session is about: its MRs, its Jira ticket and the claudia inbox items waiting on the user for it. "+
			"When the user asks whether the thing is done or closed, check it (glab, Jira) and answer. "+
			"When it's handled, or the user wants to go back to claudia, run `%[1]s ctl dismiss --here` to clear its inbox items and `%[1]s ctl home` to switch the terminal back to claudia "+
			"(`%[1]s ctl home --close` also ends this session; only use it when the user asks to close it). `%[1]s ctl help` lists the rest. "+
			"`%[1]s teams help` reads and answers Microsoft Teams chats and threads, starts chats and posts, sends files and reads the calendar; send nothing there without the user's OK on the exact text.", self)}
}

// New starts a fresh session with a known id, so claudia can pair it with an MR or
// ticket before Claude writes anything.
func New(dir string, p claude.Profile, name, prompt string) (Spec, string) {
	id := uuid()
	args := append([]string{"--session-id", id, "-n", name}, hint()...)
	if prompt != "" {
		args = append(args, prompt)
	}
	return Spec{Dir: dir, Profile: p, Name: name, Args: args, ID: id}, id
}

// AsRun makes the session claudia run id of step on subject: it gets
// CLAUDIA_RUN and is told to end with a result line. The note joins the
// appended system prompt, since a second --append-system-prompt replaces the
// first. Whatever the run posts starts with commentNote.
func (sp Spec) AsRun(id, step, subject, commentNote string) Spec {
	note := fmt.Sprintf("This is claudia run %s, step %s for %s. When you're done, or stuck, end your last message with exactly one line "+
		"`CLAUDIA_RESULT: ok|needs-you|waiting|failed — <short reason>`; waiting means you asked someone else (on Jira, an MR, Teams) and the step can't go on until they answer. Keep your own context lean: push broad searches and big reads into subagents, since this session doesn't compact on its own.", id, step, subject)
	text, _ := json.Marshal(commentNote)
	note += fmt.Sprintf(" When you post anything to Jira or Confluence, use the Atlassian MCP with ADF content (contentFormat and responseContentFormat \"adf\" where the tool takes them) "+
		"and start the body with this panel node, then the rest as normal ADF: "+
		`{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":%s}]}]}.`+
		" When you post to GitLab (MR notes or threads), start with the same sentence as a markdown quote: `> %s`. Never prefix anything with \"(claudia)\".", text, commentNote)
	switch step {
	case "plan":
		note += fmt.Sprintf(" Write the plan as markdown to %s, replacing what's there: what changes where, the steps in order, and the open questions. "+
			"I'll read it and may iterate on it with you in this session before I OK it; keep that file up to date with every change we agree on, since implementation follows it. "+
			"The reason says how big the change is: `size: simple`, `size: medium` or `size: large`; simple is a small change with no open questions.", PlanFile(subject))
	case "implement":
		note += fmt.Sprintf(" If %s exists, it's the plan I OK'd for this ticket: follow it.", PlanFile(subject))
	case "review":
		note += " End with `CLAUDIA_RESULT: ok — no findings` when there's nothing the author should change, else `CLAUDIA_RESULT: needs-you — <n> findings`."
	}
	sp.Args = slices.Clone(sp.Args)
	if i := slices.Index(sp.Args, "--append-system-prompt"); i >= 0 && i+1 < len(sp.Args) {
		sp.Args[i+1] += " " + note
	} else {
		sp.Args = append(sp.Args, "--append-system-prompt", note)
	}
	sp.Env = append(slices.Clone(sp.Env), "CLAUDIA_RUN="+id)
	return sp
}

// PlanFile is where a ticket's plan step writes its plan.
func PlanFile(key string) string { return state.Path(filepath.Join("plans", key+".md")) }

// WithPermissionMode starts the session in that --permission-mode; "default"
// or "" leaves the user's own.
func (sp Spec) WithPermissionMode(mode string) Spec {
	if mode == "" || mode == "default" {
		return sp
	}
	sp.Args = append([]string{"--permission-mode", mode}, sp.Args...)
	return sp
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func Profile(name string, profiles []claude.Profile) claude.Profile {
	for _, p := range profiles {
		if p.Name == name {
			return p
		}
	}
	if len(profiles) == 0 {
		home, _ := os.UserHomeDir()
		return claude.Profile{Name: "default", Dir: filepath.Join(home, ".claude")}
	}
	return profiles[0]
}

// Start opens the session in a window of the tmux session named after the repo,
// the same naming `tp` uses, so Claude sits next to that repo's nvim and shell.
func Start(sp Spec) (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".local", "bin", "claude")
	}
	argv := []string{bin}
	if slices.Contains(config.Get().RemoteControl, sp.Profile.Name) {
		argv = append(argv, "--remote-control", sp.Name)
	}
	argv = append(argv, sp.Args...)
	if sp.Color != "" && sp.ID != "" {
		argv = withColor(bin, sp, argv)
	}
	env := append([]string{"CLAUDE_CONFIG_DIR=" + sp.Profile.Dir}, sp.Env...)
	session := sp.Session
	if session == "" {
		session = SessionName(sp.Dir)
	}
	return tmux.NewWindow(session, windowName(rememberKind(sp), sp.Name), sp.Dir, env, argv)
}

// withColor sets the colour with a headless /color run first (it costs no model
// call), then resumes the session interactively; Claude restores the colour on
// resume. Should the colour step fail, the session starts as it would have.
func withColor(bin string, sp Spec, argv []string) []string {
	flag, resume := "--resume", slices.Clone(argv)
	if i := slices.Index(resume, "--session-id"); i >= 0 {
		flag, resume[i] = "--session-id", "--resume"
	}
	script := "if " + shellJoin(bin, "-p", flag, sp.ID, "/color "+sp.Color) + " >/dev/null 2>&1; then exec " +
		shellJoin(resume...) + "; else exec " + shellJoin(argv...) + "; fi"
	return []string{"sh", "-c", script}
}

func shellJoin(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// rememberKind records what a new session is for, and looks it up when one is
// resumed, so it keeps its icon.
func rememberKind(sp Spec) string {
	if sp.ID == "" {
		return sp.Kind
	}
	kinds := Kinds()
	if sp.Kind == "" {
		return kinds[sp.ID]
	}
	kinds[sp.ID] = sp.Kind
	_ = state.Save(kindsFile, kinds)
	return sp.Kind
}

// Kinds maps session ids to what claudia started them for: review, fix, ….
func Kinds() map[string]string {
	kinds := map[string]string{}
	state.Load(kindsFile, &kinds)
	return kinds
}

func SessionName(dir string) string {
	root := dir
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		root = strings.TrimSpace(string(out))
	}
	name := strings.ReplaceAll(filepath.Base(root), ".", "_")
	if name == tmux.Home {
		name += "-dev" // claudia's own repo; the bare name is claudia's session
	}
	return name
}

func windowName(kind, name string) string {
	r := []rune(name)
	if len(r) > 32 {
		r = append(r[:31], '…')
	}
	icon, ok := kindIcons[kind]
	if !ok {
		icon = tmux.Icon
	}
	return icon + " " + string(r)
}

// ProfileFor guesses which Claude profile a directory belongs to: the work
// profile under the work folder, otherwise "personal", otherwise any other one.
func ProfileFor(dir string, profiles []claude.Profile) claude.Profile {
	c := config.Get()
	root := filepath.Join(c.CodeRoot, c.WorkDir)
	if dir == root || strings.HasPrefix(dir, root+"/") {
		return WorkProfile(profiles)
	}
	for _, p := range profiles {
		if p.Name == "personal" {
			return p
		}
	}
	for _, p := range profiles {
		if p.Name != c.WorkProfile {
			return p
		}
	}
	return Profile("", profiles)
}

// WorkProfile is the configured work profile, or the first one.
func WorkProfile(profiles []claude.Profile) claude.Profile {
	return Profile(config.Get().WorkProfile, profiles)
}

// AssistantSession is the tmux session the assistant lives in.
const AssistantSession = "assistant"

// Assistant is the long-lived helper session claudia opens on @: it knows the
// dashboard's own commands and asks before writing anywhere. id resumes it.
func Assistant(p claude.Profile, dir, id string, resume bool) Spec {
	self, err := executable()
	if err != nil {
		self = "claudia"
	}
	c := config.Get()
	note := fmt.Sprintf("You are the user's assistant inside claudia, their terminal dashboard for Claude Code, GitLab, Jira and Confluence. "+
		"They come here to ask what to do next and to get small things done. "+
		"`%[1]s ctl inbox` prints everyone waiting on them as JSON, oldest first; `%[1]s ctl help` lists more. "+
		"The latest status brief is in %[2]s. GitLab: use glab. Jira and Confluence: the Atlassian MCP.", self, filepath.Join(stateDir(), "brief.json"))
	if c.Vault != "" {
		note += " Their notes are an Obsidian vault at " + c.Vault + "; " + c.VaultProjects + "/<TICKET>/ holds a ticket's notes."
	}
	if c.Teams.ClientID != "" {
		note += " `" + self + " teams waiting` lists Teams chats, @mentions and meeting invites waiting on them, `" + self + " teams calendar` their meetings; `" + self + " teams help` shows how to read, answer, start chats and send files."
	}
	note += " Be brief and concrete and name tickets and MRs. Ask before anything that writes to GitLab, Jira, Confluence or Teams: approving, transitioning, commenting, posting, sending or pushing."
	args := []string{"--session-id", id, "-n", "assistant"}
	if resume {
		args = []string{"--resume", id}
	}
	args = append(args, "--append-system-prompt", note)
	return Spec{Dir: dir, Profile: p, Name: "assistant", Args: args, Session: AssistantSession}
}

// NewID makes a session id for a session claudia names up front.
func NewID() string { return uuid() }

func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "claudia")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "claudia")
}
