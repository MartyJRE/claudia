package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// Tests must not read the real ~/.config/claudia.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "claudia-test")
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_STATE_HOME", dir)
	os.Setenv("CLAUDIA_GITLAB_HOST", "gitlab.example.com")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func stdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	w.Close()
	return <-done
}

func TestCtlHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		var err error
		if out := stdout(t, func() { err = ctl(args) }); out != ctlHelp || err != nil {
			t.Errorf("%v: %q %v", args, out, err)
		}
	}
}

func TestCtlUnknown(t *testing.T) {
	err := ctl([]string{"frobnicate"})
	if err == nil || !strings.HasPrefix(err.Error(), `unknown command "frobnicate"`) || !strings.Contains(err.Error(), "dismiss --here") {
		t.Errorf("got %v", err)
	}
}

func TestPrintJSON(t *testing.T) {
	out := stdout(t, func() {
		if err := printJSON(map[string]any{"id": "mr:1", "n": 2}); err != nil {
			t.Error(err)
		}
	})
	if out != "{\n  \"id\": \"mr:1\",\n  \"n\": 2\n}\n" {
		t.Errorf("got %q", out)
	}
}

func TestVersion(t *testing.T) {
	// Test binaries carry neither a module version nor vcs stamps.
	if v := version(); v != "unknown (local build)" {
		t.Errorf("got %q", v)
	}
}
