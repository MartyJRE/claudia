package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MartyJRE/claudia/internal/ui"
)

const ctlHelp = `claudia ctl: operate claudia from a shell, e.g. from inside a Claude session.

  here                 JSON: this session's MRs, ticket and inbox items (found via $TMUX_PANE)
  inbox                JSON: every inbox item; "this_session" marks the ones about this session
  dismiss --here       snooze this session's inbox items until they change
  dismiss <id>...      snooze the given inbox items (ids from "inbox" or "here")
  home [--close]       switch the terminal back to claudia; --close also ends this session
  prio                 JSON: the order agreed at standup or grooming, first first
  prio <ref>... [--note <who, where>]
                       set that order: ticket keys, MR links or !IID (repo!IID when
                       ambiguous); it holds until the sprint it was agreed in closes
  prio --clear         forget the agreed order
  todo                 JSON: open todos, and the ones done in the last two weeks
  todo add <title> [--from <who>] [--note <text>] [--project <KEY>]
                       note an ask that isn't in Jira; it shows on the Board
  todo ticket <id> <KEY>
                       record the Jira ticket a todo became
  todo done <id>       tick a todo off
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
	case "prio":
		m := ui.Headless()
		var refs []string
		note, clear := "", false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--note":
				if i+1 >= len(args) {
					return errors.New("--note needs a value, e.g. --note \"Zdeněk, grooming\"")
				}
				i++
				note = args[i]
			case "--clear":
				clear = true
			default:
				refs = append(refs, args[i])
			}
		}
		if len(refs) > 0 || clear {
			if err := m.SetPrio(refs, note); err != nil {
				return err
			}
		}
		return printJSON(m.CtlPrio())
	case "todo":
		return ctlTodo(ui.Headless(), args[1:])
	case "home":
		if err := ui.Home(); err != nil {
			home() // claudia isn't running yet: start it and switch
		}
		if len(args) > 1 && args[1] == "--close" {
			// Kill the pane a moment later so this command can return first.
			pane := os.Getenv("TMUX_PANE")
			if pane != "" {
				return execCommand("tmux", "run-shell", "-b", "sleep 1; tmux kill-pane -t '"+pane+"'").Run()
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

func ctlTodo(m *ui.Model, args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		ts, err := m.CtlTodos()
		if err != nil {
			return err
		}
		return printJSON(ts)
	case "add":
		var title []string
		flags := map[string]string{}
		for i := 0; i < len(args); i++ {
			if f, ok := strings.CutPrefix(args[i], "--"); ok && (f == "from" || f == "note" || f == "project") {
				if i+1 >= len(args) {
					return fmt.Errorf("--%s needs a value", f)
				}
				i++
				flags[f] = args[i]
				continue
			}
			title = append(title, args[i])
		}
		if len(title) == 0 {
			return errors.New(`usage: todo add <title> [--from <who>] [--note <text>] [--project <KEY>]`)
		}
		t, err := m.AddTodo(strings.Join(title, " "), flags["from"], flags["note"], flags["project"])
		if err != nil {
			return err
		}
		return printJSON(t)
	case "ticket":
		if len(args) != 2 {
			return errors.New("usage: todo ticket <id> <KEY>")
		}
		if err := m.TicketTodo(args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("todo %s is %s now\n", strings.TrimPrefix(args[0], "todo:"), strings.ToUpper(args[1]))
		return nil
	case "done":
		if len(args) != 1 {
			return errors.New("usage: todo done <id>")
		}
		if err := m.DoneTodo(args[0]); err != nil {
			return err
		}
		fmt.Printf("ticked off todo %s\n", strings.TrimPrefix(args[0], "todo:"))
		return nil
	}
	return fmt.Errorf("unknown todo command %q\n\n%s", sub, ctlHelp)
}
