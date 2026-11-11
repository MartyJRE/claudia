package launch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAsRun(t *testing.T) {
	sp, id := New("/code/x", work, "plan: Card form", "plan it")
	run := sp.AsRun("run-1", "plan", "ABC-2", "By Claude \"for\" me.")
	if !slices.Equal(run.Env, []string{"CLAUDIA_RUN=run-1"}) || len(sp.Env) != 0 {
		t.Errorf("env %q, original %q", run.Env, sp.Env)
	}
	if len(run.Args) != len(sp.Args) || slices.Index(run.Args, "--append-system-prompt") != 4 || run.Args[1] != id {
		t.Fatalf("args %q", run.Args)
	}
	note := run.Args[5]
	for _, want := range []string{"ctl here", "This is claudia run run-1, step plan for ABC-2.", "`CLAUDIA_RESULT: ok|needs-you|waiting|failed — <short reason>`", "waiting means you asked someone else", "`size: simple`", "Write the plan as markdown to " + PlanFile("ABC-2"), "push broad searches and big reads into subagents",
		`{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":"By Claude \"for\" me."}]}]}`,
		"`> By Claude \"for\" me.`", `"adf"`, `Never prefix anything with "(claudia)"`} {
		if !strings.Contains(note, want) {
			t.Errorf("note misses %q: %s", want, note)
		}
	}
	if strings.Contains(sp.Args[5], "claudia run") {
		t.Error("the original spec changed")
	}
	bare := Spec{Args: []string{"--resume", "s1"}, Env: []string{"A=1"}}.AsRun("run-2", "review", "!5", "n")
	if !slices.Equal(bare.Args[:3], []string{"--resume", "s1", "--append-system-prompt"}) || !strings.Contains(bare.Args[3], "run-2") || !strings.Contains(bare.Args[3], "ok — no findings") || strings.Contains(bare.Args[3], "size:") {
		t.Errorf("args without a hint %q", bare.Args)
	}
	if impl := bare.AsRun("run-3", "implement", "ABC-2", "n"); !strings.Contains(impl.Args[3], PlanFile("ABC-2")+" exists, it's the plan I OK'd") {
		t.Errorf("implement isn't pointed at the plan: %s", impl.Args[3])
	}
	if !slices.Equal(bare.Env, []string{"A=1", "CLAUDIA_RUN=run-2"}) {
		t.Errorf("env %q", bare.Env)
	}
}

func TestStartEnv(t *testing.T) {
	privateTmux(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "env")
	script := "#!/bin/sh\necho \"$CLAUDIA_RUN $CLAUDE_CONFIG_DIR\" > " + log + "\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	sp := Spec{Dir: t.TempDir(), Profile: personal, Name: "n", Session: "s"}.AsRun("run-7", "plan", "ABC-1", "n")
	if _, err := Start(sp); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for i := 0; i < 200 && len(got) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		got, _ = os.ReadFile(log)
	}
	if strings.TrimSpace(string(got)) != "run-7 /p/personal" {
		t.Errorf("env in the window %q", got)
	}
}

func TestWithPermissionMode(t *testing.T) {
	sp, _ := New("/code/x", work, "n", "go")
	if got := sp.WithPermissionMode("auto"); !slices.Equal(got.Args[:2], []string{"--permission-mode", "auto"}) || got.Args[len(got.Args)-1] != "go" || sp.Args[0] == "--permission-mode" {
		t.Errorf("args %q", got.Args)
	}
	for _, m := range []string{"", "default"} {
		if got := sp.WithPermissionMode(m); !slices.Equal(got.Args, sp.Args) {
			t.Errorf("%q: %q", m, got.Args)
		}
	}
}
