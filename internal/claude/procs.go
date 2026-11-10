package claude

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Proc is a claude process started for a known session id.
type Proc struct {
	PID  int
	Name string // its -n title
	Age  time.Duration
}

var psCommands = func() ([]byte, error) { return exec.Command("ps", "-axww", "-o", "pid=,etime=,command=").Output() }

// ProcessSessions is the claude processes by the session id they were started
// with, registered or not: claudia passes it as --session-id or --resume.
func ProcessSessions() map[string]Proc {
	out, err := psCommands()
	if err != nil {
		return nil
	}
	procs := map[string]Proc{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		p := Proc{PID: pid, Age: elapsed(f[1])}
		id := ""
		for i := 2; i+1 < len(f); i++ {
			switch f[i] {
			case "--session-id", "--resume":
				id = f[i+1]
			case "-n":
				p.Name = nameFrom(f[i+1:])
			}
		}
		if id != "" {
			procs[id] = p
		}
	}
	return procs
}

// nameFrom joins a -n title back up to the next flag; ps prints argv joined by spaces.
func nameFrom(rest []string) string {
	var words []string
	for _, w := range rest {
		if strings.HasPrefix(w, "--") {
			break
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// elapsed reads ps's etime, [[dd-]hh:]mm:ss.
func elapsed(s string) time.Duration {
	var days int
	if d, rest, ok := strings.Cut(s, "-"); ok {
		days, _ = strconv.Atoi(d)
		s = rest
	}
	var secs int
	for _, part := range strings.Split(s, ":") {
		n, _ := strconv.Atoi(part)
		secs = secs*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(secs)*time.Second
}
