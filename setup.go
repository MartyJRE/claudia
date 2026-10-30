package main

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The notification helper ships inside the binary, so `go install` users can
// build it without cloning the repo.
//
//go:embed notifier/main.swift notifier/icon.swift notifier/build.sh
var notifierSrc embed.FS

// setup builds ~/Applications/Claudia.app, the native notification helper.
// Without it notifications fall back to AppleScript, which macOS attributes
// to Script Editor: clicking one opens Script Editor.
func setup() error {
	if _, err := exec.LookPath("xcrun"); err != nil {
		return errors.New("needs the Xcode command line tools: run xcode-select --install, then claudia setup again")
	}
	if err := exec.Command("xcrun", "--find", "swiftc").Run(); err != nil {
		return errors.New("swiftc not found: run xcode-select --install, then claudia setup again")
	}
	dir, err := os.MkdirTemp("", "claudia-notifier")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for _, name := range []string{"main.swift", "icon.swift", "build.sh"} {
		b, err := notifierSrc.ReadFile("notifier/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o755); err != nil {
			return err
		}
	}
	fmt.Println("building the notification helper (takes a minute)…")
	c := exec.Command(filepath.Join(dir, "build.sh"))
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	bin := filepath.Join(home, "Applications", "Claudia.app", "Contents", "MacOS", "claudia-notify")
	fmt.Println("sending a test notification; allow notifications for claudia if macOS asks")
	test := exec.Command(bin, "post", "--title", "claudia notifications work", "--body", "Clicking a notification now jumps to the session or opens the link.", "--id", "setup")
	test.Stdout, test.Stderr = os.Stdout, os.Stderr
	if err := test.Run(); err != nil {
		return fmt.Errorf("built, but the test notification failed (%v): allow claudia in System Settings → Notifications", err)
	}
	// A running watcher keeps the old fallback in memory only until its next
	// notification; it looks the helper up each time, so nothing to restart.
	fmt.Println("done")
	return nil
}
