package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/MartyJRE/claudia/internal/ui"
)

const ctlHelp = `claudia ctl: operate claudia from a shell, e.g. from inside a Claude session.

  here                 JSON: this session's MRs, ticket and inbox items (found via $TMUX_PANE)
  inbox                JSON: every inbox item; "this_session" marks the ones about this session
  dismiss --here       snooze this session's inbox items until they change
  dismiss <id>...      snooze the given inbox items (ids from "inbox" or "here")
  home [--close]       switch the terminal back to claudia; --close also ends this session
`

func ctl(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(ctlHelp)
		return nil
	}
	switch args[0] {
	case "here":
		m := ui.Headless()
		r, ok := m.CurrentSession()
		if !ok {
			return errors.New("not inside a Claude session that claudia can see (needs tmux)")
		}
		return printJSON(m.CtlHere(r))
	case "inbox":
		m := ui.Headless()
		sid := ""
		if r, ok := m.CurrentSession(); ok {
			sid = r.ID
		}
		return printJSON(m.CtlInbox(sid))
	case "dismiss":
		m := ui.Headless()
		ids := args[1:]
		if len(ids) == 1 && ids[0] == "--here" {
			r, ok := m.CurrentSession()
			if !ok {
				return errors.New("not inside a Claude session that claudia can see (needs tmux)")
			}
			ids = nil
			for _, it := range m.CtlInbox(r.ID) {
				if it.ThisSession && !it.Snoozed {
					ids = append(ids, it.ID)
				}
			}
		}
		if len(ids) == 0 {
			fmt.Println("nothing to dismiss")
			return nil
		}
		fmt.Printf("snoozed %d item(s)\n", m.Snooze(ids))
		return nil
	case "home":
		if err := ui.Home(); err != nil {
			home() // claudia isn't running yet: start it and switch
		}
		if len(args) > 1 && args[1] == "--close" {
			// Kill the pane a moment later so this command can return first.
			pane := os.Getenv("TMUX_PANE")
			if pane != "" {
				return exec.Command("tmux", "run-shell", "-b", "sleep 1; tmux kill-pane -t '"+pane+"'").Run()
			}
		}
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], ctlHelp)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
