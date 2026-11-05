package plan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

var plan = []string{"# Plan", "", "## Design", "- keep state in a file", "- run in parallel", "- retry twice"}

func TestTextAndLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plan.md")
	os.WriteFile(p, []byte("# Plan\r\n\r\n1. do it\r\n\r\n"), 0o600)
	if got := Lines(Text(p, "fallback")); !slices.Equal(got, []string{"# Plan", "", "1. do it"}) {
		t.Errorf("lines %q", got)
	}
	if Text(filepath.Join(dir, "gone.md"), "from the transcript") != "from the transcript" || Text("", "x") != "x" {
		t.Error("a missing file falls back to the presented plan")
	}
	os.WriteFile(p, nil, 0o600)
	if Text(p, "x") != "x" || Lines("") != nil || Lines("\n\n") != nil {
		t.Error("empty")
	}
}

func TestNewAndFeedback(t *testing.T) {
	c := New(plan, 5, 4, "  one at a time, no retries \n")
	if c.From != 4 || c.To != 5 || !slices.Equal(c.Quote, []string{"- run in parallel", "- retry twice"}) || c.Text != "one at a time, no retries" {
		t.Fatalf("comment %+v", c)
	}
	plan[4] = "changed" // the quote is a copy
	if c.Quote[0] != "- run in parallel" {
		t.Error("quote aliases the plan")
	}
	plan[4] = "- run in parallel"
	got := Feedback([]Comment{c, New(plan, 3, 3, "put it in the DB")})
	want := "Comments on the plan:\n\n> - keep state in a file\nput it in the DB\n\n> - run in parallel\n> - retry twice\none at a time, no retries"
	if got != want {
		t.Errorf("feedback, in plan order:\n%s\nwant\n%s", got, want)
	}
}

func TestSaveLoadFollowsTheLines(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a, b := New(plan, 3, 3, "db"), New(plan, 4, 5, "serial")
	if err := Save("/plans/p.md", []Comment{a, b}); err != nil {
		t.Fatal(err)
	}
	Save("/plans/other.md", []Comment{New(plan, 0, 0, "x")})
	if got := Load("/plans/p.md", plan); len(got) != 2 || got[0].From != 3 || got[1].To != 5 {
		t.Fatalf("unchanged plan: %+v", got)
	}

	revised := append([]string{"# Plan", "", "## Context", "new line"}, plan[2:]...)
	revised = slices.Delete(revised, 7, 8) // "- retry twice" is gone
	got := Load("/plans/p.md", revised)
	if len(got) != 1 || got[0].Text != "db" || got[0].From != 5 || got[0].To != 5 {
		t.Errorf("comments move with their lines, gone ones drop: %+v", got)
	}

	dup := []string{"- x", "a", "- x"}
	Save("dup", []Comment{New(dup, 2, 2, "the second")})
	if got := Load("dup", dup); len(got) != 1 || got[0].From != 2 {
		t.Errorf("a repeated line keeps its place: %+v", got)
	}

	Save("/plans/p.md", nil)
	if Load("/plans/p.md", plan) != nil || len(Load("/plans/other.md", plan)) != 1 {
		t.Error("saving none removes only that plan's comments")
	}
	Save("empty", []Comment{{Text: "no quote"}})
	if Load("empty", plan) != nil {
		t.Error("a comment without lines can't be placed")
	}
}
