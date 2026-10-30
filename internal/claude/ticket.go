package claude

import (
	"regexp"
	"strings"
	"sync"

	"github.com/MartyJRE/claudia/internal/config"
)

var (
	ticketOnce sync.Once
	ticketRe   *regexp.Regexp
)

// TicketFrom finds a Jira key in a branch name or title. With ticket_keys
// configured it matches those projects in any case (feat/abc-12/x); without,
// any upper-case KEY-123.
func TicketFrom(s string) string {
	ticketOnce.Do(func() {
		if keys := config.Get().TicketKeys; len(keys) > 0 {
			ticketRe = regexp.MustCompile(`(?i)\b(` + strings.Join(keys, "|") + `)-(\d+)\b`)
		} else {
			ticketRe = regexp.MustCompile(`\b([A-Z][A-Z0-9]+)-(\d+)\b`)
		}
	})
	m := ticketRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1]) + "-" + m[2]
}
