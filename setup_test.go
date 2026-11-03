package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeSetup stubs every seam; fail names the command (first word) that
// should exit non-zero. It returns the commands run.
func fakeSetup(t *testing.T, fail string, check func(name string)) (*[]string, *bytes.Buffer) {
	t.Helper()
	src, look, cmd, tmp, home, out, errw := setupSrc, setupLookPath, setupCommand, setupMkdirTemp, setupHome, setupOut, setupErr
	t.Cleanup(func() {
		setupSrc, setupLookPath, setupCommand, setupMkdirTemp, setupHome, setupOut, setupErr = src, look, cmd, tmp, home, out, errw
	})
	var ran []string
	buf := &bytes.Buffer{}
	setupLookPath = func(string) (string, error) { return "/usr/bin/xcrun", nil }
	setupHome = func() (string, error) { return "/home/me", nil }
	setupOut, setupErr = buf, buf
	setupCommand = func(name string, args ...string) *exec.Cmd {
		ran = append(ran, strings.Join(append([]string{name}, args...), " "))
		if check != nil {
			check(name)
		}
		if filepath.Base(name) == fail {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	return &ran, buf
}

func TestSetup(t *testing.T) {
	var dir string
	ran, out := fakeSetup(t, "", func(name string) {
		if filepath.Base(name) != "build.sh" {
			return
		}
		dir = filepath.Dir(name)
		for _, f := range []string{"Package.swift", "build.sh", "Sources/NotifierCore/Notifier.swift", "Sources/NotifierCore/Icon.swift", "Sources/claudia-notify/main.swift", "Sources/claudia-icon/main.swift", "Tests/NotifierCoreTests/NotifierTests.swift"} {
			st, err := os.Stat(filepath.Join(dir, f))
			if err != nil || st.Mode()&0o100 == 0 {
				t.Errorf("%s: %v %v", f, st, err)
			}
		}
	})
	if err := setup(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"xcrun --find swift",
		filepath.Join(dir, "build.sh"),
		"/home/me/Applications/Claudia.app/Contents/MacOS/claudia-notify post --title claudia notifications work --body Clicking a notification now jumps to the session or opens the link. --id setup",
	}
	if strings.Join(*ran, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran %q", *ran)
	}
	if !strings.HasSuffix(out.String(), "done\n") {
		t.Errorf("out %q", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("temp dir left behind: %v", err)
	}
}

func TestSetupFailures(t *testing.T) {
	for _, tc := range []struct {
		name, fail, want string
		stub             func()
	}{
		{name: "no xcrun", want: "needs the Xcode command line tools", stub: func() {
			setupLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
		}},
		{name: "no swift", fail: "xcrun", want: "swift not found"},
		{name: "no temp dir", want: "no tmp", stub: func() {
			setupMkdirTemp = func(string, string) (string, error) { return "", errors.New("no tmp") }
		}},
		{name: "no sources", want: "file does not exist", stub: func() { setupSrc = fstest.MapFS{} }},
		{name: "build fails", fail: "build.sh", want: "exit status 1"},
		{name: "test notification fails", fail: "claudia-notify", want: "built, but the test notification failed (exit status 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeSetup(t, tc.fail, nil)
			if tc.stub != nil {
				tc.stub()
			}
			if err := setup(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

type unreadableFS struct{ fstest.MapFS }

func (unreadableFS) ReadFile(string) ([]byte, error) { return nil, fs.ErrPermission }

func TestWriteTreeErrors(t *testing.T) {
	src := fstest.MapFS{"n/a/b": {Data: []byte("x")}}
	if err := writeTree(unreadableFS{src}, "n", t.TempDir()); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("unreadable: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o644)
	if err := writeTree(src, "n", file); err == nil {
		t.Error("dir is a file: no error")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	if err := writeTree(src, "n", dir); err == nil {
		t.Error("file is a dir: no error")
	}
}
