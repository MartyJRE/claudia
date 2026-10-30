package repos

import (
	"testing"
	"time"
)

func TestProjectFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@gitlab.example.com:acme/backend/payments.git": "acme/backend/payments",
		"https://gitlab.example.com/acme/infra.git":                  "acme/infra",
		"ssh://git@gitlab.com:2222/me/x.git":                               "me/x",
	} {
		if got := ProjectFromURL(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestByPreference(t *testing.T) {
	now := time.Now()
	in := []Repo{
		{Dir: "/c/Personal/a", Touched: now},
		{Dir: "/c/Work/old", Touched: now.Add(-time.Hour)},
		{Dir: "/c/Work/new", Touched: now.Add(-time.Minute)},
	}
	got := ByPreference(in, "/c")
	if got[0].Dir != "/c/Work/new" || got[1].Dir != "/c/Work/old" || got[2].Dir != "/c/Personal/a" {
		t.Errorf("got %v", got)
	}
}
