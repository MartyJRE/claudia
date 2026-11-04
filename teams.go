package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MartyJRE/claudia/internal/teams"
)

const teamsHelp = `claudia teams: Microsoft Teams and your calendar, as you.

  login                           sign in (device code); needed once
  channels                        JSON: your teams' channels
  waiting                         JSON: chats, @mentions, invites and mail where it's your turn
  show <ref>                      the conversation a ref points into, oldest first, with files
  posts [--mr <url>]              JSON: review-channel posts, or the ones about an MR

  reply <ref|mr-url> <text>       send into the chat, or reply in the thread
  react <ref|mr-url> <emoji>      react to the message, e.g. ✅ or 👍
  chat <who>[,<who>…] <text>      write to people (name or email); several make a group chat
  post <Team/Channel> <text>      start a new thread in a channel
  reply, chat and post take --file <path> (repeatable) to attach files.

  who <name|email>                JSON: find a person
  download <file-url> [dir]       save a file shared in Teams (default: current dir)

  calendar [days]                 your meetings from now, default 1 day
  respond <event-ref> accept|tentative|decline [comment]

A ref is chat/<chat>/<msg>, <team>/<channel>/<msg>, event/<id>, mail/<id>, or a
review-channel message id; waiting, posts, chat, post and calendar print them. An MR
URL picks the newest review post about it.
`

var newTeams = func() (teamsClient, error) { return teams.New() }

type teamsClient interface {
	Login(func(string)) error
	Channels() ([]teams.Channel, error)
	Fetch() (teams.Snapshot, error)
	FetchReview() ([]teams.Post, error)
	Show(ref string) ([]teams.Line, error)
	Reply(ref, text string, files ...string) error
	React(ref, emoji string) error
	Chat(who []string, text string, files ...string) (string, error)
	Post(channel, text string, files ...string) (string, error)
	Person(who string) (teams.Person, error)
	Download(link, dir string) (string, error)
	Calendar(from, to time.Time) ([]teams.Event, error)
	Respond(ref, answer, comment string) error
}

var clock = time.Now

// fileFlags takes every --file <path> out of args.
func fileFlags(args []string) (rest, files []string, err error) {
	for i := 0; i < len(args); i++ {
		if args[i] != "--file" {
			rest = append(rest, args[i])
			continue
		}
		if i+1 == len(args) {
			return nil, nil, errors.New("--file needs a path")
		}
		files = append(files, args[i+1])
		i++
	}
	return rest, files, nil
}

func teamsCmd(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(teamsHelp)
		return nil
	}
	args, files, err := fileFlags(args)
	if err != nil {
		return err
	}
	c, err := newTeams()
	if err != nil {
		return err
	}
	need := func(n int, usage string) error {
		if len(args) < n {
			return errors.New("usage: claudia teams " + usage)
		}
		return nil
	}
	switch args[0] {
	case "login":
		if err := c.Login(func(s string) { fmt.Println(s) }); err != nil {
			return err
		}
		fmt.Println("signed in")
		return nil
	case "channels":
		chs, err := c.Channels()
		if err != nil {
			return err
		}
		return printJSON(chs)
	case "waiting":
		s, err := c.Fetch()
		if err != nil {
			return err
		}
		if s.MailErr != "" {
			fmt.Fprintln(os.Stderr, "Outlook mail: "+s.MailErr)
		}
		if s.Waiting == nil {
			s.Waiting = []teams.Waiting{}
		}
		return printJSON(s.Waiting)
	case "show":
		if err := need(2, "show <ref>"); err != nil {
			return err
		}
		lines, err := c.Show(args[1])
		if err != nil {
			return err
		}
		for _, l := range lines {
			fmt.Printf("%s  %s: %s\n", l.At.Local().Format("Mon 2 Jan 15:04"), l.From, l.Text)
			for _, f := range l.Files {
				fmt.Printf("    📎 %s  %s\n", f.Name, f.URL)
			}
		}
		return nil
	case "posts":
		all, err := c.FetchReview()
		if err != nil {
			return err
		}
		posts := all
		if len(args) == 3 && args[1] == "--mr" {
			posts = teams.Snapshot{Posts: all}.ForMR(args[2])
		}
		if posts == nil {
			posts = []teams.Post{}
		}
		return printJSON(posts)
	case "reply", "react":
		if err := need(3, args[0]+" <ref|mr-url> <"+map[string]string{"reply": "text", "react": "emoji"}[args[0]]+">"); err != nil {
			return err
		}
		id, err := postID(c, args[1])
		if err != nil {
			return err
		}
		if args[0] == "react" {
			return c.React(id, args[2])
		}
		return c.Reply(id, strings.Join(args[2:], " "), files...)
	case "chat", "post":
		if err := need(3, args[0]+" <"+map[string]string{"chat": "who", "post": "Team/Channel"}[args[0]]+"> <text>"); err != nil {
			return err
		}
		text := strings.Join(args[2:], " ")
		var ref string
		if args[0] == "chat" {
			ref, err = c.Chat(strings.Split(args[1], ","), text, files...)
		} else {
			ref, err = c.Post(args[1], text, files...)
		}
		if err != nil {
			return err
		}
		fmt.Println(ref)
		return nil
	case "who":
		if err := need(2, "who <name|email>"); err != nil {
			return err
		}
		p, err := c.Person(strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		return printJSON(p)
	case "download":
		if err := need(2, "download <file-url> [dir]"); err != nil {
			return err
		}
		dir := "."
		if len(args) > 2 {
			dir = args[2]
		}
		p, err := c.Download(args[1], dir)
		if err != nil {
			return err
		}
		fmt.Println(p)
		return nil
	case "calendar":
		days := 1
		if len(args) > 1 {
			if days, err = strconv.Atoi(args[1]); err != nil || days < 1 {
				return errors.New("usage: claudia teams calendar [days]")
			}
		}
		from := clock()
		evs, err := c.Calendar(from, from.AddDate(0, 0, days))
		if err != nil {
			return err
		}
		for _, e := range evs {
			fmt.Printf("%s–%s  %s  (%s, %s)  %s\n", e.Start.Local().Format("Mon 2 Jan 15:04"), e.End.Local().Format("15:04"), e.Subject, e.Organizer, e.Response, e.Ref())
		}
		return nil
	case "respond":
		if err := need(3, "respond <event-ref> accept|tentative|decline [comment]"); err != nil {
			return err
		}
		return c.Respond(args[1], args[2], strings.Join(args[3:], " "))
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], teamsHelp)
}

func postID(c teamsClient, ref string) (string, error) {
	if !strings.HasPrefix(ref, "http") {
		return ref, nil
	}
	all, err := c.FetchReview()
	if err != nil {
		return "", err
	}
	posts := teams.Snapshot{Posts: all}.ForMR(ref)
	if len(posts) == 0 {
		return "", errors.New("no Teams post links to " + ref)
	}
	return posts[0].ID, nil
}
