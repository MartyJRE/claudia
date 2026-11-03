package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// The notification helper ships inside the binary, so `go install` users can
// build it without cloning the repo.
//
//go:embed notifier/Package.swift notifier/build.sh notifier/Sources notifier/Tests
var notifierSrc embed.FS

// Seams for setup_test.go.
var (
	setupSrc       fs.FS     = notifierSrc
	setupLookPath            = exec.LookPath
	setupCommand             = exec.Command
	setupMkdirTemp           = os.MkdirTemp
	setupHome                = os.UserHomeDir
	setupOut       io.Writer = os.Stdout
	setupErr       io.Writer = os.Stderr
)

// setup builds ~/Applications/Claudia.app, the native notification helper.
// Without it notifications fall back to AppleScript, which macOS attributes
// to Script Editor: clicking one opens Script Editor.
func setup() error {
	if _, err := setupLookPath("xcrun"); err != nil {
		return errors.New("needs the Xcode command line tools: run xcode-select --install, then claudia setup again")
	}
	if err := setupCommand("xcrun", "--find", "swift").Run(); err != nil {
		return errors.New("swift not found: run xcode-select --install, then claudia setup again")
	}
	dir, err := setupMkdirTemp("", "claudia-notifier")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := writeTree(setupSrc, "notifier", dir); err != nil {
		return err
	}
	fmt.Fprintln(setupOut, "building the notification helper (takes a minute)…")
	c := setupCommand(filepath.Join(dir, "build.sh"))
	c.Stdout, c.Stderr = setupOut, setupErr
	if err := c.Run(); err != nil {
		return err
	}
	home, _ := setupHome()
	bin := filepath.Join(home, "Applications", "Claudia.app", "Contents", "MacOS", "claudia-notify")
	fmt.Fprintln(setupOut, "sending a test notification; allow notifications for claudia if macOS asks")
	test := setupCommand(bin, "post", "--title", "claudia notifications work", "--body", "Clicking a notification now jumps to the session or opens the link.", "--id", "setup")
	test.Stdout, test.Stderr = setupOut, setupErr
	if err := test.Run(); err != nil {
		return fmt.Errorf("built, but the test notification failed (%v): allow claudia in System Settings → Notifications", err)
	}
	// A running watcher keeps the old fallback in memory only until its next
	// notification; it looks the helper up each time, so nothing to restart.
	fmt.Fprintln(setupOut, "done")
	return nil
}

// writeTree copies root out of fsys into dir, keeping the layout.
func writeTree(fsys fs.FS, root, dir string) error {
	return fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o755)
	})
}
